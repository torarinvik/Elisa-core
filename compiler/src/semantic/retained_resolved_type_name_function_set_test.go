//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestRetainedTypeNameOwnersFollowAddedEditedRemovedFunctionAcrossDisk(t *testing.T) {
	const baseline = `module Sample:
    public:
        struct Box:
            value: i32

        def kept() -> i32:
            return 0

def main() -> i32:
    return Sample::kept()
`
	added := strings.Replace(baseline, "\ndef main()", "\n        def extra() -> i32:\n            local: Box = Box{value: 7}\n            return local.value\n\ndef main()", 1)
	edited := strings.Replace(added, "value: 7", "value: 9", 1)
	_, state := AnalyzeRetained(retainedParse(t, baseline), retainedParse(t, baseline), []byte(baseline), AnalyzeOptions{})
	if state == nil {
		t.Fatal("baseline was not retained")
	}
	for index, source := range []string{added, edited, baseline} {
		oldRows := resolvedTypeNameBodyRowsForTest(state, "extra")
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("function-set step %d fell back: %v", index, err)
		}
		for _, expr := range oldRows {
			if _, present := state.a.resolvedTypeNames[expr]; present {
				t.Fatalf("step %d retained an old function-body type-name key", index)
			}
		}
		assertResolvedTypeNameParity(t, updated, AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{}))
		if index < 2 && len(resolvedTypeNameBodyRowsForTest(state, "extra")) == 0 {
			t.Fatalf("step %d failed to create a qualified body type-name fact", index)
		}
		checkpoint, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
	}
}
