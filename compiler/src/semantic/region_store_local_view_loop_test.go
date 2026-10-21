package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestStoreScanLocalProgramViewLoopBeforeBodyTyping(t *testing.T) {
	const source = `global region source
struct State:
    safe: darray[sview @source]
    borrowed: darray[sview]
    cstrings: darray[cstr @source]
def copy_safe(target: mutable darray[sview]&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview @source] = []
        names.extend(state.safe)
        for name in names:
            target.push(name)
def copy_cstr(target: mutable darray[cstr]&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[cstr @source] = []
        names.extend(state.cstrings)
        for name in names:
            target.push(name)
def copy_borrowed(target: mutable darray[sview]&, state: State&) -> void:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = []
        names.extend(state.borrowed)
        for name in names:
            target.push(name)
`
	for _, cold := range []bool{false, true} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_view_loop.elisa", source, AnalyzeOptions{})
		if cold {
			storeScanUntyped(result)
		}
		a := result.analyzer
		a.currentScope = a.globalScope
		for _, name := range []string{"copy_safe", "copy_cstr", "copy_borrowed"} {
			fn := storeScanFindFunc(t, result, name)
			if name == "copy_safe" {
				elem, known := a.storeScanLoopBinderElemType(fn, &ast.Ident{Name: "name"})
				if !known || elem == nil || !a.storeScanTypeIsProgramLivedForStoreProof(elem) {
					t.Errorf("cold=%v source-view binder type unknown", cold)
				}
			}
			if name != "copy_safe" {
				if elem, known := a.storeScanLoopBinderElemType(fn, &ast.Ident{Name: "name"}); known && elem != nil && a.storeScanTypeIsProgramLivedForStoreProof(elem) {
					t.Errorf("cold=%v borrowed view became program-lived", cold)
				}
			}
			mode := a.calleeStoreModeInto(fn, 1, 0)
			proof := a.calleeStoresProgramLivedElements(fn, 1, 0, map[storeScanKey]bool{})
			retains := mode != storeNone && !proof
			if retains != (name != "copy_safe") {
				t.Errorf("cold=%v %s mode=%v proof=%v", cold, name, mode, proof)
			}
		}
		fn := storeScanFindFunc(t, result, "copy_safe")
		params := fn.RegionParams
		fn.RegionParams = append(append([]string{}, params...), "source")
		if _, known := a.storeScanLocalProgramViewDarrayElement(fn, "names"); known {
			t.Error("generic source region shadow supplied a program view")
		}
		fn.RegionParams = params
		idx := a.storeScanIndex(fn)
		flat := idx.flat
		idx.flat = append(append([]any{}, flat...), &ast.RegionStmt{Name: "source"})
		if _, known := a.storeScanLocalProgramViewDarrayElement(fn, "names"); known {
			t.Error("local source region shadow supplied a program view")
		}
		idx.flat = flat
		idx.otherBound["names"] = true
		if _, known := a.storeScanLocalProgramViewDarrayElement(fn, "names"); known {
			t.Error("shadowed local supplied a program view")
		}
		delete(idx.otherBound, "names")
	}
}
