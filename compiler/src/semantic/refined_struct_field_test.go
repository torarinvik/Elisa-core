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
