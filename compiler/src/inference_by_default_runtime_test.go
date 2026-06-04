package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Inference-by-default: a function that allocates with a bare container literal — no
// `region`, no `in auto:`, no `@r`, no threaded allocator — just works. The compiler
// wraps the body in a synthesized lazy auto region, and the allocation runs against it.
// This is the headline ergonomic: you don't have to mention regions to use them.
func TestRunCLIInfersRegionForBareAllocation(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_by_default_fixture.elisa")
	src := `@test
def inference_by_default_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[i64] = []
        for i in 0..<5000:
            xs.push(i.i64())
        if xs[0] != 0i64 or xs[4999] != 4999i64:
            panic("inferred region: data wrong after growth")
        if xs.count != 5000:
            panic("inferred region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inference-by-default test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] inference_by_default_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inference output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// Inference's slack stays a diagnostic, not a leak: a function that builds a value in its
// inferred region and then returns it is rejected, because that region is freed at the
// function's exit. The fix is an explicit lifetime (a `[region r]` param and `-> ... @r`).
func TestRunCLIRejectsValueEscapingInferredFunctionRegion(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inference_escape_fixture.elisa")
	src := `def build() -> darray[i64]:
    xs: mutable darray[i64] = []
    xs.push(7)
    return xs
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write escape fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "escapes its `in auto:` scope") {
		t.Fatalf("expected an escape diagnostic for a value leaving the inferred region, got:\n%s", stderr.String())
	}
}
