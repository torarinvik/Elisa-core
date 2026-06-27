//go:build cgo

package semantic

import "testing"

// COMPLETENESS-POSITIVE: a quantified loop invariant over a STRUCT-FIELD array is proven inductive.
// The struct `Buf` has a field `data: darray[i64]`; the loop fills `b.data[i] <- 0` and advances `i`.
// The invariant `forall k: (0 <= k and k < i) implies b.data[k] == 0` is established vacuously (i=0)
// and preserved via the SMT array-store model `(store v_b__field__data i 0)`, exactly mirroring the
// bare-array case — the path key `b__field__data` produced by normalizeFieldPathLValue matches the
// projection name smtProjectionName uses in arrayTermEnv.
func TestQuantifiedLoopInvariantOverStructFieldArrayFillPreserved(t *testing.T) {
	src := `
struct Buf:
    data: darray[i64]

def fill_struct_zero(b: mutable Buf&, n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies b.data[k] == 0
        b.data[i] <- 0
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_struct_field_fill.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the struct-field array-fill invariant to be proven preserved via SMT store model, got %d: %+v", got, result.ProofReport)
	}
	if got := countLoopInvariantProof(result, "establish", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the struct-field quantified invariant to be established (vacuously) on entry, got %d: %+v", got, result.ProofReport)
	}
}

// COMPLETENESS-POSITIVE: constant fill value also proves for struct-field arrays.
func TestQuantifiedLoopInvariantOverStructFieldArrayConstantFillPreserved(t *testing.T) {
	src := `
struct Buf:
    data: darray[i64]

def fill_struct_seven(b: mutable Buf&, n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies b.data[k] == 7
        b.data[i] <- 7
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_struct_field_seven.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the struct-field constant-fill invariant to be proven preserved, got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS-NEGATIVE: a FALSE quantified invariant over a struct-field array fill must NOT be proven.
// The body writes 0 but the invariant claims every filled cell is 1; z3 finds a counterexample (the
// just-written cell is 0, not 1), so preservation declines to the runtime check.
func TestQuantifiedLoopInvariantOverStructFieldArrayFalseFillDeclined(t *testing.T) {
	src := `
struct Buf:
    data: darray[i64]

def fill_struct_bad(b: mutable Buf&, n: usize):
    i: mutable usize = 0
    while i < n:
        invariant forall k: (0 <= k and k < i) implies b.data[k] == 1
        b.data[i] <- 0
        i <- i + 1
`
	result := analyzeLoopInvariantWithSMT(t, "quant_struct_field_false.elisa", src, false)
	if got := countLoopInvariantProof(result, "preserve", ProofProvenSMT); got != 0 {
		t.Fatalf("a false struct-field fill invariant must NOT be proven preserved (cell is 0, not 1): %+v", result.ProofReport)
	}
	if got := countLoopInvariantProof(result, "preserve", ProofRuntime); got != 1 {
		t.Fatalf("expected the false struct-field invariant to fall back to a runtime preservation check, got %d: %+v", got, result.ProofReport)
	}
}
