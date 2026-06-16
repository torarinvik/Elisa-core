package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A comprehension fold's reduction order is DEFINED as a vectorizable tree, not strict left-to-right
// (docs/79 Part IV). Phase 2 pins the tree for the eligible fast path — an associative fold over an
// indexable darray variable with a numeric-literal seed: it lowers to a fixed-width set of independent
// STRICT accumulators plus a fixed strict pairwise combine, so the rounded result is bit-identical
// across every target and optimization level. The lanes are independent, so the vectorizer may still
// pack them into SIMD registers, but no `reassoc` flag rides on them, so it can never reorder the
// reduction. Shapes Phase 2 cannot pin (e.g. a range source) keep the reassociating fallback.
func emitFoldProbeIR(t *testing.T, fixture string, optLevel string) string {
	t.Helper()
	fixturePath := filepath.Join(repoRootFromMainTest(t), "compiler", fixture)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", optLevel, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit llvm (%s %s) failed (%d):\n%s", fixture, optLevel, code, stderr.String())
	}
	return stdout.String()
}

func TestEligibleFoldPinsStrictTree(t *testing.T) {
	t.Run("the source carries a fixed-width pinned reduction", func(t *testing.T) {
		// At -O0 the pinned shape is visible verbatim: W named lane accumulators fixed in the source.
		out := emitFoldProbeIR(t, "fast_math_fold_probe.elisa", "-O0")
		if !strings.Contains(out, "__fold_a0") || !strings.Contains(out, "__fold_a7") {
			t.Fatalf("expected the eligible fold to lower to the fixed-width pinned lanes (__fold_a0..a7):\n%s", out)
		}
	})

	t.Run("default is strict — no reassociation, no full fast-math", func(t *testing.T) {
		out := emitFoldProbeIR(t, "fast_math_fold_probe.elisa", "-O3")
		if strings.Contains(out, "fadd reassoc") {
			t.Fatalf("a pinned fold must reduce strictly (no `reassoc`), but found one:\n%s", out)
		}
		if strings.Contains(out, "fadd fast") {
			t.Fatalf("default build must not stamp full `fast` FP on a pinned fold:\n%s", out)
		}
		// The W independent strict lanes still vectorize into SIMD registers.
		if !strings.Contains(out, "x double>") {
			t.Fatalf("expected the pinned lanes to vectorize into SIMD registers:\n%s", out)
		}
	})

	t.Run("fast-math escalates the pinned tree to full fast FP", func(t *testing.T) {
		t.Setenv("ELISACORE_FAST_MATH", "1")
		if out := emitFoldProbeIR(t, "fast_math_fold_probe.elisa", "-O3"); !strings.Contains(out, "fadd fast") {
			t.Fatalf("expected full `fast` FP under -ffast-math, got:\n%s", out)
		}
	})
}

// A fold Phase 2 cannot pin (here, a range source rather than an indexable darray) keeps the
// reassociating scalar fallback: its accumulator update carries `reassoc` so the loop vectorizer can
// still re-bracket it into a tree (reproducible per-target, not across targets).
func TestIneligibleFoldKeepsReassocTier(t *testing.T) {
	out := emitFoldProbeIR(t, "range_fold_probe.elisa", "-O3")
	if !strings.Contains(out, "fadd reassoc") {
		t.Fatalf("an unpinnable (range) fold must keep the reassoc tier so it can still vectorize:\n%s", out)
	}
}
