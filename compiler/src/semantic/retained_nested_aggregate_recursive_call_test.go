package semantic

import (
	"strings"
	"testing"
)

func TestRetainedNestedAggregateRecursiveCallAcrossDiskRestore(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `struct BorrowedBox:
    value: i64&
global root: i64 = 9

def descend(value: i64&, count: i64) -> i64&:
    if count <= 0:
        return value
    return descend(value, count - 1)

def pack(value: i64&, count: i64) -> BorrowedBox:
    return BorrowedBox{value: descend(value, count)}

def relay(value: i64&, count: i64) -> BorrowedBox:
    return pack(value, count)

def escapes() -> BorrowedBox:
    local: i64 = 1
    return relay(&local, 2)
`
	changed := strings.Replace(source, "        return value\n    return descend", "        return &root\n    return descend", 1)
	if source == changed || len(source) != len(changed) {
		t.Fatal("fixture edit must change the recursive base case without shifting source")
	}
	if !strings.Contains(source, "BorrowedBox{value: descend(value, count)}") {
		t.Fatal("fixture must return an aggregate with a nested recursive call")
	}

	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	for revision, current := range []string{changed, source, changed} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatalf("marshal revision %d: %v", revision, err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatalf("restore revision %d: %v", revision, err)
		}
		warm, err := state.Update(retainedParse(t, current), retainedParse(t, current), []byte(current))
		if err != nil {
			t.Fatalf("update revision %d: %v", revision, err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, current), AnalyzeOptions{})
		wantEscape := current == source
		for label, result := range map[string]*Result{"cold": cold, "retained": warm} {
			gotEscape := strings.Contains(retainedDiagnostics(result), "function-local storage")
			if gotEscape != wantEscape {
				t.Fatalf("%s escape outcome at revision %d = %v, want %v:\n%s", label, revision, gotEscape, wantEscape, retainedDiagnostics(result))
			}
		}
		// This compares all current-AST facts, including the nested CallExpr entry
		// in ExprFacts, after rekeying source nodes across the disk restore.
		t.Logf("nested revision %d", revision)
		logRetainedFunctionTypeDifferences(t, cold, warm)
		logRetainedFunctionAnalysisDifferences(t, cold, warm)
		logRetainedExpressionFactDifferences(t, cold, warm)
		assertRetainedLiveResultParity(t, cold, warm)
	}
}
