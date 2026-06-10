package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: an `[errorset R]` protocol method conforms, and `T.run(f)` inside
// an interface-bounded generic dispatches to each impl with R monomorphized per
// call site — two impls x two error sets, ok and raise paths.
func TestRunCLIErrorSetParamProtocolDispatch(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "error_set_protocol_fixture.elisa")
	src := `error IoErr:
    Bad

error NetErr:
    Down

protocol Runner:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]

struct AddOne:
    tag: i64

struct Double:
    tag: i64

impl Runner for AddOne:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
        v: i64 = try f()
        return v + 1

impl Runner for Double:
    def run[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
        v: i64 = try f()
        return v * 2

def ioOk() -> i64 error[IoErr]:
    return 7

def ioFail() -> i64 error[IoErr]:
    raise IoErr.Bad

def netOk() -> i64 error[NetErr]:
    return 5

def drive[T: Runner, errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return T.run(f)

def runAddIo() -> i64:
    catch drive[AddOne, IoErr](ioOk):
        n:
            return n
        IoErr.Bad:
            return -1

def runAddIoFail() -> i64:
    catch drive[AddOne, IoErr](ioFail):
        n:
            return n
        IoErr.Bad:
            return 100

def runDoubleNet() -> i64:
    catch drive[Double, NetErr](netOk):
        n:
            return n
        NetErr.Down:
            return -1

@test
def proto_errorset_test() -> void:
    can Abort.Panic:
        if runAddIo() != 8:
            panic("expected 8")
        if runAddIoFail() != 100:
            panic("expected IoErr.Bad -> 100")
        if runDoubleNet() != 10:
            panic("expected 10")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write error-set-protocol fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected error-set-protocol test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] proto_errorset_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected error-set-protocol output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
