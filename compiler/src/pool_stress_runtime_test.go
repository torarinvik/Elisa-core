package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Persistent thread pool stress + determinism. Exercises the real worker pool
// (queue + completion handshake + worker join on shutdown) hard: a submit storm of
// 8000 tasks across 3 workers in two waves (reusing the persistent workers),
// reduce() determinism across worker counts 1/2/4/8, 50 rounds of explicit
// pool create+shutdown, and 200 default-pool parallel-for sweeps. Bit-exact results
// across worker counts prove race-freedom; completion without a timeout proves no
// deadlock in the queue / shutdown-join path.
func TestRunCLIStdPoolStressRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_pool_stress_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("pool stress runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "passed=4 skipped=0 failed=0") {
		t.Fatalf("expected all 4 pool stress tests to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
