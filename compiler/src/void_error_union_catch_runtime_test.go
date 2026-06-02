package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression (runtime): a `void error[E]` union caught in statement form must
// codegen correctly on both the ok and error paths. Previously the catch backend
// unconditionally extracted the success payload, which fails for a void union
// ("error union has no payload").
func TestRunCLIVoidErrorUnionCatch(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "void_error_union_catch.elisa")
	src := `error E:
    Bad

def act(fail: bool) -> void error[E]:
    if fail:
        raise E.Bad
    return

def run(fail: bool) -> i64:
    catch act(fail):
        n:
            true
        error e:
            return 1
    return 2

@test
def void_error_union_catch_test() -> void:
    can Abort.Panic:
        if run(false) != 2:
            panic("expected ok path -> 2")
        if run(true) != 1:
            panic("expected error path -> 1")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("void-error-union catch test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] void_error_union_catch_test") {
		t.Fatalf("expected OK, got:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
