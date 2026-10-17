//go:build cgo

package semantic

import "testing"

func TestSMTEmptyViewEqualityUsesLength(t *testing.T) {
	for _, expression := range []string{`view == ""`, `"" == view`, `not (view != "")`} {
		source := `def empty(view: sview) -> bool:
    ensure result == (view.len == 0)
    return ` + expression + `
`
		result := analyzeContractStrict(t, "empty_view.elisa", source)
		if len(result.Errors()) != 0 {
			t.Fatalf("%s: %v", expression, result.Errors())
		}
	}
}

func TestSMTIndexedEmptyViewEquality(t *testing.T) {
	source := `global region source
def empty(values: darray[sview @source]&, index: usize) -> bool:
    requires index < values.count
    ensure result == (values[index].len == 0)
    return values[index] == ""
`
	result := analyzeContractStrict(t, "indexed_empty_view.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
}

func TestSMTEmptyViewEqualityRejectsWrongClaim(t *testing.T) {
	source := `def wrong(view: sview) -> bool:
    ensure result == (view.len == 1)
    return view == ""
`
	result := analyzeContractStrict(t, "wrong_empty_view.elisa", source)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatal("wrong length must not prove")
	}
}

func TestSMTEmptyViewEqualityDoesNotReuseHeaderBeforeMutation(t *testing.T) {
	source := `def wrong(view: mutable sview&) -> bool:
    ensure result == (view == "")
    before: bool = view == ""
    view <- "changed"
    return before
`
	result := analyzeContractStrict(t, "empty_view_mutation.elisa", source)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatalf("stale header must not prove: %v", result.Errors())
	}
}

func TestSMTEmptyViewEqualityAgreesWithStringDispatch(t *testing.T) {
	for _, join := range []string{"or", "and"} {
		source := `def special(view: sview) -> bool:
    ensure result == (view == "" ` + join + ` view == "sp")
    return when view:
        "" | "sp" -> true
        _ -> false
`
		result := analyzeContractStrict(t, "empty_view_dispatch.elisa", source)
		if got := ensureFailuresFor(result.Errors(), "special") > 0; got != (join == "and") {
			t.Fatalf("%s: %v", join, result.Errors())
		}
	}
}
