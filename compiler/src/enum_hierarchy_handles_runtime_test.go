package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
enum Node layout(soa):
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
