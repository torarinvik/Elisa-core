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

// Common-shift bias: a divergence localized to EXACTLY one specific power of two
// (x == 2048 == 1<<11). The uniform `k = (s>>8) % bits` draws any single power of
// two with probability <1/bits, so before the common-shift bias this passed
// vacuously within the case budget. Biasing k toward a small high-value set of
// common alignment/width shifts {0,1,2,3,6,8,11,12,16,21,...} (clamped per type)
// for the pow2 / mask categories makes 2048 recur often enough to be caught.
func TestBoundaryBiasCatchesExactPowerOfTwo(t *testing.T) {
	t.Parallel()
	const body = `
def is_2048(x: u64) -> bool:
    return x == 2048

def always_false(x: u64) -> bool:
    return false

@differential
def diff_exactly_2048(x: u64) -> bool:
    return is_2048(x) == always_false(x)
`
	_, stdout, stderr := runPropertyProgram(t, "bias_pow2", body)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected the exact-power-of-two (x==2048) divergence to be caught (failed=1), got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "differential diff_exactly_2048 counterexample") {
		t.Fatalf("expected a differential counterexample report, got:\n%s", combined)
	}
	if !strings.Contains(combined, "x (u64) = 2048") {
		t.Fatalf("expected the counterexample to be exactly x = 2048, got:\n%s", combined)
	}
}

// The common-shift k bias must stay deterministic: same property name + source =>
// the same counterexample (and same case index) across runs, so reproduction and
// shrinking are unchanged.
func TestBoundaryBiasCommonShiftDeterministic(t *testing.T) {
	t.Parallel()
	const body = `
def is_2048(x: u64) -> bool:
    return x == 2048

def always_false(x: u64) -> bool:
    return false

@differential
def diff_exactly_2048(x: u64) -> bool:
    return is_2048(x) == always_false(x)
`
	_, out1, _ := runPropertyProgram(t, "bias_pow2_det", body)
	_, out2, _ := runPropertyProgram(t, "bias_pow2_det", body)
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
	if !strings.Contains(c1, "= 2048") {
		t.Fatalf("expected x = 2048, got %q", c1)
	}
}

// The common-shift k bias must not manufacture extra near-MAX operands that
// spuriously trap checked arithmetic in a property that genuinely holds for all
// inputs across the integer widths it touches (the pow2 / mask categories now
// recur more often). Bitwise/identity properties must still pass on i32/u32/u8 --
// the extra power-of-two and mask draws introduce no spurious counterexamples.
// (Wrapping-arithmetic overflow on narrow signed types is a pre-existing property
// of the page category, independent of this change, so these holding properties
// are overflow-free by construction.)
func TestBoundaryBiasCommonShiftNoFalseFailures(t *testing.T) {
	t.Parallel()
	const body = `
@property
def i32_mask_zero(x: i32) -> bool:
    return (x & 0) == 0

@property
def u32_self_eq(x: u32) -> bool:
    return x == x

@property
def u8_roundtrip(x: u8) -> bool:
    return x.u64().u8() == x
`
	exit, stdout, stderr := runPropertyProgram(t, "bias_pow2_noff", body)
	if exit != 0 {
		t.Fatalf("expected exit 0 (no false failures), got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "passed=3") || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected passed=3 failed=0, got:\n%s", stdout)
	}
}
