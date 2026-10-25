package semantic

import (
	"strings"
	"testing"
)

func TestPermissionInventoryFreshAfterRestoredMembershipAndSummaryEdits(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = `struct BorrowedBox:
    value: i64&
global root: i64 = 2

def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}

def relay(value: i64&) -> BorrowedBox:
    return pack(value)

def escapes_local() -> BorrowedBox:
    local: i64 = 3
    return relay(&local)
`
	changed := strings.Replace(before, "BorrowedBox{value: value}", "BorrowedBox{value: &root}", 1)
	spare := "\ndef spare() -> i64:\n    return 7\n"
	initial, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if !strings.Contains(retainedDiagnostics(initial), "function-local storage") {
		t.Fatal("fixture did not exercise transitive borrow diagnostic")
	}
	for step, source := range []string{changed, changed + spare, changed, before, changed} {
		blob, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(blob)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		assertRetainedLiveResultParity(t, cold, warm)
		for name, symbol := range cold.GlobalScope.Symbols {
			want, ok := symbol.Type.(*FuncType)
			if !ok {
				continue
			}
			actual := warm.GlobalScope.Symbols[name]
			if actual == nil {
				t.Fatalf("step %d missing callable %s", step, name)
			}
			got, ok := actual.Type.(*FuncType)
			if !ok || !snapshotPermissionRow(want).same(got) {
				t.Fatalf("step %d full permission row mismatch for %s", step, name)
			}
		}
		if (step == 0 || step >= 3) && len(state.perms.summaryReanalyzed) == 0 {
			t.Fatalf("step %d did not exercise nested summary replay", step)
		}
	}
}
