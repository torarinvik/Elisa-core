package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Phase B2 (early per-object reclamation): an own-stack growable that dies before the function
// returns and is never aliased has its arena freed early (right after its last use) rather than at
// region exit. End-to-end: the program still computes correctly (the early free is idempotent with
// the region-exit cleanup, and the object is genuinely dead at the free point).
func TestRunCLIEarlyFreeReclaimsDeadObject(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "early_free_fixture.elisa")
	src := `def f(n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        scratch: mutable darray[i64] = []
        scratch.push(10)
        scratch.push(20)
        summary: mutable i64 = scratch[0] + scratch[1]
        kept: mutable darray[i64] = []
        kept.push(summary)
        kept.push(summary * 2)
        return kept[0] + kept[1]
@test
def early_free_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if f(0) != 90:
            panic("early-free changed the result")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected early-free test to compile and pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] early_free_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
