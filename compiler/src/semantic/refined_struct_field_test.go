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

// --- darray/array element refinement (docs/85 element-read channel) --------------------------------

// Reading an element of a darray whose element type is refined inherits the element's refinement.
// `xs: darray[u32 is UB[0,127]]`, then `e = xs[i]` gives `e` the UB[0,127] guarantee, so returning
// `e` from a `-> u32 is UB[0,127]` function discharges with no runtime check.
// Sound: element refinements are enforced on every push/store (parallel to struct-field refinement
// enforcement at construction), so reading xs[i] can safely inherit the bound.
func TestDArrayElemRefinementInherited(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def f(xs: darray[u32 is UB[0, 127]]&, i: usize) -> u32 is UB[0, 127]:
    return xs[i]
`
	if errs := analyzeContractStrict(t, "darray_elem_refine_ok.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("reading a refined darray element should inherit UB[0,127] and prove the return, got: %v", errs)
	}
}

// Static array: same guarantee — element type refinement is enforced at construction, so reading
// arr[i] inherits it.
func TestArrayElemRefinementInherited(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def f(xs: array[u32 is UB[0, 127], 8]&, i: usize) -> u32 is UB[0, 127]:
    return xs[i]
`
	if errs := analyzeContractStrict(t, "array_elem_refine_ok.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("reading a refined array element should inherit UB[0,127] and prove the return, got: %v", errs)
	}
}

// SOUNDNESS: a darray with a NON-refined element type gives no bound. Returning xs[i] as a
// `u32 is UB[0,127]` must decline (runtime check or error under -strict).
func TestDArrayUnrefinedElemGivesNoBound(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def f(xs: darray[u32]&, i: usize) -> u32 is UB[0, 127]:
    return xs[i]
`
	result := analyzeContractStrict(t, "darray_elem_unrefined.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("an unrefined darray element must NOT prove UB[0,127]; return should be declined under -strict")
	}
}

// SOUNDNESS: a wider element refinement does NOT entail a narrower return refinement.
// darray[u32 is UB[0,1000]] does not prove the return UB[0,127] — the element might be 500.
func TestDArrayWiderElemDoesNotProveNarrower(t *testing.T) {
	src := `
law UB(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def f(xs: darray[u32 is UB[0, 1000]]&, i: usize) -> u32 is UB[0, 127]:
    return xs[i]
`
	result := analyzeContractStrict(t, "darray_elem_wide.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("UB[0,1000] element does not entail UB[0,127] return; must decline under -strict")
	}
}
