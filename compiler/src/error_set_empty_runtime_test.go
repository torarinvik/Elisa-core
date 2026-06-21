package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end (docs/64 Phase 5a): an infallible callback fed to an `[errorset R]`
// combinator binds R := ∅; the combinator's `T error[∅]` return collapses to a
// plain `T`, and the SAME combinator still works for a fallible callback in the
// same program (distinct monomorphizations: scalar return vs union return).
func TestRunCLIErrorSetParamEmptySet(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "error_set_empty_fixture.elisa")
	src := `error IoErr:
    Bad

def applyDouble[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    v: i64 = try f()
    return v * 2

def pure() -> i64:
    return 5

def ioOk() -> i64 error[IoErr]:
    return 6

def ioFail() -> i64 error[IoErr]:
    raise IoErr.Bad

def runPure() -> i64:
    return applyDouble(pure)

def runIoOk() -> i64:
    catch applyDouble(ioOk):
        n:
            return n
        IoErr.Bad:
            return -1

def runIoFail() -> i64:
    catch applyDouble(ioFail):
        n:
            return n
        IoErr.Bad:
            return 99

@test
def empty_set_test() -> void:
    can Abort.Panic:
        if runPure() != 10:
            panic("expected pure path 10")
        if runIoOk() != 12:
            panic("expected ioOk 12")
        if runIoFail() != 99:
            panic("expected ioFail 99")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write error-set-empty fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected error-set-empty test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] empty_set_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected error-set-empty output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
