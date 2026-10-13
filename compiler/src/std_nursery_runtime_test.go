package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Nurseries auto-join their submissions at block exit. This compiles + runs the fixture
// natively and checks bare nurseries, shared-pool reuse across batches, and TaskGroup reuse.
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
	if !strings.Contains(stdout.String(), "[       OK ] std_nursery_shared_pool_smoke") {
		t.Fatalf("expected shared-pool nursery smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_task_group_reuse_smoke") {
		t.Fatalf("expected TaskGroup reuse smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
