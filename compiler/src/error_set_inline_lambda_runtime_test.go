package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: an INLINE lambda passed to an `[errorset R]` combinator. The
// infallible `fn(n) => n + 1` binds R := ∅ (the combinator collapses to a
// plain return); a raising `fn(n) => raise IoErr.Bad` infers error[IoErr]
// and binds R to it, so the SAME combinator catches the raised tag — both
// monomorphizations run in one program.
func TestRunCLIErrorSetParamInlineLambda(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "error_set_inline_lambda_fixture.elisa")
	src := `error IoErr:
    Bad

def applyOne[errorset R](f: fn(i64) -> i64 error[R], x: i64) -> i64 error[R]:
    v: i64 = try f(x)
    return v * 2

def runPure() -> i64:
    return applyOne(fn(n) => n + 1, 3)

def runRaise() -> i64:
    catch applyOne(fn(n) => raise IoErr.Bad, 3):
        v:
            return v
        IoErr.Bad:
            return 99

@test
def inline_lambda_test() -> void:
    can Abort.Panic:
        if runPure() != 8:
            panic("expected pure inline lambda path 8")
        if runRaise() != 99:
            panic("expected raising inline lambda path 99")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inline-lambda fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inline-lambda test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] inline_lambda_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inline-lambda output to contain %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
