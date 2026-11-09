package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
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
// region (`bt`'s inferred region) through every recursive call, so all 101 nodes land in ONE region
// and each survives to be read by the next level. depth-100 adds 1 per level → value 101. If
// threading were broken (a per-call arena freed on return), `inner.value` would read freed memory
// and the result would be wrong or crash. This is the end-to-end proof that the region is threaded.
func TestRegionPolymorphicRecursiveBuildThreadsCallerRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
struct Box:
    value: i64

def make(depth: i64) -> Box&:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if depth <= 0:
            return new[auto] Box{value: 1}
        inner: Box& = make(depth - 1)
        return new[auto] Box{value: inner.value + 1}

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
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

// An explicit `new[r]` in a region-polymorphic helper must use the hidden arena
// argument associated with r. Unlike `new[auto]`, this is an explicit lifetime
// choice and must remain valid after the helper returns, until the caller ends
// the region.
func TestRegionParamExplicitNewUsesCallerArena(t *testing.T) {
	src := `
struct Box:
    value: i64

def make_box[@r](seed: i64& @r) -> Box& @r:
    return new[r] Box{value: seed}

@test
def explicit_region_new_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region scratch(4096):
            seed: i64& @scratch = new[scratch] 37
            box: Box& @scratch = make_box(seed)
            if box.value != 37:
                panic("new[r] did not allocate into the caller's region")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "explicit_region_new.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	objectPath := filepath.Join(dir, "explicit_region_new.o")
	if code := runCLI([]string{"-emit", "obj", "-o", objectPath, path}, &stdout, &stderr); code != 0 {
		t.Fatalf("build failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if info, err := os.Stat(objectPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty object after lowering region-parameter new[r], stat=%v", err)
	}
}

// A region-parameterized caller that invokes an inferred-region builder gets a
// synthetic caller-region argument. That argument must lower to the caller's
// hidden region slot even though it is not a source-written local variable.
func TestRegionParamThreadsSyntheticCallerRegion(t *testing.T) {
	src := `module Json:
    public:
        struct Box:
            value: i64

        def make_box(value: i64) -> Box& can[Memory.Allocate, Abort.Panic]:
            return new[auto] Box{value: value}

        def append_box[T, @r](values: mutable darray[i64]& @r, marker: T) -> void can[Memory.Allocate, Abort.Panic]:
            box: Box& = make_box(42)
            values.push(box.value)

        def forward_box[T, @candidate_arena](values: mutable darray[i64]& @candidate_arena, marker: T) -> void can[Memory.Allocate, Abort.Panic]:
            append_box(values, marker)

using Json

def main() -> i64 can[Memory.Allocate, Abort.Panic]:
    region scratch(4096):
        values: mutable darray[i64] @scratch = []
        Json::forward_box(values, true)
        return values[0]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "synthetic_region_argument.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	objectPath := filepath.Join(dir, "synthetic_region_argument.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objectPath, path}, &stdout, &stderr); code != 0 {
		t.Fatalf("LLVM lowering failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if info, err := os.Stat(objectPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty object after lowering synthetic caller region, stat=%v", err)
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

// A generic function dispatching to a region-polymorphic impl method through a protocol
// bound (`B.make_leaf(...)` with `[B: NodeBuilder]`) must classify region-polymorphic
// itself and receive the impl's packed-store needs (union over impls), so the root
// caller creates and threads the store exactly like a direct call chain.
func TestGenericProtocolDispatchThreadsRegionAndPackedStore(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil || func() bool { _, e := os.Stat(std); return e != nil }() {
		t.Skip("std runtime not found")
	}
	src := "include \"" + std + "\"\n" + `
enum E:
    Leaf(value: i64)
    Pair(left: E, right: E)

struct BuilderA:
    tag: mutable i8

protocol NodeBuilder:
    type Node
    def make_leaf(value: i64) -> Node
    def combine(left: Node, right: Node) -> Node

impl NodeBuilder for BuilderA:
    type Node = E

    def make_leaf(value: i64) -> E:
        return E.Leaf(value: value)

    def combine(left: E, right: E) -> E:
        return E.Pair(left: left, right: right)

def build_core[B: NodeBuilder]() -> B.Node:
    can Abort.Panic, Memory.Allocate:
        a: B.Node = B.make_leaf(41)
        b: B.Node = B.make_leaf(2)
        return B.combine(a, b)

@test
def gp() -> void:
    can Abort.Panic, Memory.Allocate:
        e: E = build_core[BuilderA]()
        if e is E.Pair(l, _):
            if l is E.Leaf(v):
                if v != 41:
                    panic("wrong leaf value through generic protocol dispatch")
                return
        panic("generic protocol dispatch did not build the expected tree")
`
	dir := t.TempDir()
	path := filepath.Join(dir, "gp.elisa")
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

	cmd := exec.Command(exePath, "gp")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generic protocol-dispatch run failed: %v\noutput:\n%s", err, string(out))
	}
}
