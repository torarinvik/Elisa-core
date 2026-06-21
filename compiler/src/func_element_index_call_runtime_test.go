package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: calling a function stored in a container element. `fs[0](...)` parses as a
// generic specialize-call (`fs[T](...)`); the analyzer must recognize that `fs` is a value
// and route it back to index-then-call. Covers literal index, variable index, and a
// struct-field container.
func TestRunCLIIndexedFuncElementCall(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "func_element_index_call_fixture.elisa")
	src := `def one() -> i64:
    return 1

def two() -> i64:
    return 2

def applyAt(fs: darray[func() -> i64], i: usize) -> i64:
    return fs[i]()

struct Dispatch:
    handlers: darray[func() -> i64]

@test
def func_element_index_call_test() -> void:
    can Abort.Panic, Memory.Allocate:
        fs: darray[func() -> i64] = [one, two]
        if fs[0]() != 1:
            panic("fs[0]() != 1")
        if fs[1]() != 2:
            panic("fs[1]() != 2")
        if applyAt(fs, 1) != 2:
            panic("applyAt(fs, 1) != 2")
        d: Dispatch = Dispatch(handlers: [two, one])
        if d.handlers[0]() != 2:
            panic("d.handlers[0]() != 2")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] func_element_index_call_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
