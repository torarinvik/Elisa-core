package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"elisacore/src/backend"
)

// A recursive enum lives in a packed AoS store; every `match` on a node resolves its record with
// ctx_aos_store_record. With the std compiled into the module (as every program is), -O3 must
// inline it into a tree walk: when it was forced never-inline, each step paid up to three opaque,
// non-CSE-able calls (LangArena Compress::HuffDecode 68.5 -> 28.5 G instructions once inlined).
// The program must also still compute the right answer.
func TestRunCLIAoSStoreRecordInlinedIntoTreeWalk(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "aos_record_inline.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `enum Tree:
    Leaf(value: i32)
    Node(weight: i32, left: Tree, right: Tree)

@inline(never)
def walk(root: Tree, bits: u64, steps: i32) -> i64:
    total: mutable i64 = 0
    current: mutable Tree = root
    i: mutable i32 = 0
    while i < steps |i, total, current|:
        match current:
            Tree.Node(weight, left, right):
                current <- right if ((bits >> (i % 64).u64()) & 1) == 1 else left
            Tree.Leaf(value):
                total <- total + value.i64()
                current <- root
        i <- i + 1
    return total

def build_tree() -> Tree can[Memory.Allocate]:
    a: Tree = Tree.Leaf(value: 1)
    b: Tree = Tree.Leaf(value: 2)
    c: Tree = Tree.Leaf(value: 3)
    inner: Tree = Tree.Node(weight: 1, left: b, right: c)
    return Tree.Node(weight: 2, left: a, right: inner)

def main() -> int can[Memory.Allocate]:
    total: mutable i64 = 0
    region r(4096):
        t: Tree = build_tree()
        total <- walk(t, 12345, 1000)
    # Exit status carries the walk's sum; the test compares -O0 with -O3.
    return (total % 200).int()
`
	if err := os.WriteFile(fixturePath, []byte("include \""+std+"\"\n"+src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	irPath := filepath.Join(dir, "out.ll")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", "-O3", "-o", irPath, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit llvm failed:\n%s", stderr.String())
	}
	ir, err := os.ReadFile(irPath)
	if err != nil {
		t.Fatalf("read IR: %v", err)
	}
	walk := regexp.MustCompile(`(?s)define [^\n]*@walk\(.*?\n}\n`).Find(ir)
	if walk == nil {
		t.Fatalf("walk not found in IR")
	}
	if regexp.MustCompile(`call [^\n]*@ctx_aos_store_record\(`).Match(walk) {
		t.Fatalf("ctx_aos_store_record must be inlined into the tree walk at -O3:\n%s", walk)
	}
	statusAt := func(opt backend.OptimizationLevel) int {
		t.Helper()
		var diag bytes.Buffer
		expanded, err := readSourceWithIncludes(fixturePath, map[string]bool{})
		if err != nil {
			t.Fatalf("expand includes: %v", err)
		}
		_, result, ok := analyzeProgram(fixturePath, expanded, &diag)
		if !ok {
			t.Fatalf("analyze failed:\n%s", diag.String())
		}
		exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &diag)
		if err != nil {
			t.Fatalf("build (opt=%v): %v\n%s", opt, err, diag.String())
		}
		defer cleanup()
		runErr := exec.Command(exePath).Run()
		var exitErr *exec.ExitError
		if runErr == nil {
			return 0
		}
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("run (opt=%v): %v", opt, runErr)
		}
		return exitErr.ExitCode()
	}
	o0, o3 := statusAt(backend.OptimizationLevel0), statusAt(backend.OptimizationLevel3)
	if o0 != o3 || o0 == 0 {
		t.Fatalf("tree walk result differs or is empty: -O0 status %d, -O3 status %d", o0, o3)
	}
}
