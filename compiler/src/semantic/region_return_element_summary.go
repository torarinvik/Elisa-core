package semantic

import (
	"maps"
	"reflect"
	"slices"

	"elisacore/src/ast"
)

// Return-element summaries: the element provenance of the container a function returns, in
// terms of its own parameters, so a caller's `xs: darray[T] = f(args)` keeps element tracking
// instead of assuming the elements die with the caller's frame.
//
// A summary exists only when EVERY return statement of the (non-generic) function returns an
// element-tracked local container, `[]` or a call to an already-summarized function, and each
// such container's elements depend on nothing
// but parameters: no local or ambient region, no packed store. Anything else leaves the function
// without a summary and the caller falls back to the container's own region.

// noteReturnElementState records at `return X` the element provenance of the returned
// container. A return it cannot describe is marked, which makes the function's summary unknown.
func (a *Analyzer) noteReturnElementState(n *ast.ReturnStmt) {
	if n == nil || n.Value == nil {
		return
	}
	if a.returnElementStmtStates == nil {
		a.returnElementStmtStates = map[*ast.ReturnStmt]regionRefState{}
		a.returnElementStmtUnknown = map[*ast.ReturnStmt]bool{}
	}
	state, ok := a.returnedContainerElementState(n.Value)
	if !ok {
		a.returnElementStmtUnknown[n] = true
		return
	}
	if prior, seen := a.returnElementStmtStates[n]; seen {
		merged, mergedOK := mergeReturnElementStates(prior, state)
		if !mergedOK {
			a.returnElementStmtUnknown[n] = true
			return
		}
		state = merged
	}
	a.returnElementStmtStates[n] = state
}

func (a *Analyzer) returnedContainerElementState(value ast.Expr) (regionRefState, bool) {
	switch e := stripParenExpr(value).(type) {
	case *ast.ListLitExpr:
		if len(e.Elems) == 0 && !e.Brace && e.Owner == nil && e.Keys == nil {
			return regionRefState{}, true
		}
	case *ast.Ident:
		if a.currentElementStates == nil || a.currentScope == nil {
			return regionRefState{}, false
		}
		sym, found := a.currentScope.Lookup(e.Name)
		if !found || sym == nil {
			return regionRefState{}, false
		}
		state, tracked := a.currentElementStates[sym]
		if !tracked || !regionRefStateOnlyParamDeps(state, map[uintptr]bool{}) {
			return regionRefState{}, false
		}
		return cloneRegionRefState(state), true
	case *ast.CallExpr:
		// `return g(args)`: g's own summary, instantiated against our arguments, as long as it
		// still names nothing but our parameters.
		state, ok := a.callReturnedElementState(e)
		if !ok || !regionRefStateOnlyParamDeps(state, map[uintptr]bool{}) {
			return regionRefState{}, false
		}
		return state, true
	}
	return regionRefState{}, false
}

// regionRefStateOnlyParamDeps reports whether a provenance state depends on parameters alone.
func regionRefStateOnlyParamDeps(state regionRefState, seen map[uintptr]bool) bool {
	if len(state.Deps) != 0 || len(state.StoreDeps) != 0 {
		return false
	}
	if len(state.Fields) == 0 {
		return true
	}
	if id := regionRefFieldsIdentity(state.Fields); id != 0 {
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	for _, field := range state.Fields {
		if !regionRefStateOnlyParamDeps(field, seen) {
			return false
		}
	}
	return true
}

// regionRefStateParamIndices collects every parameter index a state depends on, fields included.
func regionRefStateParamIndices(state regionRefState, out map[int]bool, seen map[uintptr]bool) {
	forEachRegionParamDep(state, func(index int) { out[index] = true })
	if len(state.Fields) == 0 {
		return
	}
	if id := regionRefFieldsIdentity(state.Fields); id != 0 {
		if seen[id] {
			return
		}
		seen[id] = true
	}
	for _, field := range state.Fields {
		regionRefStateParamIndices(field, out, seen)
	}
}

// finishReturnElementSummary publishes fn's summary once its body has been checked.
func (a *Analyzer) finishReturnElementSummary(fn *ast.FuncDecl) {
	if fn == nil || len(fn.TypeParams) != 0 || len(fn.GenericParams) != 0 {
		return
	}
	// A summary assumed for a recursive function is dropped unless the body still supports it.
	delete(a.returnElementSummaries, fn)
	var returns []*ast.ReturnStmt
	returnElementWalk(reflect.ValueOf(fn.Body), func(ret *ast.ReturnStmt) {
		returns = append(returns, ret)
	}, map[uintptr]bool{})
	if len(returns) == 0 {
		return
	}
	summary := regionRefState{}
	for _, ret := range returns {
		state, recorded := a.returnElementStmtStates[ret]
		if !recorded || a.returnElementStmtUnknown[ret] {
			return
		}
		merged, ok := mergeReturnElementStates(summary, state)
		if !ok {
			return
		}
		summary = merged
	}
	if a.returnElementSummaries == nil {
		a.returnElementSummaries = map[*ast.FuncDecl]regionRefState{}
	}
	a.returnElementSummaries[fn] = summary
}

// returnElementWalk visits the return statements of a function body, not those of lambdas or
// nested functions (which return from themselves).
func returnElementWalk(v reflect.Value, visit func(*ast.ReturnStmt), seen map[uintptr]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !v.CanInterface() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		switch n := v.Interface().(type) {
		case *ast.LambdaExpr, *ast.FuncDecl:
			return
		case *ast.ReturnStmt:
			visit(n)
		}
		returnElementWalk(v.Elem(), visit, seen)
	case reflect.Interface:
		if !v.IsNil() {
			returnElementWalk(v.Elem(), visit, seen)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				returnElementWalk(v.Field(i), visit, seen)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			returnElementWalk(v.Index(i), visit, seen)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			returnElementWalk(iter.Value(), visit, seen)
		}
	}
}

// callReturnedElementState instantiates a direct call's return-element summary against its
// arguments: each parameter the summary names contributes its argument's provenance and, for an
// element-tracked local container, that container's element provenance.
func (a *Analyzer) callReturnedElementState(call *ast.CallExpr) (regionRefState, bool) {
	if call == nil || call.SafeReceiver != nil || call.Safe || call.HasArgForward || len(call.ArgNames) != 0 {
		return regionRefState{}, false
	}
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil {
		return regionRefState{}, false
	}
	a.ensureProvisionalSummaries(decl)
	summary, known := a.returnElementSummaries[decl]
	if !known || len(call.Args) != len(decl.Params) {
		return regionRefState{}, false
	}
	if sym := a.funcDeclSymbols[decl]; sym != nil {
		if fnType, ok := sym.Type.(*FuncType); !ok || fnType == nil || fnType.Variadic {
			return regionRefState{}, false
		}
	} else {
		return regionRefState{}, false
	}
	if !hasRegionProvenance(summary) {
		return regionRefState{}, true
	}
	indices := map[int]bool{}
	regionRefStateParamIndices(summary, indices, map[uintptr]bool{})
	states := []regionRefState{}
	for _, index := range slices.Sorted(maps.Keys(indices)) { // map: sort — the merge below is order-sensitive
		if index < 0 || index >= len(call.Args) {
			return regionRefState{}, false
		}
		ident, isIdent := stripParenExpr(call.Args[index]).(*ast.Ident)
		if !isIdent || a.currentScope == nil {
			continue
		}
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil {
			if elems, tracked := a.currentElementStates[sym]; tracked && hasRegionProvenance(elems) {
				states = append(states, elems)
			}
		}
	}
	if instantiated, ok := a.instantiateReturnProvenance(summary, call.Args); ok {
		states = append(states, instantiated)
	}
	merged, ok := mergeRegionRefStates(states...)
	if !ok && len(states) != 0 {
		return regionRefState{}, false
	}
	return merged, true
}

// mergeReturnElementStates merges two element states of returned containers. Two states that
// carry no provenance (an empty container, `[]`) merge to the empty state: a return with no
// elements contributes no provenance, and mergeRegionRefStates reports that as a failure.
func mergeReturnElementStates(left, right regionRefState) (regionRefState, bool) {
	if !hasRegionProvenance(left) && !hasRegionProvenance(right) {
		return regionRefState{}, true
	}
	return mergeRegionRefStates(left, right)
}
