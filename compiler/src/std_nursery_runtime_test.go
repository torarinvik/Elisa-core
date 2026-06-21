package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The nursery desugars to a pool scope with an implicit task group auto-waited at exit, so a
// `submit` inside it is fire-into-nursery and every task is joined before the block returns.
// This compiles + runs the fixture natively and confirms the join guarantee (the assert that
// the shared counter equals the number of submitted tasks holds right after the nursery).
func TestRunCLIStdNurseryRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_nursery_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std nursery runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_nursery_native_smoke") {
		t.Fatalf("expected std nursery smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
