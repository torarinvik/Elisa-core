package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

// pinned_fold_smoke exercises the Phase 2 fixed-width pinned reduction across the W boundary (small-n
// fallback, exact n==W, and n=8k+remainder), for `+`/`*` and FP/integer accumulators, with a
// non-zero seed and a transformed element. Its asserts encode the exact mathematical results, so
// running it at BOTH -O0 and -O3 also pins the headline guarantee: the pinned reduction is
// bit-identical across optimization levels (a vectorizer that re-bracketed it would fail an assert).
func TestRunCLIPinnedFoldSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "pinned_fold_smoke.elisa")

	for _, optLevel := range []string{"-O0", "-O3"} {
		t.Run(optLevel, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI([]string{"-emit", "test", optLevel, fixturePath}, &stdout, &stderr); code != 0 {
				t.Fatalf("pinned fold smoke failed at %s (%d):\nstdout:\n%s\nstderr:\n%s", optLevel, code, stdout.String(), stderr.String())
			}
			if !bytes.Contains(stdout.Bytes(), []byte("[       OK ] pinned_fold_smoke")) {
				t.Fatalf("expected pinned_fold_smoke to pass at %s, got:\n%s", optLevel, stdout.String())
			}
		})
	}
}
