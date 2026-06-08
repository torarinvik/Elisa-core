package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Set comprehension end to end: `{ value for name in src [if p] }` parses (Set flag on
// ListComprehensionExpr), type-checks to set[V], lowers to a fused loop of `s.add(v)`, and runs.
// Covers iterable dedup, range+filter, and a mapped body.
func TestRunCLISetComprehensionRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "set_comprehension_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("set comprehension smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] set_comprehension_smoke") {
		t.Fatalf("expected set comprehension smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
