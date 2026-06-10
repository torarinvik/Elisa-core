package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/80 Phase A: `if v is value` binds the unwrapped optional payload (the
// refutable analog of the retired `if let value = v`), composes with an `and`
// guard, and coexists with the existing variant/type `is` tests.
func TestRunCLIIsOptionalBindingExecutes(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "is_binding_fixture.elisa")
	src := `def unwrap(v: i64?) -> i64:
    if v is value:
        return value + 1
    return 0

def guarded(v: i64?) -> i64:
    if v is value and value > 10:
        return value
    return -1

@test
def is_optional_binding_test() -> void:
    can Abort.Panic:
        if unwrap(7) != 8:
            panic("optional bind wrong")
        if unwrap(null) != 0:
            panic("optional none wrong")
        if guarded(20) != 20:
            panic("guarded bind wrong")
        if guarded(5) != -1:
            panic("guarded reject wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected is-binding runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{"[       OK ] is_optional_binding_test", "passed=1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}
