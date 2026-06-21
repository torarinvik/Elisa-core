package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Arena-backed packed/SoA store columns.
//
// A packed-enum "store" keeps its node payload + per-node metadata in COLUMNS.
// These tests pin that those columns (and the store state struct) are allocated
// through the ACTIVE REGION's arena — not the heap — and that the result is
// bit-correct. arena_used_bytes / arena_region_count (read-only introspection
// helpers in arena.elisa) measure the arena footprint to prove the placement.
//
//   1. ColumnsLiveOnArena  — a tree built inside an explicit `in a:` arena scope
//      leaves a large committed-byte footprint IN THAT ARENA (heap-backed columns
//      would leave the arena empty), and reads back correctly.
//   2. RegionPolyColumnsOnArena — the same through a region-polymorphic recursive
//      builder (`new[auto]` with the caller's region threaded in), so the column
//      growth across the recursion lands on the caller's arena.
//   3. Correct — a deeper tree summed back through storeless match equals the leaf
//      count (no aliasing/garbage from the column layout under many growth cycles).

func buildAndRunSoATest(t *testing.T, name, src, arg string) (string, time.Duration) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, name+".elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr strings.Builder
	if code := runCLI([]string{"-emit", "test", "-O2", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	exePath := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if idx := strings.Index(line, "test binary: "); idx >= 0 {
			exePath = strings.TrimSpace(line[idx+len("test binary: "):])
			break
		}
	}
	if exePath == "" {
		t.Skipf("could not locate kept test binary:\n%s", stderr.String())
	}
	defer os.Remove(exePath)
	defer os.RemoveAll(exePath + ".dSYM")
	start := time.Now()
	out, err := exec.Command(exePath, arg).CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run %q failed: %v\noutput:\n%s", arg, err, string(out))
	}
	return string(out), elapsed
}

const soaPackedEnumDecl = `
packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)
`

// A depth-13 packed-enum tree (8192 leaves) built via new[auto] and summed back
// through storeless match must equal 8192. Exercises many column-growth (geometric
// realloc) cycles on the arena-backed path.
func TestSoAArenaBackedPackedTreeCorrect(t *testing.T) {
	src := soaPackedEnumDecl + `
def make(depth: i64) -> Expr:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Expr.Int(span: 0, value: 1)
        return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

def eval(node: Expr) -> i64:
    match node:
        Expr.Int(value: v):
            return v
        Expr.Add(left: l, right: r):
            return eval(l) + eval(r)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Expr = make(13)
        if eval(root) != 8192:
            panic("arena-backed packed tree produced wrong sum")
`
	_, elapsed := buildAndRunSoATest(t, "soa_correct", src, "bt")
	t.Logf("depth-13 packed tree (8192 leaves) built+summed in %s", elapsed)
}

// Arena-backing assertion (direct): build a tree inside an explicit `in a:` arena
// scope and measure arena_used_bytes(&a). A depth-12 tree's columns+payload are
// tens of KiB; heap-backed columns would leave `a` holding only the tiny state
// struct, so we require a healthy lower bound AND a non-zero region count, while
// still verifying the value read back is correct.
func TestSoAArenaBackedColumnsLiveOnArena(t *testing.T) {
	src := soaPackedEnumDecl + `
def make(depth: i64) -> Expr:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Expr.Int(span: 0, value: 1)
        return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

def eval(node: Expr) -> i64:
    match node:
        Expr.Int(value: v):
            return v
        Expr.Add(left: l, right: r):
            return eval(l) + eval(r)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable Arena = zeroed
        before: usize = arena_used_bytes(&a)
        if before != 0:
            panic("fresh arena was not empty")
        sum: mutable i64 = 0
        in a:
            root: Expr = make(12)
            sum <- eval(root)
        after: usize = arena_used_bytes(&a)
        regions: usize = arena_region_count(&a)
        if sum != 4096:
            panic("arena-backed packed tree produced wrong sum")
        # A depth-12 tree (8191 nodes) packs into well over 8 KiB of columns+payload.
        if after < 8192:
            panic("store columns did NOT land on the arena (heap-backed regression)")
        if regions < 1:
            panic("arena has no regions after building store")
        arena_free(&a)
`
	buildAndRunSoATest(t, "soa_on_arena", src, "bt")
}

// Arena-backing through a region-polymorphic builder: `make` takes no arena and uses
// new[auto]; the caller's `in a:` region is threaded into it. The store's columns must
// therefore grow on the caller's arena `a` across the whole recursion, not on a
// separate/heap buffer. (Direct local-arena form — exercises the region-poly call
// argument threading path end to end.)
func TestSoAArenaBackedRegionPolyColumnsOnArena(t *testing.T) {
	src := soaPackedEnumDecl + `
def make(depth: i64) -> Expr:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Expr.Int(span: 0, value: 1)
        return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable Arena = zeroed
        in a:
            r: Expr = make(10)
            _ = r
        used: usize = arena_used_bytes(&a)
        if used < 8192:
            panic("region-poly store columns did NOT land on the caller arena")
        arena_free(&a)
`
	buildAndRunSoATest(t, "soa_regionpoly", src, "bt")
}
