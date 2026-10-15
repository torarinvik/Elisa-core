package semantic

import "elisacore/src/ast"

// applyDeclaredReturnRegion transfers the parser's `-> T @r` contract onto the
// function summary. The return type remains nominally T; the lifetime is
// represented by return provenance tied to the explicit parameter whose type
// carries @r. This is the same distinction used by stage1's side-table
// __region_return annotation and avoids making recursive enum types distinct by
// region merely to express a lifetime.
func (a *Analyzer) applyDeclaredReturnRegion(fn *ast.FuncDecl, ft *FuncType) {
	if fn == nil || ft == nil || fn.ReturnRegion == "" {
		return
	}
	ft.ReturnRegion = fn.ReturnRegion
	if a.isProgramRegion(fn.ReturnRegion) {
		// docs/128: `-> T @source` names no parameter; the result lives in the program
		// arena. Stamp the region onto the type so the return statement is checked as a
		// store into `source` and callers receive a program-lived value.
		stamped, carriesRegion, conflicts := stampAggregateRegion(ft.Return, fn.ReturnRegion)
		if conflicts || !carriesRegion {
			a.errorf(fn.Pos(), "return region @%s requires a region-carrying return type", fn.ReturnRegion)
			return
		}
		ft.Return = stamped
		return
	}
	if tuple, ok := ft.Return.(*TupleType); ok {
		stamped, carriesRegion, conflicts := stampAggregateRegion(tuple, fn.ReturnRegion)
		if conflicts {
			a.errorf(fn.Pos(), "tuple return region @%s conflicts with a field tied to a different region", fn.ReturnRegion)
		} else if !carriesRegion {
			a.errorf(fn.Pos(), "tuple return region @%s requires at least one region-carrying field", fn.ReturnRegion)
		} else {
			ft.Return = stamped
		}
	}
	for index, param := range ft.Params {
		if regionParamReturnTypeRegion(param) != fn.ReturnRegion {
			continue
		}
		ft.ReturnProvenance = regionRefStateFromParamDependency(index)
		ft.ReturnProvenanceKnown = true
		return
	}
	if !a.lookupRegionParam(fn.ReturnRegion) && !returnRegionListContains(ft.RegionParams, fn.ReturnRegion) {
		a.errorf(fn.Pos(), "return region %q is not declared by the function", fn.ReturnRegion)
	}
}

func returnRegionListContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
