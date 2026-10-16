//go:build cgo

package semantic

import "testing"

// A string-literal classifier whose arms are conditional expressions (`"128" if negative else
// "127"`) discharges literal-equality ensures; a false variant stays unproven.
func TestSMTStringTernaryClassifierEnsures(t *testing.T) {
	proven := `
def limit_text(target: sview, negative: bool) -> sview:
    ensure (result == "128") == (target == "i8" and negative)
    ensure (result == "127") == (target == "i8" and not negative)
    ensure (result == "") == (target != "i8")
    return when target:
        "i8" -> "128" if negative else "127"
        _ -> ""
`
	result := analyzeContractStrict(t, "limit_text.elisa", proven)
	if n := ensureFailuresFor(result.Errors(), "limit_text"); n != 0 {
		t.Fatalf("expected the ternary classifier ensures to prove, got: %v", result.Errors())
	}
	falseVariant := `
def limit_text_false(target: sview, negative: bool) -> sview:
    ensure (result == "128") == (target == "i8")
    return when target:
        "i8" -> "128" if negative else "127"
        _ -> ""
`
	result = analyzeContractStrict(t, "limit_text_false.elisa", falseVariant)
	if ensureFailuresFor(result.Errors(), "limit_text_false") == 0 {
		t.Fatalf("the ensure is false when not negative and must stay unproven; got: %v", result.Errors())
	}
}
