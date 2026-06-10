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
        in auto:
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
        in auto:
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
        in auto:
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
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
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
        in auto:
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
        in auto:
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
        in auto:
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
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
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
        region scratch(65536u)
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

// expectEnumProgramError builds a fixture and asserts compilation fails mentioning `want`.
func expectEnumProgramError(t *testing.T, fixture string, src string, want string) {
	t.Helper()
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
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected compile error containing %q, but build succeeded", want)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, want) {
		t.Fatalf("expected compile error containing %q, got:\n%s", want, combined)
	}
}

// docs/82: ptr handles require stable addresses — SoA columns relocate, so the combination is
// rejected at declaration.
func TestPtrHandleRejectsSoA(t *testing.T) {
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
	expectEnumProgramError(t, "ptr_freeze.elisa", `
enum Tree layout(handle: ptr):
    Node(left: Tree, right: Tree)
    Leaf(value: i64)

def main() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(65536u)
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
