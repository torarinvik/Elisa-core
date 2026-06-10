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

// docs/77 Phase 3: CROSS-FUNCTION recursive hierarchy — a builder (make, returns Node) and a consumer
// (eval, takes Node) are separate functions, so the per-root store must be THREADED between them.
func TestRecursiveEnumHierarchyCrossFunction(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier_xfn.elisa", `
enum Node: pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)

def make(depth: i64) -> Node:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Expr.Lit(value: 1)
        return Expr.Add(left: make(depth - 1), right: make(depth - 1))

def eval(n: Node) -> i64:
    match n:
        Expr.Add(left: l, right: r):
            return eval(l) + eval(r)
        Expr.Lit(value: v):
            return v

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            root: Node = make(10)
            if eval(root) != 1024:
                panic("cross-function recursive hierarchy produced wrong sum")
`)
}

// docs/77: common(...) fields on the hierarchy root are shared by every node, readable from any
// refinement (the canonical AST `span` pattern). Build two nodes with spans, sum via match.
func TestRecursiveEnumHierarchyCommonField(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier_common.elisa", `
enum Node:
    common(span: int)
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: Node = new[auto] Expr.Lit(span: 10, value: 5)
            b: Node = new[auto] Expr.Lit(span: 20, value: 7)
            root: Node = new[auto] Expr.Add(span: 30, left: a, right: b)
            if a.span != 10:
                panic("common field read wrong: a.span should be 10")
            if root.span != 30:
                panic("common field read wrong: root.span should be 30")
            total: mutable i64 = 0
            match root:
                Expr.Add(left: l, right: r):
                    total <- total + root.span
                Expr.Lit(value: v):
                    total <- total + root.span + v
            if total != 30:
                panic("common field read in match arm wrong")
`)
}

// docs/77 §2 + docs/81 Phase 3e: a bare-category `is` test in expression position is a single
// unsigned tag-range check (`tag - lo <u count`). Value hierarchy: Mono owns 2 tags, RGB owns 3.
func TestValueEnumHierarchyCategoryIsTest(t *testing.T) {
	runEnumHierarchyProgram(t, "cat_is.elisa", `
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
    if c is Mono:
        panic("RGB.Green must not test as Mono")
    if not (c is RGB):
        panic("RGB.Green must test as RGB")
    if not (c is Color):
        panic("widening test must be true")
    m: Color = Mono.White
    if not (m is Mono):
        panic("Mono.White must test as Mono")
    if m is RGB:
        panic("Mono.White must not test as RGB")
`)
}

// Category `is` on a REGION-BACKED recursive hierarchy: the tag is read from the root store's
// record (one load), then the same range check.
func TestRecursiveEnumHierarchyCategoryIsTest(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_cat_is.elisa", `
enum Node: pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)
enum Stmt is Node:
    Ret(value: Node)
    Nop

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            lit: Node = new[auto] Expr.Lit(value: 5)
            ret: Node = new[auto] Stmt.Ret(value: lit)
            if not (lit is Expr):
                panic("Lit must test as Expr")
            if lit is Stmt:
                panic("Lit must not test as Stmt")
            if not (ret is Stmt):
                panic("Ret must test as Stmt")
            if ret is Expr:
                panic("Ret must not test as Expr")
            if not (ret is Node):
                panic("widening test must be true")
`)
}

// docs/77 §2 category arms: `Mono:` matches the whole sub-category (range dispatch), and the
// match is EXHAUSTIVE through category arms alone (no wildcard) — each child category
// discharges its leaf range.
func TestValueEnumHierarchyCategoryMatchArms(t *testing.T) {
	runEnumHierarchyProgram(t, "cat_arms.elisa", `
enum Color: pass
enum Mono is Color:
    Black
    White
enum RGB is Color:
    Red
    Green
    Blue

def classify(c: Color) -> i64:
    match c:
        Mono:
            return 1
        RGB:
            return 2

@test
def bt() -> void:
    can Abort.Panic:
        if classify(Mono.Black) != 1:
            panic("Mono.Black must take the Mono category arm")
        if classify(Mono.White) != 1:
            panic("Mono.White must take the Mono category arm")
        if classify(RGB.Red) != 2:
            panic("RGB.Red must take the RGB category arm")
        if classify(RGB.Blue) != 2:
            panic("RGB.Blue must take the RGB category arm")
`)
}

// Category arm WITH binder (`Expr e:`) on a region-backed recursive hierarchy: binds the
// scrutinee at the narrowed type so it can be passed where the sub-category is required.
func TestRecursiveEnumHierarchyCategoryArmBinder(t *testing.T) {
	runEnumHierarchyProgram(t, "cat_arm_bind.elisa", `
enum Node: pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)
enum Stmt is Node:
    Ret(value: Node)
    Nop

def eval_expr(e: Expr) -> i64:
    match e:
        Expr.Add(left: l, right: r):
            return describe(l) + describe(r)
        Expr.Lit(value: v):
            return v

def describe(n: Node) -> i64:
    match n:
        Expr e:
            return eval_expr(e)
        Stmt:
            return 100

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: Node = new[auto] Expr.Lit(value: 5)
            b: Node = new[auto] Expr.Lit(value: 7)
            root: Node = new[auto] Expr.Add(left: a, right: b)
            nop: Node = new[auto] Stmt.Nop
            if describe(root) != 12:
                panic("Expr category arm with binder must narrow and eval to 12")
            if describe(nop) != 100:
                panic("Stmt category arm must match Nop")
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
