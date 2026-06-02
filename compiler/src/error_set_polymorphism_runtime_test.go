package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end (Phase 5b): an error-set-polymorphic combinator `apply[errorset R]`
// is monomorphized for two different callback error sets (IoErr and NetErr) in
// the same program, and both the ok-path and the raised-error path round-trip
// correctly through it at runtime.
func TestRunCLIErrorSetPolymorphismMonomorphizesPerCallbackSet(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "error_set_poly_fixture.elisa")
	src := `error IoErr:
    Bad

error NetErr:
    Down

def ioOk() -> i64 error[IoErr]:
    return 7

def ioFail() -> i64 error[IoErr]:
    raise IoErr.Bad

def netOk() -> i64 error[NetErr]:
    return 9

def netFail() -> i64 error[NetErr]:
    raise NetErr.Down

# The polymorphic combinator: doubles the callback's ok value, propagating
# whatever error set the callback raises.
def applyDouble[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    v: i64 = try f()
    return v * 2

# Each helper instantiates applyDouble at a distinct error set and folds the
# error-union result down to a plain i64 via catch.
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
            return 100

def runNetOk() -> i64:
    catch applyDouble(netOk):
        n:
            return n
        NetErr.Down:
            return -1

def runNetFail() -> i64:
    catch applyDouble(netFail):
        n:
            return n
        NetErr.Down:
            return 200

@test
def error_set_poly_test() -> void:
    can Abort.Panic:
        if runIoOk() != 14:
            panic("expected 14 from IoErr ok path")
        if runIoFail() != 100:
            panic("expected 100 from IoErr error path")
        if runNetOk() != 18:
            panic("expected 18 from NetErr ok path")
        if runNetFail() != 200:
            panic("expected 200 from NetErr error path")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write error-set-poly fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected error-set-poly test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] error_set_poly_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected error-set-poly output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
