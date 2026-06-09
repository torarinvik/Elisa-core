package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The global `-ffast-math` flag (ELISACORE_FAST_MATH) opts the whole program into full fast-math
// FP, which unlocks the reassociated *tree* horizontal reduction for FP folds — `fadd fast` over
// vector lanes (parallel partial sums) instead of the default ordered `llvm.vector.reduce.fadd`.
// Reassociation changes FP results, so it must stay an explicit opt-in: by default the fold stays
// the bit-exact ordered reduction.
func TestFastMathFoldReductionReassociates(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "fast_math_fold_probe.elisa")

	emit := func(t *testing.T) string {
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", "-O3", fixturePath}, &stdout, &stderr); code != 0 {
			t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
		}
		return stdout.String()
	}

	t.Run("default stays ordered (no reassociated vector fadd)", func(t *testing.T) {
		if out := emit(t); strings.Contains(out, "fadd fast <") {
			t.Fatalf("default build must not reassociate FP folds, but found a vector `fadd fast`:\n%s", out)
		}
	})

	t.Run("fast-math unlocks reassociated tree reduction", func(t *testing.T) {
		t.Setenv("ELISACORE_FAST_MATH", "1")
		if out := emit(t); !strings.Contains(out, "fadd fast <") {
			t.Fatalf("expected a reassociated vector `fadd fast` under fast-math, got:\n%s", out)
		}
	})
}
