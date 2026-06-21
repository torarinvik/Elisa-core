package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Comma-head bindings on list/dict/set comprehensions end to end: `name [:T] = e` items before
// the body parse (Bindings on ListComprehensionExpr), are analyzed as per-element lets in the
// loop scope (in scope for body + filter), lower into the fused loop body, and run.
func TestRunCLIComprehensionHeadBindingsRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "comprehension_head_bindings_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("comprehension head bindings smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] comprehension_head_bindings_smoke") {
		t.Fatalf("expected comprehension head bindings smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
