package main

import (
	"strings"
	"testing"
)

// A default reserve_commit stack whose growable has a proven bound is backed by a frame
// block when the bound fits at run time, and keeps the lazy reservation otherwise. Exercise
// the small (frame block), large (fallback), and push-grown shapes, plus recursion (each
// frame owns its own block), and check results against a plain recomputation.
const regionStackBlockBody = `
def steps(n: i64) -> i64:
    if n < 1 or n > 100000:
        return -1
    table: mutable darray[i64] = [n + 1 for _ in 0..<(n + 1).usize()]
    table[1] <- 0
    i: mutable i64 = 1
    while i < n:
        c: i64 = table[i.usize()] + 1
        table[(i + 1).usize()] <- c if table[(i + 1).usize()] > c
        if i * 2 <= n and table[(i * 2).usize()] > c:
            table[(i * 2).usize()] <- c
        if i * 3 <= n and table[(i * 3).usize()] > c:
            table[(i * 3).usize()] <- c
        i <- i + 1
    table[n.usize()]

def pushed(k: i64) -> i64:
    xs: mutable darray[i64] = []
    for j in 0..<k:
        xs.push(j)
    total: mutable i64 = 0
    for x in xs:
        total <- total + x
    total

def nested(depth: i64) -> i64:
    if depth == 0:
        return 0
    xs: mutable darray[i64] = [depth for _ in 0..<4]
    inner: i64 = nested(depth - 1)
    xs[0] + xs[3] + inner

def guarded(n: i64) -> i64:
    if n < 0:
        return -1
    xs: mutable darray[u64] = []
    total: mutable u64 = 0
    factor: mutable u64 = 1
    while factor <= 3:
        size: usize = (n.u64() * factor).usize()
        if size > 600:
            return -2
        xs.resize(size)
        i: mutable usize = 0
        while i < size:
            xs[i] <- factor
            i <- i + 1
        for x in xs:
            total <- total + x
        factor <- factor + 1
    total.i64()

def local_sized(n: i64) -> i64:
    if n < 1 or n > 64:
        return -1
    k: usize = n.usize()
    m: usize = k
    xs: mutable darray[i64] = [2 for _ in 0..<k]
    ys: mutable darray[i64] = [3 for _ in 0..<m]
    xs[k - 1] + ys[0]

def open_low(n: i64) -> i64:
    if n > 64:
        return -1
    k: usize = n.usize()
    xs: mutable darray[i64] = [2 for _ in 0..<k]
    xs.count.i64()

@test
def region_stack_block() -> void:
    can Abort.Panic:
        if steps(1) != 0 or steps(6) != 2 or steps(15) != 4:
            panic("small steps")
        if steps(10) != 3 or steps(400) != 9:
            panic("mid steps")
        if steps(5000) != 13 or steps(0) != -1 or steps(-7) != -1:
            panic("large or invalid steps")
        if pushed(10) != 45 or pushed(0) != 0 or pushed(3000) != 4498500:
            panic("pushed")
        if nested(50) != 2550:
            panic("nested")
        if guarded(5) != 70 or guarded(0) != 0 or guarded(200) != 2800 or guarded(201) != -2:
            panic("guarded resize")
        if local_sized(1) != 5 or local_sized(64) != 5 or local_sized(0) != -1 or open_low(3) != 3:
            panic("local sized")
`

func TestRegionStackBlock(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "region_stack_block", regionStackBlockBody)
	assertAllPassed(t, exit, stdout, stderr, "region_stack_block")
}

// steps and nested get frame blocks, and guarded gets an exactly-sized one from its
// guard-proven resize bound. local_sized gets two from the guard range carried through
// `n.usize()` and the plain copy; open_low (no lower bound: a negative n wraps) gets none. pushed is auto-reserved onto the shared stack 0, which keeps the
// region's main arena and never takes this path.
func TestRegionStackBlockEmitted(t *testing.T) {
	t.Parallel()
	exit, out, stderr := compileStressProgram(t, "region_stack_block_ir", regionStackBlockBody)
	if exit != 0 {
		t.Fatalf("emit llvm: exit %d\n%s", exit, stderr)
	}
	if n := strings.Count(out, "call void @arena_init_stack_block"); n != 5 {
		t.Fatalf("expected frame blocks for steps, nested, guarded and local_sized (2), got %d calls", n)
	}
}
