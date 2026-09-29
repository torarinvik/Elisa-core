package main

import (
	"strings"
	"testing"
)

// darray GROWTH WITH A LIVE HEADER COPY: `b = a` copies the header and shares the buffer
// (docs/84). When `a` then grows past its block and the arena moves it, `b` still reads
// the old block. arena_realloc used to push that block onto the region's free list, so
// the next allocation of the same size class (`c`) was carved out of it and `b[0]` read
// `c`'s elements — a silent wrong answer in safe code, no diagnostic, no crash.
const darrayGrowthHeaderCopyBody = `
def build_copy_then_grow() -> i64:
    can Memory.Allocate, Abort.Panic:
        a: mutable darray[i64] = []
        for i in 0 ..< 4 |a|:
            a <- a.push(42)
        b: darray[i64] = a
        # d takes the arena tail, so a's growth must MOVE instead of extending in place.
        d: mutable darray[i64] = []
        d <- d.push(5)
        for i in 0 ..< 100000 |a|:
            a <- a.push(7)
        c: mutable darray[i64] = []
        for i in 0 ..< 100000 |c|:
            c <- c.push(9)
        return b[0] + b[1] + b[2] + b[3] + c[0] + d[0] - 14

@test
def darray_growth_keeps_header_copy() -> void:
    can Memory.Allocate, Abort.Panic:
        if build_copy_then_grow() != 168:
            panic("header copy read a reclaimed block (UAF)")
`

func TestDarrayGrowthKeepsHeaderCopyBlock(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "darray_growth_header_copy", darrayGrowthHeaderCopyBody)
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "darray_growth_keeps_header_copy")
}
