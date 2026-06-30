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
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
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
// depth-1 tree (allocations infer their region) and fold it with nested storeless matches. 5 + 7 = 12.
func TestRecursiveEnumHierarchySingleFunction(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier.elisa", `
enum Node: pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
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
        root: Node = make(10)
        if eval(root) != 1024:
            panic("cross-function recursive hierarchy produced wrong sum")
`)
}

// docs/76 side-tables: a variant whose ONLY self-reference is through a container (`body: darray[Node]`)
// is still a recursive tree (the AST block/args case) and must promote to the AoS handle store — not
// silently stay an inline value enum. Before the computeRecursiveEnumSet container-descent fix the
// hierarchy was not packed, so a builder returning such a node left its child container's backing in a
// build-local region that freed on return → use-after-free on traversal. Build cross-fn + return + fold.
func TestRecursiveEnumHierarchyContainerChildCrossFunction(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier_container.elisa", `
enum Node: pass
enum Stmt is Node:
    Leaf(value: i64)
    Block(body: darray[Node], tag: i64)

def make_leaf(v: i64) -> Node:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        return new Stmt.Leaf(value: v)

def build() -> Node:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        kids: mutable darray[Node] = []
        kids.push(make_leaf(10))
        kids.push(make_leaf(20))
        kids.push(make_leaf(30))
        return new Stmt.Block(body: kids, tag: 99)

def sum_tree(n: Node) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        match n:
            Stmt.Leaf(value: v):
                return v
            Stmt.Block(body: b, tag: t):
                total: mutable i64 = t
                for k in b:
                    total <- total + sum_tree(k)
                return total

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Node = build()
        if sum_tree(root) != 159:
            panic("container-child cross-function fold produced wrong sum")
`)
}

// Same container-child shape but with a narrowed handle width (docs/82 dial): `new` must hand back a
// width-correct opaque handle (here u16), never a raw ref, even when the variant carries a container.
func TestRecursiveEnumHierarchyContainerChildNarrowHandle(t *testing.T) {
	runEnumHierarchyProgram(t, "rec_hier_container_u16.elisa", `
enum Node layout(handle: u16): pass
enum Stmt is Node:
    Leaf(value: i64)
    Block(body: darray[Node], tag: i64)

def build() -> Node:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        kids: mutable darray[Node] = []
        kids.push(new Stmt.Leaf(value: 10))
        kids.push(new Stmt.Leaf(value: 20))
        kids.push(new Stmt.Leaf(value: 30))
        return new Stmt.Block(body: kids, tag: 99)

def sum_tree(n: Node) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        match n:
            Stmt.Leaf(value: v):
                return v
            Stmt.Block(body: b, tag: t):
                total: mutable i64 = t
                for k in b:
                    total <- total + sum_tree(k)
                return total

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Node = build()
        if sum_tree(root) != 159:
            panic("u16 container-child fold produced wrong sum")
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

// docs/77 §2 category arm with binder, where the hierarchy is declared inside a MODULE so the
// sub-category's type is namespace-qualified (`Ast.Expr`) while the arm names it unqualified
// (`Expr e:`). Regression: the backend's enumCategoryArm did a raw NamedTypes["Expr"] lookup that
// missed the qualified key, so the binder was never emitted -> "unknown identifier e during LLVM
// lowering". It now resolves the arm name against the scrutinee's hierarchy by simple name.
func TestEnumHierarchyCategoryArmBinderInModule(t *testing.T) {
	runEnumHierarchyProgram(t, "cat_arm_bind_module.elisa", `
module Ast:
    public:
        enum Node: pass
        enum Expr is Node:
            Add(left: Node, right: Node)
            Lit(value: i64)
        enum Stmt is Node:
            Ret(value: Node)
            Nop

using Ast

def eval_expr(e: Ast::Expr) -> i64:
    match e:
        Expr.Add(left: l, right: r):
            return describe(l) + describe(r)
        Expr.Lit(value: v):
            return v
        _:
            return 0

def describe(n: Ast::Node) -> i64:
    match n:
        Expr e:
            return eval_expr(e)
        Stmt:
            return 100
        _:
            return 0

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Ast::Node = new[auto] Expr.Lit(value: 5)
        b: Ast::Node = new[auto] Expr.Lit(value: 7)
        root: Ast::Node = new[auto] Expr.Add(left: a, right: b)
        nop: Ast::Node = new[auto] Stmt.Nop
        if describe(root) != 12:
            panic("module-qualified Expr category arm with binder must narrow and eval to 12")
        if describe(nop) != 100:
            panic("module-qualified Stmt category arm must match Nop")
`)
}

// docs/77 §2 binding form: `if e is Stmt s:` — the docs/81 range test plus a branch-scoped
// binder at the NARROWED category type (sugar for `is Stmt as s`). The binder must be usable
// where the sub-category is required.
func TestRecursiveEnumHierarchyIsBindingForm(t *testing.T) {
	runEnumHierarchyProgram(t, "cat_is_bind.elisa", `
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
            return 0
        Expr.Lit(value: v):
            return v

def describe(n: Node) -> i64:
    if n is Expr e:
        return eval_expr(e)
    if n is Stmt as st:
        return 100
    return -1

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        lit: Node = new[auto] Expr.Lit(value: 42)
        nop: Node = new[auto] Stmt.Nop
        if describe(lit) != 42:
            panic("is-binding form must narrow Expr and eval to 42")
        if describe(nop) != 100:
            panic("is-binding (as-alias) form must match Stmt")
`)
}

// docs/82: `layout(handle: u16)` narrows the opaque index handle to 2 bytes per edge. The
// recursive enum must build and traverse correctly at the narrow width (handle values stay
// below the u16 null sentinel for this tree size).
func TestRecursiveEnumNarrowHandleU16(t *testing.T) {
	runEnumHierarchyProgram(t, "handle_u16.elisa", `
enum Tree layout(handle: u16):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(value: 1)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def total(t: Tree) -> i64:
    match t:
        Tree.Node(left: l, right: r):
            return total(l) + total(r)
        Tree.Leaf(value: v):
            return v

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Tree = make(8)
        if total(root) != 256:
            panic("u16-handle tree built or traversed wrong")
`)
}

// docs/82: the narrow handle composes with hierarchies — the width is a ROOT-level fact shared
// by every sub-category (one store, one shape), and category range tests work unchanged.
func TestHierarchyNarrowHandleU16(t *testing.T) {
	runEnumHierarchyProgram(t, "hier_handle_u16.elisa", `
enum Node layout(handle: u16): pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)
enum Stmt is Node:
    Nop

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Node = new[auto] Expr.Lit(value: 5)
        b: Node = new[auto] Expr.Lit(value: 7)
        root: Node = new[auto] Expr.Add(left: a, right: b)
        if not (root is Expr):
            panic("category test must hold at u16 width")
        sum: mutable i64 = 0
        match root:
            Expr.Add(left: l, right: r):
                match l:
                    Expr.Lit(value: lv):
                        sum <- sum + lv
                    _:
                        pass
                match r:
                    Expr.Lit(value: rv):
                        sum <- sum + rv
                    _:
                        pass
            _:
                pass
        if sum != 12:
            panic("u16-handle hierarchy fold produced wrong sum")
`)
}

// docs/82: overflowing a narrowed handle is a LOUD trap at the allocation site, never a silent
// wrap — a u8 store caps at 255 nodes (index 255 is the null sentinel), so allocating 300 leaves
// must abort the program.
func TestNarrowHandleOverflowTraps(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := `
enum Chain layout(handle: u8):
    Link(next: Chain)
    End

def chain(depth: i64) -> Chain:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Chain.End
        return Chain.Link(next: chain(depth - 1))

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        c: Chain = chain(300)
`
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, "handle_overflow.elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// `-emit test` builds AND runs the @test functions; the overflow trap is the expected
	// outcome, so the run must FAIL with a signal-trap panic (never exit cleanly = silent wrap).
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("u8-handle overflow must trap, but the test run exited cleanly:\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "PANIC") || !strings.Contains(stdout.String(), "trap") {
		t.Fatalf("expected a signal-trap panic from the handle-overflow guard, got (exit %d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

// docs/82 step 3: `layout(handle: ptr)` — the handle IS the record address (carried as a
// uintptr-width integer). Reads need no store-state call; everything else is unchanged.
func TestRecursiveEnumPtrHandle(t *testing.T) {
	runEnumHierarchyProgram(t, "handle_ptr.elisa", `
enum Tree layout(handle: ptr):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(value: 1)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def total(t: Tree) -> i64:
    match t:
        Tree.Node(left: l, right: r):
            return total(l) + total(r)
        Tree.Leaf(value: v):
            return v

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Tree = make(8)
        if total(root) != 256:
            panic("ptr-handle tree built or traversed wrong")
`)
}

// docs/82: the ptr handle composes with hierarchies — root-level fact, category range tests
// unchanged (tag read is a direct load at the record address).
func TestHierarchyPtrHandle(t *testing.T) {
	runEnumHierarchyProgram(t, "hier_handle_ptr.elisa", `
enum Node layout(handle: ptr): pass
enum Expr is Node:
    Add(left: Node, right: Node)
    Lit(value: i64)
enum Stmt is Node:
    Nop

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Node = new[auto] Expr.Lit(value: 5)
        b: Node = new[auto] Expr.Lit(value: 7)
        root: Node = new[auto] Expr.Add(left: a, right: b)
        if not (root is Expr):
            panic("category test must hold with ptr handles")
        if root is Stmt:
            panic("negative category test must fail with ptr handles")
        sum: mutable i64 = 0
        if root is Expr e:
            match e:
                Expr.Add(left: l, right: r):
                    match l:
                        Expr.Lit(value: lv):
                            sum <- sum + lv
                        _:
                            pass
                    match r:
                        Expr.Lit(value: rv):
                            sum <- sum + rv
                        _:
                            pass
                _:
                    pass
        if sum != 12:
            panic("ptr-handle hierarchy fold produced wrong sum")
`)
}

// docs/82 step 3 tail: a function that only READS a ptr-handle enum carries no implicit store
// parameter (the handle is the record address — reads are self-contained), while a function that
// CONSTRUCTS still threads the region + store. This is the ABI simplification the ptr dial buys.
func TestPtrHandleReaderHasNoImplicitStoreParam(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := `
enum Tree layout(handle: ptr):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def total(t: Tree) -> i64:
    match t:
        Tree.Node(left: l, right: r):
            return total(l) + total(r)
        Tree.Leaf(value: v):
            return v

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(value: 1)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(65536)
        root: Tree = make(3)
        out: i64 = total(root) - 8
        destroy scratch
        return out
`
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, "ptr_abi.elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstderr:\n%s", code, stderr.String())
	}
	ir := stdout.String()
	totalDefine, makeDefine := "", ""
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "define") && strings.Contains(line, "@total(") {
			totalDefine = line
		}
		if strings.HasPrefix(line, "define") && strings.Contains(line, "@make(") {
			makeDefine = line
		}
	}
	if totalDefine == "" || makeDefine == "" {
		t.Fatalf("could not locate @total/@make defines in IR")
	}
	if strings.Contains(totalDefine, "Tree__Store") {
		t.Fatalf("ptr-handle reader must carry no implicit store param, got: %s", totalDefine)
	}
	if !strings.Contains(makeDefine, "Tree__Store") {
		t.Fatalf("ptr-handle builder must still thread the store, got: %s", makeDefine)
	}
}

// docs/77 Phase 4: hierarchy column scan. The SOA hierarchy shares ONE root store, so a root
// scan streams every node's common column, and a SUB-CATEGORY scan range-filters rows by tag
// (`tag - LeafTagLo <u LeafTagCount`) before reading the column. Expr nodes carry spans 1,2,4
// and the Stmt node 8: root sum = 15, Expr-only sum = 7, Stmt-only sum = 8.
func TestHierarchyColumnScanRangeFilters(t *testing.T) {
	runEnumHierarchyProgram(t, "hier_colscan.elisa", `
enum Node layout soa:
    common(span: i64)
enum Expr is Node:
    Lit(value: i64)
    Add(left: Node, right: Node)
enum Stmt is Node:
    Print(target: Node)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Node = new[auto] Expr.Lit(span: 1, value: 5)
        b: Node = new[auto] Expr.Lit(span: 2, value: 7)
        c: Node = new[auto] Expr.Add(span: 4, left: a, right: b)
        d: Node = new[auto] Stmt.Print(span: 8, target: c)
        all_total: mutable i64 = 0
        for s in Node of .span:
            all_total <- all_total + s
        if all_total != 15:
            panic("root column scan produced wrong sum")
        expr_total: mutable i64 = 0
        for s in Expr of .span:
            expr_total <- expr_total + s
        if expr_total != 7:
            panic("category column scan must range-filter to Expr rows")
        stmt_total: mutable i64 = 0
        for s in Stmt of .span:
            stmt_total <- stmt_total + s
        if stmt_total != 8:
            panic("category column scan must range-filter to Stmt rows")
`)
}

// docs/77 Phase 5: a NON-recursive hierarchy is a VALUE hierarchy — an inline {tag, payload}
// value with no store, no region threading, and no allocation. This pins the property in IR:
// the consumer takes the enum BY VALUE (no store param) and the program calls no arena helper.
func TestValueHierarchyIsStoreFreeInIR(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := `
enum Shape: pass
enum Round is Shape:
    Circle(r: i64)
    Ellipse(a: i64, b: i64)
enum Poly is Shape:
    Square(side: i64)

def area2(s: Shape) -> i64:
    match s:
        Round.Circle(r: r):
            return 3 * r * r
        Round.Ellipse(a: a, b: b):
            return 3 * a * b
        Poly.Square(side: d):
            return d * d

def main() -> i64:
    c: Shape = Round.Circle(r: 2)
    q: Shape = Poly.Square(side: 3)
    return area2(c) + area2(q) - 21
`
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, "value_hier_ir.elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstderr:\n%s", code, stderr.String())
	}
	ir := stdout.String()
	area2Define := ""
	for _, line := range strings.Split(ir, "\n") {
		if strings.HasPrefix(line, "define") && strings.Contains(line, "@area2(") {
			area2Define = line
		}
		if strings.HasPrefix(line, "define") && strings.Contains(line, "@main(") {
			break
		}
	}
	if area2Define == "" {
		t.Fatal("could not locate @area2 define in IR")
	}
	if strings.Contains(area2Define, "Store") || strings.Contains(area2Define, "ptr") {
		t.Fatalf("value-hierarchy consumer must take the enum by value with no store/region params, got: %s", area2Define)
	}
	for _, fn := range []string{"@area2(", "@main("} {
		a := strings.Index(ir, "define i64 "+fn)
		if a < 0 {
			t.Fatalf("missing define for %s", fn)
		}
		body := ir[a:]
		if end := strings.Index(body, "\n}"); end > 0 {
			body = body[:end]
		}
		if strings.Contains(body, "arena_alloc") || strings.Contains(body, "ctx_aos_store") || strings.Contains(body, "ctx_packed_store") {
			t.Fatalf("value hierarchy must not touch any store/arena helper in %s, got:\n%s", fn, body)
		}
	}
}

// docs/76 free-null niche (docs/82 polish): an optional enum child `Tree?` in an AoS record is
// stored as the BARE handle with the null sentinel meaning absent — no presence flag in the
// record. The generic carrier remains the ABI outside the record; conversion happens at the
// record boundary. This runs a tree with one present and one absent optional child.
func TestOptionalEnumChildNicheRuntime(t *testing.T) {
	runEnumHierarchyProgram(t, "optional_niche.elisa", `
enum Tree:
    Node(left: Tree, right: Tree?)
    Leaf(value: i64)

def total(t: Tree) -> i64:
    match t:
        Tree.Node(left: l, right: r):
            sum: mutable i64 = total(l)
            if r is rt:
                sum <- sum + total(rt)
            return sum
        Tree.Leaf(value: v):
            return v

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Tree = new[auto] Tree.Leaf(value: 3)
        b: Tree = new[auto] Tree.Leaf(value: 7)
        both: Tree = new[auto] Tree.Node(left: a, right: b)
        lone: Tree = new[auto] Tree.Node(left: both, right: null)
        if total(lone) != 10:
            panic("optional-niche tree summed wrong")
`)
}

// IR pin for the niche: the Node payload is two bare handles ({ i32, i32 }, no i1 presence
// flag) and the absent marker is a select against the width's sentinel (-1 at u32).
func TestOptionalEnumChildNicheIR(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := `
enum Tree:
    Node(left: Tree, right: Tree?)
    Leaf(value: i64)

def probe(n: Tree) -> i64:
    match n:
        Tree.Node(left: l, right: r):
            if r is rr:
                return 1
            return 0
        _:
            return 2

def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: Tree = new[auto] Tree.Leaf(value: 1)
        n: Tree = new[auto] Tree.Node(left: a, right: null)
        return probe(n)
`
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, "niche_ir.elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstderr:\n%s", code, stderr.String())
	}
	ir := stdout.String()
	// The absent write folds to the bare -1 sentinel constant (a present child would emit the
	// niche.pack select); the read side must rebuild the carrier by comparing against it.
	if !strings.Contains(ir, "niche.unpack.present = icmp ne i32") || !strings.Contains(ir, ", -1") {
		t.Fatalf("expected the payload read to unpack the optional child against the -1 sentinel, IR:\n%s", ir[:min(len(ir), 4000)])
	}
	if strings.Contains(ir, "insertvalue { i32, %Optional__Tree }") || strings.Contains(ir, "{ i32, { i1, i32 } }") {
		t.Fatal("optional child must occupy a bare-handle record slot, not the generic carrier")
	}
}

// docs/82 tight-payload pin: the handle dial changes the RECORD size, not just the value ABI.
// With handle-width payload slots (the AoS slot-narrowing), Tree{Node(l,r),Leaf(i64)} rows are 12
// bytes at handle u16/u32 — tag(4) + the i64 leaf packed into handle-width slots with no 8-byte
// payload-array alignment pad — and 24 at u64, where 8-byte slots fill the machine word so the union
// is two 8-byte edges and there is no padding to reclaim.
func TestHandleDialShrinksRecordRows(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	rowBytes := func(width string) string {
		t.Helper()
		src := `
enum Tree layout(handle: ` + width + `):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(value: 1)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def total(t: Tree) -> i64:
    match t:
        Tree.Node(left: l, right: r):
            return total(l) + total(r)
        Tree.Leaf(value: v):
            return v

def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        root: Tree = make(2)
        return total(root) - 4
`
		full := "include \"" + std + "\"\n" + src
		dir := t.TempDir()
		path := filepath.Join(dir, "row_"+width+".elisa")
		if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
			t.Fatalf("build failed at %s (exit %d)\nstderr:\n%s", width, code, stderr.String())
		}
		for _, line := range strings.Split(stdout.String(), "\n") {
			if idx := strings.Index(line, "row_bytes = insertvalue %Tree__Store "); idx >= 0 {
				parts := strings.Split(line, ", ")
				if len(parts) >= 2 {
					return strings.TrimSpace(strings.TrimPrefix(parts[len(parts)-2], "i64 "))
				}
			}
		}
		t.Fatalf("could not find row_bytes for %s", width)
		return ""
	}
	if got := rowBytes("u16"); got != "12" {
		t.Fatalf("u16-handle rows must be 12 bytes (handle-width slots, no payload-array pad), got %s", got)
	}
	if got := rowBytes("u64"); got != "24" {
		t.Fatalf("u64-handle rows must be 24 bytes (8-byte slots fill the word, no pad to reclaim), got %s", got)
	}
}

// expectEnumProgramError builds a fixture and asserts compilation fails mentioning `want`.
func expectEnumProgramError(t *testing.T, fixture string, src string, want string) {
	t.Helper()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, fixture)
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected compile error containing %q, but build succeeded", want)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, want) {
		t.Fatalf("expected compile error containing %q, got:\n%s", want, combined)
	}
}

// The legacy `(index: uN)` enum-layout spelling is hard-removed: a parse error pointing at the
// canonical `(handle: uN)` form (docs/82).
func TestLegacyIndexSpellingRejected(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := `
enum Tree layout(index: u16):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def main() -> i64:
    return 0
`
	full := "include \"" + std + "\"\n" + src
	dir := t.TempDir()
	path := filepath.Join(dir, "index_legacy.elisa")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("legacy `(index: uN)` spelling must be rejected, but compiled cleanly")
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "has been removed") || !strings.Contains(combined, "handle:") {
		t.Fatalf("expected a removal error pointing at the `handle:` spelling, got:\n%s", combined)
	}
}

// docs/77: common(...) is a ROOT-level fact; a sub-category declaring its own is rejected at the
// declaration (previously it passed analysis and failed deep in codegen offset lookup).
func TestSubCategoryCommonFieldsRejected(t *testing.T) {
	t.Parallel()
	expectEnumProgramError(t, "subcat_common.elisa", `
enum Node:
    common(span: i64)
enum Expr is Node:
    common(prec: i64)
    Lit(value: i64)
    Add(left: Node, right: Node)

def main() -> i64:
    return 0
`, "must be declared on the hierarchy root")
}

// docs/76 Phase 6 tail / docs/82 polish: the narrow-handle width lint. A constant-bounded
// constructor loop in a region-owning function proves the store's population locally, so a
// default-width root draws a -Wperf suggestion to narrow the handle; an already-narrowed
// enum (or an unbounded builder via calls) stays silent.
func TestNarrowHandleWidthLint(t *testing.T) {
	t.Parallel()
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	build := func(name, src string) string {
		t.Helper()
		full := "include \"" + std + "\"\n" + src
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
			t.Fatalf("build failed (exit %d)\nstderr:\n%s", code, stderr.String())
		}
		return stderr.String()
	}
	body := `
def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region scratch(1048576)
        last: mutable Tree = new[auto] Tree.Leaf(value: 0)
        for i in 0..<100:
            last <- new[auto] Tree.Leaf(value: i)
        out: mutable i64 = 0
        match last:
            Tree.Leaf(value: v):
                out <- v
            _:
                pass
        destroy scratch
        return out - 99
`
	wide := build("width_lint_wide.elisa", `
enum Tree:
    Node(left: Tree, right: Tree)
    Leaf(value: i64)
`+body)
	if !strings.Contains(wide, "layout(handle: u8)") {
		t.Fatalf("expected the width lint to suggest a narrow handle for a 100-node bounded loop, got:\n%s", wide)
	}
	narrow := build("width_lint_narrow.elisa", `
enum Tree layout(handle: u8):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)
`+body)
	if strings.Contains(narrow, "layout(handle:") {
		t.Fatalf("already-narrowed enum must not draw the width lint, got:\n%s", narrow)
	}
}

// docs/82: ptr handles require stable addresses — SoA columns relocate, so the combination is
// rejected at declaration.
func TestPtrHandleRejectsSoA(t *testing.T) {
	t.Parallel()
	expectEnumProgramError(t, "ptr_soa.elisa", `
enum Tree layout soa(handle: ptr):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def main() -> i64:
    return 0
`, "requires stable record addresses")
}

// docs/82: a non-recursive (inline value) enum has no store record to point at.
func TestPtrHandleRejectsValueEnum(t *testing.T) {
	t.Parallel()
	expectEnumProgramError(t, "ptr_value.elisa", `
enum Color layout(handle: ptr):
    Red
    Green

def main() -> i64:
    return 0
`, "requires a recursive region-backed enum")
}

// docs/82: pointer handles are not position-independent → freeze is a compile error naming the fix.
func TestPtrHandleRejectsFreeze(t *testing.T) {
	t.Parallel()
	expectEnumProgramError(t, "ptr_freeze.elisa", `
enum Tree layout(handle: ptr):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(65536)
        store: Tree.Store[Local] = Tree.Store(r)
        t: Tree = new[store] Tree.Leaf(value: 1)
        frozen: Tree.Store[Frozen] = freeze(move store)
        destroy r
        return 0
`, "not position-independent")
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
