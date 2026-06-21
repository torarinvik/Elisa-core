package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end (docs/64 follow-up): symbolic error-set unions. A combinator can
// union its error-set param with its OWN failure mode (`error[R, Timeout]`,
// the retry/give-up shape), re-raise the opaque error via the `error e:`
// catch-all, and two independent params can union in a return (`error[R, S]`,
// the pair shape), and binding joins across arguments so a narrower-set
// callback may come first. All monomorphize and round-trip at runtime.
func TestRunCLIErrorSetParamUnions(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "error_set_union_fixture.elisa")
	src := `error Timeout:
    Expired

error IoErr:
    Bad

error NetErr:
    Down

def ioFail() -> i64 error[IoErr]:
    raise IoErr.Bad

def ioOk() -> i64 error[IoErr]:
    return 7

def netOk() -> i64 error[NetErr]:
    return 9

def retryTwice[errorset R](f: func() -> i64 error[R], propagate: bool) -> i64 error[R, Timeout]:
    catch f():
        n:
            return n
        error e:
            if propagate:
                raise e
            catch f():
                m:
                    return m
                error e2:
                    raise Timeout.Expired

def pair[errorset R, errorset S](f: func() -> i64 error[R], g: func() -> i64 error[S]) -> i64 error[R, S]:
    return (try f()) + (try g())

def runRetryGiveUp() -> i64:
    catch retryTwice(ioFail, false):
        n:
            return n
        IoErr.Bad:
            return -1
        Timeout.Expired:
            return 100

def runRetryPropagate() -> i64:
    catch retryTwice(ioFail, true):
        n:
            return n
        IoErr.Bad:
            return 200
        Timeout.Expired:
            return -1

def runRetryOk() -> i64:
    catch retryTwice(ioOk, false):
        n:
            return n
        IoErr.Bad:
            return -1
        Timeout.Expired:
            return -2

def runPairOk() -> i64:
    catch pair(ioOk, netOk):
        n:
            return n
        IoErr.Bad:
            return -1
        NetErr.Down:
            return -2

def bothOf[errorset R](f: func() -> i64 error[R], g: func() -> i64 error[R]) -> i64 error[R]:
    return (try f()) + (try g())

def bigFail() -> i64 error[IoErr, NetErr]:
    raise NetErr.Down

def runJoinNarrowFirst() -> i64:
    catch bothOf(ioOk, bigFail):
        n:
            return n
        IoErr.Bad:
            return -1
        NetErr.Down:
            return 400

def runPairFail() -> i64:
    catch pair(ioFail, netOk):
        n:
            return n
        IoErr.Bad:
            return 300
        NetErr.Down:
            return -2

@test
def error_set_union_test() -> void:
    can Abort.Panic:
        if runRetryGiveUp() != 100:
            panic("expected Timeout give-up path -> 100")
        if runRetryPropagate() != 200:
            panic("expected re-raised IoErr.Bad -> 200")
        if runRetryOk() != 7:
            panic("expected ok 7")
        if runPairOk() != 16:
            panic("expected 16")
        if runPairFail() != 300:
            panic("expected IoErr.Bad through pair -> 300")
        if runJoinNarrowFirst() != 400:
            panic("expected joined narrow-first NetErr.Down -> 400")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write error-set-union fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected error-set-union test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] error_set_union_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected error-set-union output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}

// End-to-end (docs/64 Phase 5b): a BARE expr-lambda whose body propagates errors
// (`lambda() => try ioFail()`) infers its error-union return from what it propagates,
// binds the callee's error-set param R, and round-trips at runtime — both the ok and
// the error paths. No annotation on the lambda.
func TestRunCLIBareLambdaErrorInference(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "bare_lambda_infer_fixture.elisa")
	src := `error IoErr:
    Bad

def ioFail() -> i64 error[IoErr]:
    raise IoErr.Bad

def ioOk() -> i64 error[IoErr]:
    return 7

def applyOnce[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def runInferredOk() -> i64:
    catch applyOnce(lambda() => try ioOk()):
        n:
            return n
        IoErr.Bad:
            return -1

def runInferredFail() -> i64:
    catch applyOnce(lambda() => try ioFail()):
        n:
            return n
        IoErr.Bad:
            return 42

@test
def bare_lambda_infer_test() -> void:
    can Abort.Panic:
        if runInferredOk() != 7:
            panic("expected inferred-ok bare lambda -> 7")
        if runInferredFail() != 42:
            panic("expected inferred IoErr.Bad through bare lambda -> 42")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write bare-lambda fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected bare-lambda inference test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] bare_lambda_infer_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected bare-lambda output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
