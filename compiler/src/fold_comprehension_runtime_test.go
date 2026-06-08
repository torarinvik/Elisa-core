package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Fold / reduce comprehension end to end: `( body for x in src [if p] with acc [: T] = seed )`
// parses, desugars to a fused accumulator loop (no new AST node), type-checks, lowers, and runs.
// Covers sum, a typed-accumulator order-sensitive weighted checksum over a range, a filtered
// fold, product, and a conditional-body running max.
func TestRunCLIFoldComprehensionRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "fold_comprehension_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("fold comprehension smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] fold_comprehension_smoke") {
		t.Fatalf("expected fold comprehension smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
