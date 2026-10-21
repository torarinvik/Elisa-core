package semantic

import "testing"

func TestStoreScanLocalContainerProjectionBeforeBodyTyping(t *testing.T) {
	source := `global region source
struct Param:
    name: sview @source
    unrelated: sview
    header: darray[sview @source]
struct State:
    params: darray[Param]
struct Note:
    name: sview @source
struct Table:
    notes: mutable darray[Note] = []
def record(table: mutable Table&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        params: mutable darray[Param] = []
        params.extend(state.params)
        table.notes.push(Note{name: params[0].name})
def retain_other(table: mutable darray[sview]&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        params: mutable darray[Param] = []
        params.extend(state.params)
        table.push(params[0].unrelated)
def retain_header(table: mutable darray[darray[sview @source]]&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        params: mutable darray[Param] = []
        params.extend(state.params)
        table.push(params[0].header)
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_container_projection.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		for _, name := range []string{"record", "retain_other", "retain_header"} {
			fn := storeScanFindFunc(t, result, name)
			mode := a.calleeStoreModeInto(fn, 1, 0)
			t.Logf("cold=%v %s mode=%v", cold, name, mode)
			wantNone := name == "record"
			if (mode == storeNone) != wantNone {
				t.Errorf("cold=%v %s mode=%v want-none=%v", cold, name, mode, wantNone)
			}
		}
	}
}

func TestStoreScanLocalDarrayStructElementRejectsUncertainBindings(t *testing.T) {
	const prefix = `global region source
struct Param:
    name: sview @source
struct Other:
    name: sview
`
	for _, tc := range []struct {
		name, body string
		known      bool
	}{
		{"explicit", "def inspect() -> void:\n    params: mutable darray[Param] = []\n", true},
		{"generic_shadow", "def inspect[Param]() -> void:\n    params: mutable darray[Param] = []\n", false},
		{"parameter", "def inspect(params: darray[Param]) -> void:\n    params: mutable darray[Param] = []\n", false},
		{"non_struct", "def inspect() -> void:\n    params: mutable darray[sview] = []\n", false},
		{"conflicting", "def inspect() -> void:\n    params: mutable darray[Param] = []\n    params: mutable darray[Other] = []\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_container_bindings.elisa", prefix+tc.body, AnalyzeOptions{})
			storeScanUntyped(result)
			fn := storeScanFindFunc(t, result, "inspect")
			_, known := result.analyzer.storeScanLocalDarrayStructElement(fn, "params")
			if known != tc.known {
				t.Fatalf("known=%v want %v", known, tc.known)
			}
			if tc.known {
				a := result.analyzer
				idx := a.storeScanIndex(fn)
				idx.otherBound["params"] = true
				if _, known := a.storeScanLocalDarrayStructElement(fn, "params"); known {
					t.Fatal("a name bound by another construct supplied a local type")
				}
				delete(idx.otherBound, "params")
				original := a.namedTypes["Param"]
				delete(a.namedTypes, "Param")
				a.namedTypes["Unrelated.Param"] = original
				a.storeScanStructIndex = nil
				if _, known := a.storeScanLocalDarrayStructElement(fn, "params"); known {
					t.Fatal("unrelated module suffix supplied a local type")
				}
				a.namedTypes["Param"] = original
				a.namedTypes["OtherModule.Param"] = &StructType{Name: "OtherModule.Param"}
				a.storeScanStructIndex = nil
				if _, known := a.storeScanLocalDarrayStructElement(fn, "params"); known {
					t.Fatal("ambiguous struct name supplied a local type")
				}
			}
		})
	}
}
