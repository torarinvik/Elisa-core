package semantic

import "testing"

func TestStoreScanFilledBooleanBeforeBodyTyping(t *testing.T) {
	source := `struct Snapshot:
    names: mutable darray[sview] = []
    flags: mutable darray[bool] = []
    lists: mutable darray[darray[sview]] = []
def build(names: darray[sview]&) -> Snapshot:
    can Memory.Allocate, Abort.Panic:
        result: mutable Snapshot = Snapshot{}
        for i in 0..<names.count |result|:
            result.names.push(names[i])
            result.flags.push(names.count > 0)
        return result
def build_header(names: darray[sview]&) -> Snapshot:
    can Memory.Allocate:
        result: mutable Snapshot = Snapshot{}
        result.lists.push(names)
        result.flags.push(names.count > 0)
        return result
def capture(result: mutable Snapshot&, names: darray[sview]&) -> bool:
    can Memory.Allocate:
        result.lists.push(names)
        return true
def build_condition_effect(names: darray[sview]&) -> Snapshot:
    can Memory.Allocate:
        result: mutable Snapshot = Snapshot{}
        result.flags.push(capture(&result, names))
        return result
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "filled_boolean.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		a.storeScanCache = nil
		fn := storeScanFindFunc(t, result, "build")
		if got := a.calleeReturnModeOf(fn, 0, map[storeScanKey]bool{}); got != storeViews {
			t.Errorf("cold=%v fresh result retains source header: %v", cold, got)
		}
		for _, name := range []string{"build_header", "build_condition_effect"} {
			fn := storeScanFindFunc(t, result, name)
			if got := a.calleeReturnModeOf(fn, 0, map[storeScanKey]bool{}); got <= storeViews {
				t.Errorf("%s/cold=%v input header retention lost: %v", name, cold, got)
			}
		}
	}
}
