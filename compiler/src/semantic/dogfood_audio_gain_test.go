//go:build cgo

package semantic

import "testing"

// DOGFOOD (docs/90): a realistic hot path — applying an 8.8 fixed-point gain to an audio sample —
// exercises the SMT tier's headline case (a var*var PRODUCT the affine prover cannot bound) and shows
// the engine ELIDE the return-refinement runtime check that a real mixer would otherwise pay per
// sample. With the inputs carrying refinement bounds, `(s * g) / 256` is provably in [0, 255]:
//   s ∈ [0,255], g ∈ [0,256] ⟹ s*g ∈ [0,65280] ⟹ (s*g)/256 ∈ [0,255].
// This is the "measure checks elided" half of the adoption story: the contract turns a per-sample
// debug check into a static proof.
func TestDogfoodAudioGainProvenNoRuntimeCheck(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Sample = i64 is Bounded[0, 255]
type Gain = i64 is Bounded[0, 256]

def apply_gain(s: Sample, g: Gain) -> i64 is Bounded[0, 255]:
    return (s * g) / 256
`
	result := analyzeWithSMT(t, "dogfood_gain_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("the gain product is provably in [0,255]; expected a clean analysis, got: %v", errs)
	}
	// MEASURE the elision via the proof report: the return bound is proven by the SMT tier (the product
	// is outside the affine fragment) and NO obligation falls back to a per-sample runtime check.
	var smtProven, runtimeChecked int
	for _, f := range result.ProofReport {
		switch f.Outcome {
		case ProofProvenSMT:
			smtProven++
		case ProofRuntime:
			runtimeChecked++
		}
	}
	if smtProven == 0 {
		t.Fatalf("expected the nonlinear gain bound to be proven by the SMT tier, report: %+v", result.ProofReport)
	}
	if runtimeChecked != 0 {
		t.Fatalf("MEASURE: the contract should ELIDE the per-sample return check, got %d runtime-checked", runtimeChecked)
	}
}

// The contrast that quantifies what the contract BUYS: drop the input refinements (plain i64 params)
// and the same return bound can no longer be proven — the engine falls back to the per-sample runtime
// check. This is the cost the dogfood example removes.
func TestDogfoodAudioGainWithoutContractsKeepsRuntimeCheck(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def apply_gain(s: i64, g: i64) -> i64 is Bounded[0, 255]:
    return (s * g) / 256
`
	result := analyzeWithSMT(t, "dogfood_gain_unbounded.elisa", src)
	// Unbounded inputs: (s*g)/256 is not in [0,255] (e.g. s=-1, g=256), so the return bound must NOT be
	// proven by any tier — it degrades to the per-sample runtime check (ProofRuntime). That fallback is
	// exactly the cost the contracted version removes.
	var provenSMT, runtimeChecked int
	for _, f := range result.ProofReport {
		switch f.Outcome {
		case ProofProvenSMT:
			provenSMT++
		case ProofRuntime:
			runtimeChecked++
		}
	}
	if provenSMT != 0 {
		t.Fatalf("without input bounds the return bound is false and must not be SMT-proven: %+v", result.ProofReport)
	}
	if runtimeChecked == 0 {
		t.Fatalf("without contracts the return bound must fall back to a runtime check (ProofRuntime): %+v", result.ProofReport)
	}
}
