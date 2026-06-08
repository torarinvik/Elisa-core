package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The `fold` combinator carries a `@deprecated` annotation (steering to the fold comprehension).
// Deprecation is a warning, not an error, so the program still builds and runs — this guards that
// `@deprecated` on a real generic, permissioned stdlib function doesn't break compilation. The
// deprecation-diagnostic mechanism itself is covered by the semantic package's deprecated_annotation
// tests.
func TestRunCLIFoldCombinatorDeprecation(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "fold_combinator_deprecation_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("fold deprecation smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] fold_combinator_deprecation_smoke") {
		t.Fatalf("expected fold deprecation smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
