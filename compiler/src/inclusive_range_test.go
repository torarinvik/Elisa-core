package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// inclusive_range_smoke proves the inclusive range operator `lo ..= hi` (added so real code like the
// raycaster's `forall k in y ..= yend` and `for i in lo ..= hi:` works): it iterates the upper endpoint
// (1..=4 sums to 10), leaves the half-open `..<` unchanged (1..<4 sums to 6), and a single-element
// `n ..= n` runs exactly once. `..=` desugars to the half-open `lo ..< (hi + 1)` at parse time.
func TestRunCLIInclusiveRangeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "inclusive_range_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("inclusive range smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] inclusive_range_smoke") {
		t.Fatalf("expected inclusive_range_smoke to pass, got:\n%s", stdout.String())
	}
}
