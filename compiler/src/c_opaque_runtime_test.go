package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIGeneratesCOpaqueRuntimeShim(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "c_opaque_alloc_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("c_opaque runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] c_opaque_alloc_smoke") {
		t.Fatalf("expected c_opaque runtime smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
