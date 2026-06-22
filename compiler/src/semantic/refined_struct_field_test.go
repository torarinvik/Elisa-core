//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A refinement on a STRUCT FIELD must resolve its law even though struct fields are resolved before law
// decls are registered (laws can have struct subjects and structs refined fields — no fixed order, so
// predicate validation is deferred to the post-law pass). Regression: previously reported "is not a law".
func TestRefinedStructFieldResolvesLaw(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sop2:
    op: mutable u32 is UB[0, 127]
def sop2_op(inst: u32) -> u32 is UB[0, 127]:
    return (inst >> 23) & 0x7f
def decode(inst: u32) -> Sop2:
    return Sop2(sop2_op(inst))
`
	result := analyzeContractStrict(t, "refined_field.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "is not a law") {
			t.Fatalf("a refined struct field must resolve its law (deferred past law registration), got: %v", result.Errors())
		}
	}
}

// SOUNDNESS: constructing the struct with an out-of-range value is still rejected (the field refinement
// is enforced at construction, not decorative), and a genuinely-missing law still errors.
func TestRefinedStructFieldEnforcedAndMissingLawErrors(t *testing.T) {
	bad := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sop2:
    op: mutable u32 is UB[0, 127]
def f() -> Sop2:
    return Sop2(999)
`
	errs := strings.Join(analyzeContractStrict(t, "rf_bad.elisa", bad).Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("Sop2(999) violates op's UB[0,127]; construction must be rejected, got: %v", errs)
	}
	missing := `
struct S:
    x: mutable u32 is NotALaw[0, 5]
`
	if !strings.Contains(strings.Join(analyzeContractStrict(t, "rf_missing.elisa", missing).Errors(), "\n"), "is not a law") {
		t.Fatalf("a struct field refinement naming a non-law must still error")
	}
}

// Reading a refined field and returning it as the same refinement discharges by composition (the field
// invariant is enforced at construction). `return d.sdst` where sdst is `is UB[0,127]` and the return
// requires `UB[0,127]` proves with no check; a WIDER field returned as a narrower refinement declines.
func TestReturnRefinedFieldComposes(t *testing.T) {
	ok := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sop2:
    sdst: mutable u32 is UB[0, 127]
def dest(d: Sop2&) -> u32 is UB[0, 127]:
    return d.sdst
`
	if errs := analyzeContractStrict(t, "fld_ok.elisa", ok).Errors(); len(errs) != 0 {
		t.Fatalf("returning a UB[0,127] field as a UB[0,127] return should prove, got: %v", errs)
	}
	bad := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct S:
    w: mutable u32 is UB[0, 1000]
def narrow(s: S&) -> u32 is UB[0, 255]:
    return s.w
`
	if !strings.Contains(strings.Join(analyzeContractStrict(t, "fld_bad.elisa", bad).Errors(), "\n"), "could not be proven") {
		t.Fatalf("a UB[0,1000] field does not entail a UB[0,255] return; must decline")
	}
}

// Routing a refined value THROUGH a struct field at construction (`Dst(s.v)` where the source field
// `s.v` is itself `is UB[0,127]` and the target field `op` requires the same) discharges by composition
// — symmetric to the direct return-by-field path. The source field's invariant is enforced at its own
// construction, so the value already satisfies the target field's refinement, no runtime check needed.
func TestConstructFieldFromRefinedFieldComposes(t *testing.T) {
	ok := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Src:
    v: mutable u32 is UB[0, 127]
struct Dst:
    op: mutable u32 is UB[0, 127]
def copy(s: Src&) -> Dst:
    return Dst(s.v)
`
	if errs := analyzeContractStrict(t, "ctor_fld_ok.elisa", ok).Errors(); len(errs) != 0 {
		t.Fatalf("routing a UB[0,127] field into a UB[0,127] field should prove by composition, got: %v", errs)
	}
}

// SOUNDNESS: a WIDER source field routed into a NARROWER target field does NOT entail it (the source
// value can exceed the target bound), and an out-of-range constant is still rejected at construction.
func TestConstructFieldFromRefinedFieldSoundness(t *testing.T) {
	wider := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Src:
    v: mutable u32 is UB[0, 1000]
struct Dst:
    op: mutable u32 is UB[0, 127]
def copy(s: Src&) -> Dst:
    return Dst(s.v)
`
	if !strings.Contains(strings.Join(analyzeContractStrict(t, "ctor_fld_wide.elisa", wider).Errors(), "\n"), "could not be proven") {
		t.Fatalf("a UB[0,1000] source field does not entail a UB[0,127] target field; must decline")
	}
	badConst := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Dst:
    op: mutable u32 is UB[0, 127]
def bad() -> Dst:
    return Dst(999)
`
	if !strings.Contains(strings.Join(analyzeContractStrict(t, "ctor_fld_const.elisa", badConst).Errors(), "\n"), "is violated") {
		t.Fatalf("Dst(999) violates op's UB[0,127]; construction must be rejected")
	}
}
