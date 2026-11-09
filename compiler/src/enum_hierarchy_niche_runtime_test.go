package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
        region scratch(1048576):
            last: mutable Tree = new[auto] Tree.Leaf(value: 0)
            for i in 0..<100:
                last <- new[auto] Tree.Leaf(value: i)
            out: mutable i64 = 0
            match last:
                Tree.Leaf(value: v):
                    out <- v
                _:
                    pass
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
enum Tree layout(soa, handle: ptr):
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
`, "requires a plain store-backed enum")
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
