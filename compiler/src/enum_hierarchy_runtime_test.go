package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/77 Phase 3 (value hierarchies): a NON-recursive sealed enum hierarchy lowers to one unified
// inline representation per root, so a leaf value upcasts to the root for free and a match over the
// root dispatches on the unified tag across refinements. These tests build + run real programs.
func runEnumHierarchyProgram(t *testing.T, fixture string, src string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, fixture)
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
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
		t.Fatalf("enum hierarchy run failed: %v\noutput:\n%s", err, string(out))
	}
}

// Tag-only hierarchy: construct a leaf, upcast to the root, match it back across refinements.
func TestValueEnumHierarchyTagOnly(t *testing.T) {
	runEnumHierarchyProgram(t, "color.elisa", `
enum Color: pass
enum Mono is Color:
    Black
    White
enum RGB is Color:
    Red
    Green
    Blue

@test
def bt() -> void:
    c: Color = RGB.Green
    n: mutable i64 = 0
    match c:
        Mono.Black:
            n <- 1
        Mono.White:
            n <- 2
        RGB.Red:
            n <- 3
        RGB.Green:
            n <- 4
        RGB.Blue:
            n <- 5
    if n != 4:
        panic("tag-only hierarchy match dispatched wrong")
`)
}

// docs/77 Phase 3: a RECURSIVE hierarchy (Expr.Add references the root Node) is region-backed with one
// store per root, shared by all refinements. Single-function (no cross-function threading yet): build a
// depth-1 tree in `in auto:` and fold it with nested storeless matches. 5 + 7 = 12.
func TestRecursiveEnumHierarchySingleFunction(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier.elisa", `
enum Node: pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: Node = new[auto] Expr.Lit(value: 5)
            b: Node = new[auto] Expr.Lit(value: 7)
            root: Node = new[auto] Expr.Add(left: a, right: b)
            sum: mutable i64 = 0
            match root:
                Expr.Add(left: l, right: r):
                    match l:
                        Expr.Lit(value: lv):
                            sum <- sum + lv
                        Expr.Add(left: l2, right: r2):
                            sum <- sum + 0
                    match r:
                        Expr.Lit(value: rv):
                            sum <- sum + rv
                        Expr.Add(left: l3, right: r3):
                            sum <- sum + 0
                Expr.Lit(value: v):
                    sum <- v
            if sum != 12:
                panic("recursive hierarchy fold produced wrong sum")
`)
}

// Payload hierarchy: leaves carry data; the root's record is the union of all leaves' payloads.
func TestValueEnumHierarchyWithPayload(t *testing.T) {
	runEnumHierarchyProgram(t, "shape.elisa", `
enum Shape: pass
enum Round is Shape:
    Circle(radius: i64)
enum Angular is Shape:
    Square(side: i64)

@test
def bt() -> void:
    s: Shape = Round.Circle(radius: 7)
    out: mutable i64 = 0
    match s:
        Round.Circle(radius: r):
            out <- r
        Angular.Square(side: x):
            out <- x * 2
    if out != 7:
        panic("payload hierarchy match read wrong value")
`)
}
