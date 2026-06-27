//go:build cgo

package semantic

import "testing"

// A constant mask `x & 0xFFF` keeps only the low 12 bits, so the result is provably `< 0x1000` — the
// canonical emulator page-offset extraction. The affine/linear tiers cannot reason about `&` at all
// (they decline bitwise ops); the SMT tier proves it via the bitvector bridge (int2bv/bvand/bv2nat).
func TestBitwiseMaskUpperBoundProvesBySMT(t *testing.T) {
	src := `
law ULt(self: u64, hi: u64) = self < hi

def low12(x: u64) -> u64 is ULt[4096]:
    return x & 4095
`
	result := analyzeWithSMT(t, "bv_mask.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countRefinementProof(result, "ULt", ProofProvenSMT); got != 1 {
		t.Fatalf("expected `x & 4095 < 4096` to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// A logical right shift by a constant `addr >> 12` divides by 2^12, so a 64-bit value lands in
// [0, 2^52) — the page-number extraction. Proven by the bitvector bridge (bvlshr).
func TestBitwiseShiftUpperBoundProvesBySMT(t *testing.T) {
	src := `
law ULt(self: u64, hi: u64) = self < hi

def page_no(addr: u64) -> u64 is ULt[4503599627370496]:
    return addr >> 12
`
	result := analyzeWithSMT(t, "bv_shift.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countRefinementProof(result, "ULt", ProofProvenSMT); got != 1 {
		t.Fatalf("expected `addr >> 12 < 2^52` to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// A two-stage shift `(word >> sh) >> 4` where sh is a runtime variable makes the inner shift
// unmodelable via the bitvector bridge (non-constant sh). The outer constant shift-by-4 should still
// prove `< 2^(32-4)` via the Int-side over-approximation: the inner result is some u32, so the outer
// `>> 4` lands in [0, 2^28-1] = [0, 268435455] regardless of the inner value.
func TestBitwiseShiftOverApproxProvesUnmodelableOperand(t *testing.T) {
	src := `
law ULt(self: u32, hi: u32) = self < hi

def double_shift(word: u32, sh: u32) -> u32 is ULt[268435456]:
    return (word >> sh) >> 4
`
	result := analyzeWithSMT(t, "bv_shift_overapprox.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	if got := countRefinementProof(result, "ULt", ProofProvenSMT); got != 1 {
		t.Fatalf("expected `(word >> sh) >> 4 < 2^28` to be proven by SMT over-approx, got %d: %+v", got, result.ProofReport)
	}
}

// Soundness: the shift over-approximation bounds `X >> 4` to [0, 2^28-1] for a u32. Claiming
// `< 2^27` (half that) is NOT provable from the over-approx alone — the inner value may occupy
// the full [0, 2^32-1] range, making the result reach 2^28-1 which exceeds 2^27. The prover must
// decline rather than fabricate a tighter bound.
func TestBitwiseShiftOverApproxDeclinesTooTight(t *testing.T) {
	src := `
law ULt(self: u32, hi: u32) = self < hi

def double_shift_tight(word: u32, sh: u32) -> u32 is ULt[134217728]:
    return (word >> sh) >> 4
`
	result := analyzeWithSMT(t, "bv_shift_tight.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove `(word>>sh)>>4 < 2^27` from over-approx (result can reach 2^28-1): %+v", result.ProofReport)
		}
	}
}

// Soundness: `x | 4095` SETS the low 12 bits, so it can be as large as 2^64-1 — NOT `< 4096`. The
// bridge must NOT prove this (z3 finds a counterexample, e.g. x with a high bit set), leaving the
// runtime check. A false proof here would be a serious unsoundness.
func TestBitwiseOrUnboundedDeclines(t *testing.T) {
	src := `
law ULt(self: u64, hi: u64) = self < hi

def bad(x: u64) -> u64 is ULt[4096]:
    return x | 4095
`
	result := analyzeWithSMT(t, "bv_or.elisa", src)
	if got := countRefinementProof(result, "ULt", ProofProvenSMT); got != 0 {
		t.Fatalf("`x | 4095 < 4096` is FALSE (high bits survive) and must NOT be SMT-proven: %+v", result.ProofReport)
	}
}
