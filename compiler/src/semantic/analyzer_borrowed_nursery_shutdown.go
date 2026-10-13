package semantic

import (
	"elisacore/src/ast"
)

type borrowedNurseryShutdownCall struct {
	expr                       *ast.CallExpr
	calleeType                 *FuncType
	originalCalleeType         *FuncType
	appliedType                *FuncType
	permissionBindings         map[string][]ast.PermissionRef
	permissionBindingFunctions map[string][]*FuncType
	permissionParams           map[string]bool
}

func affineValueKeysMatch(left, right affineValueKey) bool {
	return left.Root != nil && left.Root == right.Root && left.Path == right.Path
}

func borrowedPoolScopeMatchesKey(pool *poolScopeState, key affineValueKey) bool {
	if pool == nil || key.Root == nil {
		return false
	}
	if pool.HasBorrowedPoolKey && affineValueKeysMatch(pool.BorrowedPoolKey, key) {
		return true
	}
	return key.Path == "" && (key.Root == pool.BorrowedPool || key.Root == pool.BorrowedAlias)
}

func permissionRefMayShutdownPoolInContext(ref ast.PermissionRef, bindings map[string][]ast.PermissionRef, seen map[string]bool, isPermissionParam func(string) bool) bool {
	if ref.Name == "Pool" && (ref.Member == "" || ref.Member == "Shutdown") {
		return true
	}
	if ref.Member == "" {
		if len(bindings) != 0 {
			if bound, ok := bindings[ref.Name]; ok && (seen == nil || !seen[ref.Name]) {
				if seen == nil {
					seen = map[string]bool{}
				}
				seen[ref.Name] = true
				for _, nested := range bound {
					if permissionRefMayShutdownPoolInContext(nested, bindings, seen, isPermissionParam) {
						delete(seen, ref.Name)
						return true
					}
				}
				delete(seen, ref.Name)
			}
		}
		if isPermissionParam != nil && isPermissionParam(ref.Name) {
			if _, bound := bindings[ref.Name]; !bound {
				return true
			}
			if seen != nil && seen[ref.Name] {
				return true
			}
		}
	}
	for _, via := range ref.Via {
		if permissionRefMayShutdownPoolInContext(via, bindings, seen, isPermissionParam) {
			return true
		}
	}
	return false
}

func rawFunctionPermissionRefs(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	if len(fnType.PermissionRefs) != 0 {
		return fnType.PermissionRefs
	}
	refs := make([]ast.PermissionRef, 0, len(fnType.Permissions))
	for _, family := range fnType.Permissions {
		refs = append(refs, ast.PermissionRef{Name: family})
	}
	return refs
}

func (a *Analyzer) functionMayShutdownPoolWithBindings(fnType *FuncType, bindings map[string][]ast.PermissionRef, permissionParams map[string]bool) bool {
	if a == nil || fnType == nil {
		return false
	}
	containsShutdown := func(sourceType *FuncType, refs []ast.PermissionRef) bool {
		isPermissionParam := func(name string) bool {
			if a.lookupPermissionParam(name) || permissionParams[name] {
				return true
			}
			if sourceType != nil {
				for _, param := range sourceType.PermissionParams {
					if param == name {
						return true
					}
				}
			}
			return false
		}
		for _, ref := range refs {
			if permissionRefMayShutdownPoolInContext(ref, bindings, nil, isPermissionParam) {
				return true
			}
		}
		return false
	}
	if containsShutdown(fnType, rawFunctionPermissionRefs(fnType)) {
		return true
	}
	for _, name := range fnType.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil && containsShutdown(source, rawFunctionPermissionRefs(source)) {
			return true
		}
	}
	if source := a.functionTypes[fnType.Name]; source != nil && containsShutdown(source, rawFunctionPermissionRefs(source)) {
		return true
	}
	for _, name := range fnType.FunctionReturnSourceNames {
		if factory := a.functionTypes[name]; factory != nil && containsShutdown(factory, factory.ReturnedFunctionPermissionRefs) {
			return true
		}
	}
	return false
}

func (a *Analyzer) functionTypeHasInferredPermissionSource(fnType *FuncType) bool {
	if a == nil || a.functionTypes == nil || fnType == nil {
		return false
	}
	hasSource := func(name string) bool {
		return name != "" && a.functionTypes[name] != nil
	}
	if hasSource(fnType.Name) {
		return true
	}
	for _, name := range fnType.MutableGlobalSourceNames {
		if hasSource(name) {
			return true
		}
	}
	for _, name := range fnType.FunctionReturnSourceNames {
		if hasSource(name) {
			return true
		}
	}
	return false
}

// rejectBorrowedNurseryPoolShutdown catches direct shutdown by owner identity and
// helper calls through their propagated Pool.Shutdown effect. The latter matters
// because a helper can free the pool before the generated nursery join runs.
func (a *Analyzer) rejectBorrowedNurseryPoolShutdown(expr *ast.CallExpr, calleeType, appliedType *FuncType, orderedArgs []ast.Expr, permissionBindings map[string][]ast.PermissionRef) {
	if a == nil || expr == nil || calleeType == nil {
		return
	}
	insideBorrowedNursery := false
	for i := len(a.currentPoolScopes) - 1; i >= 0; i-- {
		if pool := &a.currentPoolScopes[i]; pool.Node != nil && pool.Node.BorrowedPool != "" {
			insideBorrowedNursery = true
			break
		}
	}
	if !insideBorrowedNursery {
		return
	}

	if calleeType.Name == "pool_shutdown" {
		if len(orderedArgs) != 0 {
			if key, ok := a.lookupProtocolTargetKey(orderedArgs[0]); ok {
				for i := len(a.currentPoolScopes) - 1; i >= 0; i-- {
					pool := &a.currentPoolScopes[i]
					if pool.Node != nil && pool.Node.BorrowedPool != "" && borrowedPoolScopeMatchesKey(pool, key) {
						a.errorf(expr.Position, "cannot shut down a borrowed nursery pool before its tasks have joined")
						return
					}
				}
				// A direct shutdown of a pool with a proven distinct owner cannot
				// invalidate any active borrowed nursery.
				return
			}
		}
		// If the direct target cannot be identified, reject conservatively.
		a.errorf(expr.Position, "cannot call code that may shut down a pool while a borrowed nursery is active")
		return
	}
	if a.functionMayShutdownPoolWithBindings(calleeType, permissionBindings, nil) || a.functionMayShutdownPoolWithBindings(appliedType, permissionBindings, nil) {
		a.errorf(expr.Position, "cannot call code that may shut down a pool while a borrowed nursery is active")
		return
	}
	bindingFunctions := a.borrowedNurseryPermissionBindingFunctions(calleeType, appliedType, orderedArgs)
	deferredType := calleeType
	if !a.functionTypeHasInferredPermissionSource(deferredType) {
		deferredType = appliedType
	}
	if !a.functionTypeHasInferredPermissionSource(deferredType) && len(bindingFunctions) == 0 {
		return
	}
	// Unannotated helper effects are inferred after body analysis. Retain calls
	// whose current type does not yet reveal shutdown, then check them against
	// the inferred effect row before semantic analysis returns.
	a.deferredBorrowedNurseryShutdownCalls = append(a.deferredBorrowedNurseryShutdownCalls, borrowedNurseryShutdownCall{
		expr:                       expr,
		calleeType:                 deferredType,
		originalCalleeType:         calleeType,
		appliedType:                appliedType,
		permissionBindings:         clonePermissionBindings(permissionBindings),
		permissionBindingFunctions: bindingFunctions,
		permissionParams:           a.permissionParamNamesForDeferredCall(bindingFunctions),
	})
}

func (a *Analyzer) validateDeferredBorrowedNurseryShutdownCalls() {
	if a == nil || len(a.deferredBorrowedNurseryShutdownCalls) == 0 {
		return
	}
	calls := a.deferredBorrowedNurseryShutdownCalls
	a.deferredBorrowedNurseryShutdownCalls = nil
	for _, call := range calls {
		bindings := a.resolvedBorrowedNurseryPermissionBindings(call.permissionBindings, call.permissionBindingFunctions)
		mayShutdown := a.functionMayShutdownPoolWithBindings(call.calleeType, bindings, call.permissionParams)
		if !mayShutdown {
			mayShutdown = a.functionMayShutdownPoolWithBindings(call.originalCalleeType, bindings, call.permissionParams)
		}
		if !mayShutdown {
			mayShutdown = a.functionMayShutdownPoolWithBindings(call.appliedType, bindings, call.permissionParams)
		}
		if mayShutdown {
			a.errorf(call.expr.Position, "cannot call code that may shut down a pool while a borrowed nursery is active")
		}
	}
}

func (a *Analyzer) currentPermissionParamNames() map[string]bool {
	if a == nil || len(a.permissionParamScopes) == 0 {
		return nil
	}
	params := make(map[string]bool)
	for _, scope := range a.permissionParamScopes {
		for name := range scope {
			params[name] = true
		}
	}
	return params
}

func (a *Analyzer) permissionParamNamesForDeferredCall(functions map[string][]*FuncType) map[string]bool {
	params := a.currentPermissionParamNames()
	for _, callbacks := range functions {
		for _, callback := range callbacks {
			if callback == nil {
				continue
			}
			for _, name := range callback.PermissionParams {
				if params == nil {
					params = map[string]bool{}
				}
				params[name] = true
			}
			for _, name := range callback.UsedPermissionParams {
				if params == nil {
					params = map[string]bool{}
				}
				params[name] = true
			}
		}
	}
	return params
}

func (a *Analyzer) borrowedNurseryPermissionBindingFunctions(calleeType, appliedType *FuncType, args []ast.Expr) map[string][]*FuncType {
	if a == nil || calleeType == nil {
		return nil
	}
	sources := map[string][]*FuncType{}
	for i, param := range calleeType.Params {
		paramFunc, ok := param.(*FuncType)
		if !ok {
			continue
		}
		name, ok := funcTypeHasSinglePermissionRowParam(paramFunc)
		if !ok {
			continue
		}
		var callback *FuncType
		if i < len(args) {
			callback, _ = a.exprTypes[args[i]].(*FuncType)
		}
		if callback == nil && appliedType != nil && i < len(appliedType.Params) {
			callback, _ = appliedType.Params[i].(*FuncType)
		}
		if callback != nil {
			sources[name] = append(sources[name], a.cloneFunctionValueType(callback))
		}
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

func (a *Analyzer) resolvedBorrowedNurseryPermissionBindings(bindings map[string][]ast.PermissionRef, functions map[string][]*FuncType) map[string][]ast.PermissionRef {
	if len(functions) == 0 {
		return bindings
	}
	resolved := clonePermissionBindings(bindings)
	if resolved == nil {
		resolved = make(map[string][]ast.PermissionRef, len(functions))
	}
	for name, callbacks := range functions {
		refs := append([]ast.PermissionRef(nil), resolved[name]...)
		for _, callback := range callbacks {
			refs = append(refs, rawFunctionPermissionRefs(callback)...)
			refs = append(refs, a.effectiveFunctionPermissionRefs(callback)...)
		}
		resolved[name] = canonicalizePermissionRefs(refs)
	}
	return resolved
}

func clonePermissionBindings(bindings map[string][]ast.PermissionRef) map[string][]ast.PermissionRef {
	if len(bindings) == 0 {
		return nil
	}
	clone := make(map[string][]ast.PermissionRef, len(bindings))
	for name, refs := range bindings {
		clone[name] = append([]ast.PermissionRef(nil), refs...)
	}
	return clone
}
