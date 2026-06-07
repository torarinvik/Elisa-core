package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Slice[T] is the safe, shareable, disjoint-partitionable view that replaces raw
// `uintptr.cast[T&]` for data-parallel work. This compiles + runs the fixture natively:
// split tiles the slice with no overlap/gap, bounds-checked access round-trips, and a
// parallel map over the disjoint bands (one per worker) is race-free (every element
// transformed exactly once).
func TestRunCLIStdSliceRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_slice_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std slice runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_slice_native_smoke") {
		t.Fatalf("expected std slice smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
