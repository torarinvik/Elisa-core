package semantic

import "testing"

func TestStorePayloadRecursiveAllStores(t *testing.T) {
	const source = `global region source
struct State:
    safe: darray[sview @source]
    borrowed: darray[sview]
struct Table:
    names: mutable darray[sview] = []
    headers: mutable darray[darray[sview @source]] = []
def safe_a(state: State&, table: mutable Table&, depth: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        safe_b(state, table, depth - 1) if depth > 0
        table.names.push(state.safe[0])
def safe_b(state: State&, table: mutable Table&, depth: usize) -> void:
    safe_a(state, table, depth)
def bad_a(state: State&, table: mutable Table&, depth: usize) -> void:
    bad_b(state, table, depth)
def bad_b(state: State&, table: mutable Table&, depth: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        bad_a(state, table, depth - 1) if depth > 0
        table.names.push(state.borrowed[0])
def late_bad(state: State&, table: mutable Table&, depth: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        diamond_left(state, table, depth)
        diamond_right(state, table, depth)
        table.names.push(state.borrowed[0])
def diamond_left(state: State&, table: mutable Table&, depth: usize) -> void:
    late_bad(state, table, depth - 1) if depth > 0
def diamond_right(state: State&, table: mutable Table&, depth: usize) -> void:
    diamond_left(state, table, depth)
def header_a(state: State&, table: mutable Table&, depth: usize) -> void:
    header_b(state, table, depth)
def header_b(state: State&, table: mutable Table&, depth: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        header_a(state, table, depth - 1) if depth > 0
        table.headers.push(state.safe)
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "recursive_payload.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		for _, tc := range []struct {
			name string
			safe bool
		}{{"safe_a", true}, {"safe_b", true}, {"bad_a", false}, {"bad_b", false}, {"header_a", false}, {"header_b", false}, {"late_bad", false}, {"diamond_left", false}, {"diamond_right", false}} {
			fn := storeScanFindFunc(t, result, tc.name)
			got := a.calleeStoresProgramLivedElements(fn, 0, 1, map[storeScanKey]bool{})
			if tc.safe && a.calleeStoreModeInto(fn, 0, 1) == storeNone {
				continue
			}
			if got != tc.safe {
				t.Errorf("%s cold=%v proof=%v want=%v", tc.name, cold, got, tc.safe)
			}
			if !tc.safe && a.calleeStoresProgramLivedElements(fn, 0, 1, map[storeScanKey]bool{{fn, 0, 1}: true}) {
				t.Errorf("%s cold=%v mode traversal marker bypassed payload validation", tc.name, cold)
			}
		}
	}
}
