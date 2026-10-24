package semantic

import (
	"strings"
	"testing"
)

// An annotation before a file's first declaration must not fall outside the
// compared source spans, or a replay can silently keep the unannotated type.
func TestRetainedLeadingAnnotationCannotDisappear(t *testing.T) {
	const base = "struct Pair:\n    value: i64\n\ndef get(value: Pair) -> i64:\n    return value.value\n"
	for _, prefix := range []string{"", "def before() -> i64:\n    return 1\n\n", "#line 1 \"included.elisa\"\n"} {
		old := prefix + base
		_, state := AnalyzeRetained(retainedParse(t, old), retainedParse(t, old), []byte(old), AnalyzeOptions{})
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		source := prefix + "@align(64)\n" + base
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			continue
		} // a conservative fallback is sound
		if len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		var pair *StructType
		for name, typ := range state.a.namedTypes {
			if strings.HasSuffix(name, "Pair") {
				pair, _ = typ.(*StructType)
			}
		}
		if pair == nil || !pair.HasAlignment || pair.Alignment != 64 {
			t.Fatal("leading annotation edit silently reused old alignment")
		}
	}
}
