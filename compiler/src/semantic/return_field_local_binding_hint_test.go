//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// TestReturnFieldLocalBindingHint_FieldWithTypeRangeProves checks the positive case:
// returning a field directly whose PRIMITIVE TYPE RANGE would prove the law for a local emits the hint.
//
// Pattern: `return d.low` where `low: u8` and the law is `NonNeg` (self >= 0).
// A bare `u8` local `r := d.low` would discharge NonNeg statically (u8 range is [0,255] ⊇ >= 0).
// The hint fires because the current `return d.low` path falls to ProofRuntime (the flow tier
// only handles bare idents, so a FieldExpr subject bypasses it).
func TestReturnFieldLocalBindingHint_FieldWithTypeRangeProves(t *testing.T) {
	src := `
law NonNeg(self: u8) = self >= 0
struct Packet:
    low: u8
def get_low(p: Packet&) -> u8 is NonNeg:
    return p.low
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_hint_pos.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "binding") || !strings.Contains(diags, "local") {
		t.Fatalf("expected local-binding hint when field primitive type range proves the law, got diagnostics:\n%s", diags)
	}
	if !strings.Contains(diags, "NonNeg") {
		t.Fatalf("hint should name the refinement predicate, got:\n%s", diags)
	}
}

// TestReturnFieldLocalBindingHint_NoHintWhenProvenStatically ensures NO hint fires when the
// return is already proven statically (via exprRefinementSchemeEntails / field declared refinement).
// This is the no-false-positive gate: a field with the right declared refinement is already proven,
// so no hint should appear.
func TestReturnFieldLocalBindingHint_NoHintWhenProvenStatically(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sop2:
    sdst: mutable u32 is UB[0, 127]
def dest(d: Sop2&) -> u32 is UB[0, 127]:
    return d.sdst
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_hint_already_proven.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning a matching field refinement should prove statically, got errors: %v", errs)
	}
	// No runtime-check hint should appear when the return is already proven.
	diags := allDiagnostics(result)
	if strings.Contains(diags, "binding") && strings.Contains(diags, "local") && strings.Contains(diags, "UB") {
		t.Fatalf("no local-binding hint should fire when the return is already statically proven, got:\n%s", diags)
	}
}

// TestReturnFieldLocalBindingHint_NoHintWhenTypeRangeCannotProve is the no-false-positive case
// where the field type range is too wide to prove the law. u16 field but law requires value <= 100;
// u16 range [0, 65535] does NOT entail <= 100, so no hint.
func TestReturnFieldLocalBindingHint_NoHintWhenTypeRangeCannotProve(t *testing.T) {
	src := `
law Small(self: i64, hi: i64) = self <= hi
struct S:
    val: u16
def get_small(s: S&) -> i64 is Small[100]:
    return s.val.i64()
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_hint_no_prove.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	// The hint must NOT fire: u16 range [0, 65535] does not entail self <= 100.
	if strings.Contains(diags, "binding") && strings.Contains(diags, "local") && strings.Contains(diags, "Small") {
		t.Fatalf("local-binding hint must not fire when the type range cannot prove the law, got:\n%s", diags)
	}
}

// TestReturnFieldLocalBindingHint_FieldWithRefinementRangeProves checks that a field whose
// DECLARED REFINEMENT (not just primitive width) provides a range that proves the return law
// also triggers the hint — i.e. `InRange[0,127]` field proved to satisfy a `Nat` return.
//
// Note: the declared-refinement path is only reached when the refinement-scheme entailment
// (exact or interval) has already failed. Here the field is `InRange[0,127]` and the return
// requires `Nat` (self >= 0): the interval entailment check WOULD catch this
// (InRange[0,127] interval [0,127] entails Nat >= 0), so this test confirms no-double-lint.
func TestReturnFieldLocalBindingHint_FieldRefinementAlreadyCoveredByScheme(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
struct S:
    x: mutable i64 is InRange[0, 127]
def get_nat(s: S&) -> i64 is Nat:
    return s.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_hint_scheme.elisa", src, AnalyzeOptions{})
	// InRange[0,127] entails Nat via refinement interval inclusion → no runtime check, no hint.
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("InRange[0,127] field should entail Nat statically, got errors: %v", errs)
	}
	diags := allDiagnostics(result)
	if strings.Contains(diags, "runtime check") && strings.Contains(diags, "binding") {
		t.Fatalf("no hint expected when refinement scheme entailment already covers it, got:\n%s", diags)
	}
}
