package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Dict comprehension end to end: `{ key: value for name in src [if p] }` parses (Key field on
// ListComprehensionExpr), type-checks to dict[K,V], lowers to a fused loop of `d.put(k, v)`, and
// runs. Covers range, filtered, and iterable sources.
func TestRunCLIDictComprehensionRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "dict_comprehension_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("dict comprehension smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] dict_comprehension_smoke") {
		t.Fatalf("expected dict comprehension smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
