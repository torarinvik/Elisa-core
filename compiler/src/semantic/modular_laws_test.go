//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Aligned[N] as a return refinement, proven by the cheap syntactic tier: a value masked with the
// alignment complement (`addr & ~(4096-1)`) is provably a multiple of 4096. No solver needed.
func TestModularAlignedReturnProvenByMasking(t *testing.T) {
	src := `
def page_base(addr: u64) -> u64 is Aligned[4096]:
    return addr & 0xFFFFFFFFFFFFF000
`
	result := analyzeContractStrict(t, "aligned_mask.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`addr & ~0xFFF` is a multiple of 4096; Aligned[4096] must discharge; got: %v", errs)
	}
}

// Aligned[N] proven via the SMT bitvector lane: a parameter already refined Aligned[4096] (passed via
// a refinement type alias) plus a multiple of 4096 stays aligned. The `+` is outside the cheap
// syntactic shapes, so this exercises the modular SMT body `(self % 4096) == 0`.
func TestModularAlignedThroughSMT(t *testing.T) {
	src := `
def next_page(addr: u64 is Aligned[4096]) -> u64 is Aligned[4096]:
    return addr + 4096
`
	result := analyzeContractStrict(t, "aligned_smt.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("aligned param + multiple-of-N stays Aligned[4096]; got: %v", errs)
	}
}

// Fits[bits] proven via masking: `x & 0x3F` is < 2^6, so it fits in 6 bits.
func TestModularFitsByMasking(t *testing.T) {
	src := `
def reg_field(reg: u32) -> u32 is Fits[6]:
    return (reg >> 12) & 0x3F
`
	result := analyzeContractStrict(t, "fits_mask.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`x & 0x3F` fits in 6 bits; Fits[6] must discharge; got: %v", errs)
	}
}

// MaskedZero[mask] proven by the cheap tier: `x & 0xFFFFF000` has the low 12 bits clear, so
// `result & 0xFFF == 0`.
func TestModularMaskedZeroCheap(t *testing.T) {
	src := `
def clear_low(x: u64) -> u64 is MaskedZero[0xFFF]:
    return x & 0xFFFFFFFFFFFFF000
`
	result := analyzeContractStrict(t, "maskedzero.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`x & ~0xFFF` has the low bits clear; MaskedZero[0xFFF] must discharge; got: %v", errs)
	}
}

// SOUNDNESS: an UNSOUND alignment claim must still be rejected under -strict. `x + 1` is not a
// multiple of 4096 for arbitrary x (e.g. x = 0 gives 1), so Aligned[4096] on the return must fail
// with the standard "could not be proven" diagnostic — the modular tier never fabricates a proof.
func TestModularAlignedUnsoundRejected(t *testing.T) {
	src := `
def bad(x: u64) -> u64 is Aligned[4096]:
    return x + 1
`
	result := analyzeContractStrict(t, "aligned_unsound.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`x + 1` is not Aligned[4096]; it must be rejected, got: %v", result.Errors())
	}
}

// SOUNDNESS at the CALL boundary: passing a not-provably-aligned argument to a parameter typed
// Aligned[4096] (via a refinement type alias) must error under -strict.
func TestModularAlignedCallArgUnsoundRejected(t *testing.T) {
	src := `
def mapper(addr: u64 is Aligned[4096]) -> u64:
    return 0

def caller(raw: u64) -> u64:
    return mapper(raw + 1)
`
	result := analyzeContractStrict(t, "aligned_callarg_unsound.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("a non-aligned argument must be rejected at the call boundary, got: %v", result.Errors())
	}
}
