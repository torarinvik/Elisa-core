package main

import (
	"strings"
	"testing"
)

// Boundary-biased input generation (docs: @property/@differential fuzz harness).
//
// Uniform-random scalar generation is BLIND to sparse domains: a real page-alignment
// bug went undetected because a random u64 is essentially never page-aligned, so an
// impl-vs-reference differential over `is_aligned(x)` agreed vacuously on every drawn
// input. These tests confirm the biased draw now hits the structured edge-case set
// (page-aligned values, masks, powers of two, near-MAX) and catches a divergence that
// lives ONLY on that sparse domain -- while ordinary always-true properties still pass
// (the edge cases introduce no false failures), and determinism/shrinking are preserved.

// A divergence that exists ONLY on the page-aligned (x % 4096 == 0) sub-domain. Uniform
// u64 draws essentially never land there, so without bias the differential passes
// vacuously; with boundary bias it must find and report a counterexample.
func TestBoundaryBiasCatchesSparseAlignmentDivergence(t *testing.T) {
	t.Parallel()
	const body = `
def reference_is_page_aligned(x: u64) -> bool:
    return x % 4096 == 0

def impl_is_page_aligned_bad(x: u64) -> bool:
    return false

@differential
def diff_page_aligned(x: u64) -> bool:
    return impl_is_page_aligned_bad(x) == reference_is_page_aligned(x)
`
	_, stdout, stderr := runPropertyProgram(t, "bias_sparse_align", body)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected the sparse page-alignment divergence to be caught (failed=1), got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "differential diff_page_aligned counterexample") {
		t.Fatalf("expected a differential counterexample report, got:\n%s", stdout+stderr)
	}
}

// The biased edge-case set (0, masks, powers of two, MAX) must not create false
// failures for a property that genuinely holds for ALL inputs.
func TestBoundaryBiasNoFalseFailuresOnHoldingProperty(t *testing.T) {
	t.Parallel()
	const body = `
@property
def mask_identity_u64(x: u64) -> bool:
    return (x & 0) == 0

@property
def add_commutes(a: i32, b: i32) -> bool:
    return a + b == b + a

@property
def u8_roundtrip(x: u8) -> bool:
    return x.u64().u8() == x
`
	exit, stdout, stderr := runPropertyProgram(t, "bias_holding", body)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "passed=3") || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected passed=3 failed=0 (no false failures from edge cases), got:\n%s", stdout)
	}
}

// Determinism: the same seed (same property name + source) yields the same outcome
// across runs, so reproduction and shrinking stay stable.
func TestBoundaryBiasDeterministic(t *testing.T) {
	t.Parallel()
	const body = `
def reference_low_bit(x: u64) -> bool:
    return (x & 1) == 1

def impl_low_bit_bad(x: u64) -> bool:
    return x % 4096 == 0

@differential
def diff_det(x: u64) -> bool:
    return impl_low_bit_bad(x) == reference_low_bit(x)
`
	_, out1, _ := runPropertyProgram(t, "bias_det", body)
	_, out2, _ := runPropertyProgram(t, "bias_det", body)
	extract := func(s string) string {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "x (u64) =") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	c1, c2 := extract(out1), extract(out2)
	if c1 == "" {
		t.Fatalf("expected a counterexample, got:\n%s", out1)
	}
	if c1 != c2 {
		t.Fatalf("non-deterministic counterexample: %q vs %q", c1, c2)
	}
}
