package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIGeneratesNativeCallbackRuntimeShim(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "callback_ptr_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("native callback runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] native_callback_ptr_smoke") {
		t.Fatalf("expected native callback runtime smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
