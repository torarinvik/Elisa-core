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

// docs/76 Phase 3 Slice 0+0b: the full zero-ceremony beginner surface — a PLAIN recursive `enum`
// (no `packed`, no `store`, no `in store:`), `common(...)` shared fields, BARE constructors (no
// `new[auto]`), and storeless `match` — is promoted to the region-backed machinery and runs.
// eval(make(10)) sums 1024 leaves.
func TestPlainRecursiveEnumPromotedRuns(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
enum Expr:
    common(span: int)
    Int(value: int)
    Add(left: Expr, right: Expr)

def make(depth: i64) -> Expr:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Expr.Int(span: 0, value: 1)
        return Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

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
                panic("plain recursive enum produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.elisa")
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
		t.Fatalf("plain recursive enum run failed: %v\noutput:\n%s", err, string(out))
	}
}

// docs/76 §5 (Phase 5): first-class column scan. A `layout soa` (columnar) recursive enum is
// region-backed with per-field column arrays; `for s in Expr of .span` streams the dense `span`
// common-field column across every node in the implicit store. Builds 3 nodes (spans 10, 20, 30)
// and sums the column → 60. A broken column scan would read the wrong column, miscount rows, or
// fail to recover the store.
func TestEnumColumnScanSumsCommonField(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
enum Expr layout soa:
    common(span: int)
    Int(value: int)
    Add(left: Expr, right: Expr)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            a: Expr = new[auto] Expr.Int(span: 10, value: 5)
            b: Expr = new[auto] Expr.Int(span: 20, value: 7)
            root: Expr = new[auto] Expr.Add(span: 30, left: a, right: b)
            total: mutable i64 = 0
            for s in Expr of .span:
                total <- total + s
            if total != 60:
                panic("column scan over .span produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "colscan.elisa")
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
		t.Fatalf("column scan run failed: %v\noutput:\n%s", err, string(out))
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

// docs/76 regression: a region-OWNING function (one with `in auto:` blocks, here several — a fresh
// tree per loop iteration, the binary-trees shape) must CREATE its region-backed store on demand per
// region, NOT receive a single threaded store. `run` calls `make`/`check` but owns its regions, so the
// transitive store-need injection must skip it (funcOwnsRegion); otherwise all per-iteration trees
// collapse into one store and the build fails ("no active inferred region arena"). Sums to the
// binary-trees(10) checksum.
func TestRegionOwningFunctionCreatesPerRegionStores(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
enum Tree:
    Leaf(unused: i64)
    Node(left: Tree, right: Tree)

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(unused: 0)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def check(node: Tree) -> i64:
    match node:
        Tree.Leaf(unused: u):
            return 1
        Tree.Node(left: l, right: r):
            return 1 + check(l) + check(r)

def run(reps: i64) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        total: mutable i64 = 0
        i: mutable i64 = 0
        while i < reps:
            in auto:
                total <- total + check(make(8))
            i <- i + 1
        return total

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if run(50) != 50 * 511:
            panic("region-owning per-region store build produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "perregion.elisa")
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
		t.Fatalf("region-owning per-region store run failed: %v\noutput:\n%s", err, string(out))
	}
}

// docs/76: MUTUALLY-recursive plain enums (Tree↔Forest). Tree is recursive only THROUGH Forest, so
// the transitive recursion detection promotes the whole cycle; and the transitive store-need fixpoint
// threads BOTH per-enum stores across the call graph (forest() builds via leaf(), sumT()<->sumF()
// consume across the cycle) — neither enum is in every function's signature. branch(4) builds a
// Tree.Branch over a 4-element Forest of leaves; sumT recursively sums across both enums → 10. A
// broken cross-enum thread would fail to compile ("no active inferred region arena") or read garbage.
func TestMutuallyRecursivePlainEnumsRun(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join("..", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
enum Tree:
    Leaf(v: i64)
    Branch(f: Forest)

enum Forest:
    Empty(u: i64)
    More(hd: Tree, tl: Forest)

def leaf(n: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        return Tree.Leaf(v: n)

def forest(n: i64) -> Forest:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if n <= 0:
            return Forest.Empty(u: 0)
        return Forest.More(hd: leaf(n), tl: forest(n - 1))

def branch(n: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        return Tree.Branch(f: forest(n))

def sumT(t: Tree) -> i64:
    match t:
        Tree.Leaf(v: v):
            return v
        Tree.Branch(f: f):
            return sumF(f)

def sumF(f: Forest) -> i64:
    match f:
        Forest.Empty(u: u):
            return 0
        Forest.More(hd: h, tl: rest):
            return sumT(h) + sumF(rest)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            if sumT(branch(4)) != 10:
                panic("mutually-recursive plain enums produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "mutual.elisa")
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
		t.Fatalf("mutually-recursive plain enum run failed: %v\noutput:\n%s", err, string(out))
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

// Regression: a recursive plain enum in a program that does NOT include the std runtime must
// still run. The backend declares the packed-store helpers (ctx_aos_store_new/alloc/record,
// ctx_packed_store_*) as externs; they resolve against the default runtime object, which keeps
// only whitelisted symbols external under -O3 private linkage. Before the whitelist covered the
// packed-store families, those externs null-bound under -Wl,-undefined,dynamic_lookup and the
// first constructor call segfaulted at runtime (the "self-referential enum Tree segfault").
func TestPlainRecursiveEnumWithoutStdIncludeRuns(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	src := `enum Tree:
    Leaf(value: i64)
    Node(left: Tree, right: Tree)

def make(depth: i64) -> Tree:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return Tree.Leaf(value: 1)
        return Tree.Node(left: make(depth - 1), right: make(depth - 1))

def eval(t: Tree) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        match t:
            Tree.Leaf(value: v):
                return v
            Tree.Node(left: l, right: r):
                return eval(l) + eval(r)

@test
def bt() -> void:
    can Abort.Panic, Memory.Allocate, Memory.Release:
        region scope(1048576):
            t: Tree = make(6)
            if eval(t) != 64:
                panic("recursive plain enum without std include produced wrong sum")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "plain_noinclude.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("ELISACORE_TEST_CACHE", "0")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("no-include recursive enum test failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] bt") {
		t.Fatalf("expected test to pass, got:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String(), "bind to NULL") {
		t.Fatalf("link produced null-bind warnings:\n%s", stderr.String())
	}
}
