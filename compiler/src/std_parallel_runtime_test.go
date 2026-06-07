package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// each() / reduce(): the data-parallel combinators over Slice[T]. This compiles + runs the
// fixture natively: each() maps over disjoint bands, reduce() folds with per-worker partials
// (no shared accumulator). The asserts (counter, sum=500500, max=1000) confirm correctness
// and race-freedom.
func TestRunCLIStdParallelRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_parallel_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std parallel runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_parallel_native_smoke") {
		t.Fatalf("expected std parallel smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
