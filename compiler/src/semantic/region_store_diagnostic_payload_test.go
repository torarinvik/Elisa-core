package semantic

import "testing"

func TestStoreProgramLivedDiagnosticPayload(t *testing.T) {
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "diagnostic_payload.elisa", `global region source
struct Plain:
    name: sview @source
struct Diagnostic:
    name: sview @source
    detail: sview = ""
struct State:
    names: darray[sview @source]
    unrelated: darray[sview]
struct Table:
    plain: mutable darray[Plain] = []
    diagnostics: mutable darray[Diagnostic] = []
def plain_thread(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.plain <- table.plain.push(Plain{name: state.names[0]})
def default_thread(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: state.names[0]})
def default_direct(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.diagnostics.push(Diagnostic{name: state.names[0]})
def guarded_source_local(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        name: sview @source = state.names[0] if state.unrelated.count > 0 else ""
        table.diagnostics <- table.diagnostics.push(Diagnostic{name: name}) if name != ""
def mutable_source_local(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        name: mutable sview @source = state.names[0]
        table.diagnostics.push(Diagnostic{name: name})
`, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		t.Logf("diagnostics: %v", result.Errors())
		for _, name := range []string{"plain_thread", "default_thread", "default_direct", "mutable_source_local", "guarded_source_local"} {
			fn := storeScanFindFunc(t, result, name)
			if name == "mutable_source_local" {
				t.Logf("local source view recognized=%v", a.storeScanLocalIsProgramView(fn, "name"))
			}
			mode := a.calleeStoreModeInto(fn, 1, 0)
			proof := a.calleeStoresProgramLivedElements(fn, 1, 0, map[storeScanKey]bool{})
			t.Logf("%s cold=%v mode=%d proof=%v", name, cold, mode, proof)
			if mode != storeNone && !proof {
				t.Errorf("%s: static default/source text is rejected", name)
			}
		}
	}
}
