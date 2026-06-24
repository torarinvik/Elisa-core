//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Increment 3 (struct-FIELD array stores + FIELD-READ measures) for the loop-termination prover.
//
// SOUNDNESS BASIS (see analyzer_loop_invariants.go::loopStoreTargetBaseKey + the termination capture
// mode): in termination mode every store is DISCARDED and only the SCALAR measure decrease is proven.
// A field-element store `self.xs[j] <- v` changes the field array's contents but no scalar counter and
// not `self.xs.count`; and the capture rules forbid the only things that could mutate `self.xs.count`
// (method CALLS — so no `.push`/resize — and any non-Ident assignment TARGET — so no `self.xs <- …`
// whole reassignment and no `self.k <- …` field write). So every field-read measure term is invariant
// across the captured body and translates to the SAME uninterpreted SMT symbol on both sides of the
// decrease, reducing it to the scalar part. These tests pin that boundary.

const fieldShiftStructPreamble = `struct Buf:
	xs: mutable darray[i64]
	count: mutable usize

`

const areaShiftStructPreamble = `struct Space:
	dmem_areas: mutable darray[i64]
	area_count: mutable u32

`

// POSITIVE: the canonical field-array shift loop. The store target is a struct-field array path
// (`self.xs[j]`), and the measure reads a struct field count (`self.xs.count`). The store mutates no
// scalar and cannot touch `self.xs.count`, so the scalar part `j + 1` of the measure still proves
// strictly decreasing. Mirrors the real emulator field-shift shape — no `can Unsafe.AssumeProgress`.
func TestLoopDecreasesFieldArrayShiftCountMeasureProves(t *testing.T) {
	src := fieldShiftStructPreamble + `def shift(self: mutable Buf&):
    j: mutable usize = 0
    while j + 1 < self.xs.count:
        decreases self.xs.count - (j + 1)
        self.xs[j] <- self.xs[j + 1]
        j <- j + 1
`
	result := analyzeContractStrict(t, "field_shift.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") || strings.Contains(e, "could not be proven") {
			t.Fatalf("field-array shift with a field-count measure must prove termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected field-array-shift loop termination proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// POSITIVE: the emulator `dmem_areas` shape, combined with increment 2's numeric-cast support: the
// measure subtracts a counter from a `.usize()`-converted field count, and the body shifts a
// struct-field array. The store is discarded; the measure's scalar `j` decreases each step.
func TestLoopDecreasesFieldAreaShiftCastMeasureProves(t *testing.T) {
	src := areaShiftStructPreamble + `def compact(self: mutable Space&):
    j: mutable usize = 0
    while j + 1 < self.area_count.usize():
        decreases self.area_count.usize() - j
        self.dmem_areas[j] <- self.dmem_areas[j + 1]
        j <- j + 1
`
	result := analyzeContractStrict(t, "area_shift.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") || strings.Contains(e, "could not be proven") {
			t.Fatalf("field-area shift with a cast field-count measure must prove termination, got: %v", e)
		}
	}
	if got := countLoopTerminationProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected field-area-shift loop termination proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// NEGATIVE / SOUNDNESS: a `.push` to the field array is a CALL — it can resize `self.xs`, so the
// field-count measure is NOT stable. The body stays unmodelable (calls are rejected anywhere) and the
// loop is not proven; under strict it errors (still needs a hatch).
func TestLoopDecreasesFieldArrayPushStrictErrors(t *testing.T) {
	src := fieldShiftStructPreamble + `def grow(self: mutable Buf&, v: i64):
    j: mutable usize = 0
    while j + 1 < self.xs.count:
        decreases self.xs.count - (j + 1)
        self.xs.push(v)
        j <- j + 1
`
	result := analyzeContractStrict(t, "field_push.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a `.push` (call) to the field array must keep the loop unprovable under strict, got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a field-TARGET scalar write (`self.k <- self.k + 1`) as the only progress must
// NOT prove — a field write is a non-Ident assignment target, which capture rejects outright (it would
// also defeat the stability argument for any field-read measure). Under strict it errors.
func TestLoopDecreasesFieldTargetProgressStrictErrors(t *testing.T) {
	src := `struct Ctr:
	k: mutable i64
	n: mutable i64

def run(self: mutable Ctr&):
    while self.k < self.n:
        decreases self.n - self.k
        self.k <- self.k + 1
`
	result := analyzeContractStrict(t, "field_target_progress.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a field-target scalar progress write must stay unmodelable under strict, got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: two stores to the SAME field array break the single-store / simultaneous-
// substitution invariant; capture rejects the second store (keyed by the normalized field path), so
// the body is unmodelable and the loop is not proven — strict error.
func TestLoopDecreasesFieldArrayDoubleStoreStrictErrors(t *testing.T) {
	src := fieldShiftStructPreamble + `def doubleshift(self: mutable Buf&):
    j: mutable usize = 0
    while j + 2 < self.xs.count:
        decreases self.xs.count - (j + 1)
        self.xs[j] <- self.xs[j + 1]
        self.xs[j + 1] <- self.xs[j + 2]
        j <- j + 1
`
	result := analyzeContractStrict(t, "field_double_store.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("two stores to the same field array must keep the loop unprovable under strict, got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a CONDITIONAL progress step inside the field-shift loop whose guard is NOT
// implied by the loop condition leaves the implicit no-op `else` FEASIBLE — on that path the measure
// does not decrease, so guard-`if` path modeling must REJECT the loop (it may spin forever when the
// guard is false). The guard here (`j + 2 < self.xs.count`) can be false while the loop condition
// (`j + 1 < self.xs.count`) still holds, so the no-op fall-through is a real non-decreasing path.
func TestLoopDecreasesFieldConditionalProgressStrictErrors(t *testing.T) {
	src := fieldShiftStructPreamble + `def cond(self: mutable Buf&):
    j: mutable usize = 0
    while j + 1 < self.xs.count:
        decreases self.xs.count - (j + 1)
        self.xs[j] <- self.xs[j + 1]
        if j + 2 < self.xs.count:
            j <- j + 1
`
	result := analyzeContractStrict(t, "field_conditional.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a conditional progress step whose guard is weaker than the loop condition must stay unprovable under strict (the no-op else is feasible), got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a measure reading a field array whose body could REALLOC it — here a whole
// field reassignment `self.xs <- other` appears in the body. That is a non-Ident assignment TARGET, so
// capture rejects it; the field-count measure can no longer be assumed stable and the loop is not
// proven — strict error. (This is the exact guarantee whose loss would collapse field-read soundness.)
func TestLoopDecreasesFieldArrayWholeReassignStrictErrors(t *testing.T) {
	src := fieldShiftStructPreamble + `def realloc(self: mutable Buf&, other: darray[i64]):
    j: mutable usize = 0
    while j + 1 < self.xs.count:
        decreases self.xs.count - (j + 1)
        self.xs[j] <- self.xs[j + 1]
        self.xs <- other
        j <- j + 1
`
	result := analyzeContractStrict(t, "field_realloc.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("a whole field-array reassignment must keep the field-count measure unprovable under strict, got: %v", result.Errors())
	}
}

// NEGATIVE / SOUNDNESS: a non-decreasing field-shift loop (counter goes the wrong way) must still be
// refuted — the field-count measure does not strictly decrease, so it declines to the runtime check
// (no acceptance change), exactly like the bare-array non-terminating case.
func TestLoopDecreasesFieldArrayNonTerminatingRejected(t *testing.T) {
	src := `struct IBuf:
	xs: mutable darray[i64]
	count: mutable i64

def nonterm(self: mutable IBuf&):
    j: mutable i64 = 0
    while j < self.xs.count:
        decreases self.xs.count - j
        self.xs[j] <- self.xs[j]
        j <- j - 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_nonterm.elisa", src, AnalyzeOptions{EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a non-decreasing field-shift loop should decline without changing acceptance, got: %v", errs)
	}
	if got := countLoopTerminationProof(result, ProofRuntime); got != 1 {
		t.Fatalf("expected non-decreasing field-shift loop to fall back to runtime, got %d: %+v", got, result.ProofReport)
	}
}
