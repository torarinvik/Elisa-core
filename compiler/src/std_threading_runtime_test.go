package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStdThreadingRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_threading_smoke.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "concurrency.c")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath, "-link", runtimePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std threading runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_mutex_condvar_native_smoke") {
		t.Fatalf("expected std threading smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
