package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/74 steps 2-3, single function (no cross-function threading): `new[auto] Expr.V(...)` for a
// packed enum allocates into an implicit region-backed store (columns on the `in auto:` arena), and
// storeless `match node:` (no `in Store:`) recovers that store from the active binding. Builds
// Add(Int 5, Int 7) and reads it back via nested storeless matches → 12. A broken store binding
// would read garbage or fail to compile.
func TestRegionBackedPackedEnumSingleFunction(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: Expr = new[auto] Expr.Int(span: 0, value: 5)
            b: Expr = new[auto] Expr.Int(span: 0, value: 7)
            root: Expr = new[auto] Expr.Add(span: 0, left: a, right: b)
            sum: mutable i64 = 0
            match root:
                Expr.Int(value: v):
                    sum <- v
                Expr.Add(left: l, right: r):
                    match l:
                        Expr.Int(value: lv):
                            sum <- sum + lv
                        Expr.Add(left: l2, right: r2):
                            sum <- sum + 100
                    match r:
                        Expr.Int(value: rv):
                            sum <- sum + rv
                        Expr.Add(left: l3, right: r3):
                            sum <- sum + 100
            if sum != 12:
                panic("region-backed packed enum produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bt_packed.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
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
	if out, err := exec.Command(exePath, "bt").CombinedOutput(); err != nil {
		t.Fatalf("region-backed packed enum run failed: %v\noutput:\n%s", err, string(out))
	}
}

// docs/74 + docs/75 milestone: the recursive region-backed packed-enum binary tree with ZERO
// ceremony — no explicit Store, no `in store:`, no hand-threaded params. `make` (region-polymorphic,
// returns Expr) builds a depth-10 Add-tree of 1024 Int leaves via `new[auto] Expr.V`; the implicit
// region-backed store is created in `bt`'s `in auto:` and auto-threaded into both `make` and the
// consumer `eval` (which matches storelessly). eval recursively sums the leaves → 1024. A broken
// store thread would read garbage across the function boundary.
func TestRegionBackedPackedTreeRecursiveBuildAndEval(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

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
        in auto:
            root: Expr = make(10)
            if eval(root) != 1024:
                panic("region-backed packed tree produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bt_rec.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
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
	if out, err := exec.Command(exePath, "bt").CombinedOutput(); err != nil {
		t.Fatalf("recursive region-backed packed tree run failed: %v\noutput:\n%s", err, string(out))
	}
}

// docs/75: a recursive `new[auto]` builder. `make` is region-polymorphic — it threads the caller's
// region (the `in auto:` in `bt`) through every recursive call, so all 101 nodes land in ONE region
// and each survives to be read by the next level. depth-100 adds 1 per level → value 101. If
// threading were broken (a per-call arena freed on return), `inner.value` would read freed memory
// and the result would be wrong or crash. This is the end-to-end proof that the region is threaded.
func TestRegionPolymorphicRecursiveBuildThreadsCallerRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
struct Box:
    value: i64

def make(depth: i64) -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Box(1)
        inner: Box& = make(depth - 1)
        return new[auto] Box(inner.value + 1)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            b: Box& = make(100)
            if b.value != 101:
                panic("region-polymorphic recursive build produced wrong value")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "bt.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISA_KEEP_TEST_BINARY", "1")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
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

	cmd := exec.Command(exePath, "bt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("region-polymorphic packed tree run failed: %v\noutput:\n%s", err, string(out))
	}
}
