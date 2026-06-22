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

// PRIMARY (alias transparency): when a parameter is typed by a `type` ALIAS whose definition carries a
// refinement (`type PageAddr = u64 is Aligned[4096]`), the predicate must NOT erase at the call
// boundary — an unaligned argument must be rejected under -strict, exactly like the inline form.
func TestModularAlignedAliasParamUnsoundRejected(t *testing.T) {
	src := `
type PageAddr = u64 is Aligned[4096]
def map_fixed(addr: PageAddr) -> u64:
    return addr
def caller(raw: u64) -> u64:
    return map_fixed(raw)
`
	result := analyzeContractStrict(t, "aligned_alias_unsound.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("an alias-typed Aligned[4096] param must reject an unaligned argument, got: %v", result.Errors())
	}
}

// PRIMARY (positive): a provably-aligned argument passed to an alias-typed Aligned[4096] parameter
// must prove with no error. `page_align_down -> u64 is Aligned[4096]` gives a result whose contract
// entails the parameter's refinement, so `map_fixed(page_align_down(raw))` is discharged.
func TestModularAlignedAliasParamProven(t *testing.T) {
	src := `
type PageAddr = u64 is Aligned[4096]
def map_fixed(addr: PageAddr) -> u64:
    return addr
def page_align_down(raw: u64) -> u64 is Aligned[4096]:
    return raw & 0xFFFFFFFFFFFFF000
def caller(raw: u64) -> u64:
    return map_fixed(page_align_down(raw))
`
	result := analyzeContractStrict(t, "aligned_alias_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a provably-aligned argument must discharge the alias-typed Aligned[4096] param; got: %v", errs)
	}
}

// SECONDARY: an inline-`is` refinement in a parameter type must resolve `Aligned` as a predicate, not
// be looked up as a callable/type identifier — so no spurious "undefined identifier" / "cannot call
// non-function value" diagnostic leaks even when the obligation is unproven.
func TestModularInlineParamNoSpuriousIdentifier(t *testing.T) {
	src := `
def map_fixed(addr: u64 is Aligned[4096]) -> u64:
    return addr
def caller(raw: u64) -> u64:
    return map_fixed(raw)
`
	result := analyzeContractStrict(t, "inline_no_spurious.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if strings.Contains(joined, "undefined identifier") || strings.Contains(joined, "cannot call non-function value") {
		t.Fatalf("Aligned in a refinement position must not be looked up as an identifier/callable; got: %v", result.Errors())
	}
	// The genuine obligation must still be reported (the arg is not provably aligned).
	if !strings.Contains(joined, "could not be proven statically") {
		t.Fatalf("an unaligned inline-param argument must still be rejected, got: %v", result.Errors())
	}
}

// `aligned` is a valid identifier (the vestigial reserved keyword was removed), and a refinement on
// a var-decl carries through: a refined-return initializer discharges the local by contract, and the
// refinement-typed immutable local then satisfies a same-predicate parameter without a runtime check.
func TestModularRefinedLocalFlowsThroughToParam(t *testing.T) {
	src := `
type PageAddr = u64 is Aligned[4096]
def map_fixed(addr: PageAddr) -> u64:
    return addr
def page_align_down(raw: u64) -> u64 is Aligned[4096]:
    return raw & 0xFFFFFFFFFFFFF000
def use_aligned(raw: u64) -> u64:
    aligned: u64 is Aligned[4096] = page_align_down(raw)
    return map_fixed(aligned)
`
	result := analyzeContractStrict(t, "aligned_flow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`aligned` identifier + refined-local-flows-to-param should discharge cleanly, got: %v", errs)
	}
}

// Soundness boundary: an UNTYPED local does not inherit a refined initializer's predicate, so passing
// it where the predicate is required must still be rejected (the refinement must be on the binding).
func TestModularUntypedLocalDoesNotInheritRefinement(t *testing.T) {
	src := `
type PageAddr = u64 is Aligned[4096]
def map_fixed(addr: PageAddr) -> u64:
    return addr
def page_align_down(raw: u64) -> u64 is Aligned[4096]:
    return raw & 0xFFFFFFFFFFFFF000
def leaky(raw: u64) -> u64:
    base: u64 = page_align_down(raw)
    return map_fixed(base)
`
	result := analyzeContractStrict(t, "aligned_untyped.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven statically") {
		t.Fatalf("an untyped local must not silently inherit the refinement, got: %v", result.Errors())
	}
}

// Soundness: a MUTABLE refinement-typed local is not a standing guarantee (it may be reassigned), so
// it must NOT be trusted to satisfy a same-predicate parameter.
func TestModularMutableRefinedLocalNotTrustedAsArg(t *testing.T) {
	src := `
type PageAddr = u64 is Aligned[4096]
def map_fixed(addr: PageAddr) -> u64:
    return addr
def page_align_down(raw: u64) -> u64 is Aligned[4096]:
    return raw & 0xFFFFFFFFFFFFF000
def evil(raw: u64) -> u64:
    base: mutable u64 is Aligned[4096] = page_align_down(raw)
    base <- raw + 1
    return map_fixed(base)
`
	result := analyzeContractStrict(t, "aligned_mutable.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven statically") {
		t.Fatalf("a reassigned mutable refined local must not be trusted, got: %v", result.Errors())
	}
}

// A `name = X % C` local is bounded 0 <= name < C for any X when C>0 and name is unsigned, EVEN
// when X carries a width conversion/shift that the SMT translator cannot model (e.g. an i32->u64
// `.u64()` then `>> 16`). Previously the opaque left operand dropped the whole local and with it the
// modulo bound, so an alias-typed refinement param (VmCap = u64 is InRange[0, C-1]) failed to
// discharge. Regression for the emulator's vm_window_end property.
func TestModuloBoundSurvivesOpaqueLeftOperand(t *testing.T) {
	src := `
const M: u64 = 0x100000000000
law InR(self: u64, lo: u64, hi: u64) = self >= lo and self <= hi
type Cap = u64 is InR[0, 0x100000000000]
def takes_cap(a: Cap) -> u64:
    return a
def via_conversion(seed: i32) -> u64:
    a: u64 = (seed.u64() >> 16) % (M + 1)
    return takes_cap(a)
`
	result := analyzeContractStrict(t, "modulo_opaque.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("X-mod-C with opaque X should bound the local and discharge Cap, got: %v", errs)
	}
}

// Soundness guard for the above: a SIGNED `x % c` may be negative (truncated remainder), so the
// 0 <= name bound must NOT be seeded for signed locals — a NonNeg refinement must still be rejected.
func TestModuloBoundNotSeededForSignedLocal(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0
type NN = i64 is NonNeg
def takes_nn(a: NN) -> i64:
    return a
def signed_mod(x: i64, c: i64) -> i64:
    requires c > 0
    a: i64 = x % c
    return takes_nn(a)
`
	result := analyzeContractStrict(t, "modulo_signed.elisa", src)
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "could not be proven statically") {
		t.Fatalf("a signed `x %% c` may be negative; NonNeg must NOT be falsely proven, got: %v", result.Errors())
	}
}
