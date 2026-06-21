package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// par_map(src, dst, transform): the parallel map-into combinator. Fills a caller-allocated dst with
// transform(src[i]) over disjoint bands in parallel. This compiles + runs the fixture natively and
// asserts the parallel result is bit-identical to the sequential map (race-free by band disjointness).
func TestRunCLIStdParMapRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_par_map_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std par_map runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_par_map_smoke") {
		t.Fatalf("expected std par_map smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
