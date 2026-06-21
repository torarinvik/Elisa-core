package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) trackAffineValueSymbol(sym *Symbol) {
	if sym == nil || !a.containsAffineHandleValues(sym.Type, map[string]bool{}) {
		return
	}
	a.registerLiveProtocolValuePaths(affineValueKey{Root: sym}, sym.Type)
}

func borrowableOwnerRefElemType(t Type) (Type, bool) {
	ref, ok := t.(*RefType)
	if !ok || !isBorrowableAffineOwnerType(ref.Elem) {
		return nil, false
	}
	return ref.Elem, true
}

func (a *Analyzer) containsBorrowedOwnerRefValues(t Type, seen map[string]bool) bool {
	return a.containsBorrowedOwnerRefValuesWithSeen(t, map[Type]bool{}, 0)
}

func (a *Analyzer) containsBorrowedOwnerRefValuesWithSeen(t Type, seen map[Type]bool, depth int) bool {
	if t == nil {
		return false
	}
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("borrowed-owner traversal", semanticTraversalDepthLimit)
		return false
	}
	if _, ok := borrowableOwnerRefElemType(t); ok {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Elem, seen, depth+1)
	case *DArrayType:
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Elem, seen, depth+1)
	case *OptionalType:
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Value, seen, depth+1)
	case *ViewType:
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Elem, seen, depth+1)
	case *DictType:
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Key, seen, depth+1) || a.containsBorrowedOwnerRefValuesWithSeen(tt.Value, seen, depth+1)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.containsBorrowedOwnerRefValuesWithSeen(field.Type, seen, depth+1) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.containsBorrowedOwnerRefValuesWithSeen(payloadType, seen, depth+1) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsBorrowedOwnerRefValuesWithSeen(field.Type, seen, depth+1) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsBorrowedOwnerRefValuesWithSeen(payloadType, seen, depth+1) {
					return true
				}
			}
		}
		return false
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsBorrowedOwnerRefValuesWithSeen(field.Type, seen, depth+1) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.containsBorrowedOwnerRefValuesWithSeen(fieldType, seen, depth+1) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsBorrowedOwnerRefValuesWithSeen(arg, seen, depth+1) {
				return true
			}
		}
		return a.containsBorrowedOwnerRefValuesWithSeen(tt.Base, seen, depth+1)
	default:
		return false
	}
}

func (a *Analyzer) recordBorrowedOwnerRefParam(sym *Symbol) {
	if a.currentBorrowedOwnerRefs == nil || sym == nil {
		return
	}
	state, ok := a.abstractParamBorrowedOwnerRefState(sym.Type, affineValueKey{Root: sym}, map[string]bool{})
	if !ok {
		delete(a.currentBorrowedOwnerRefs, sym)
		return
	}
	a.currentBorrowedOwnerRefs[sym] = state
}

// borrowedOwnerRefStateHasRawInteriorAlias reports whether a borrowed-owner state (or any
// nested field/element of it) is a raw interior pointer aliased out of a Pooled handle.
// Such states must be tracked even when the bound type is not an owner-ref carrier, so a
// borrow stashed in a local, struct, or aggregate and used after release is caught.
func borrowedOwnerRefStateHasRawInteriorAlias(state borrowedOwnerRefState) bool {
	if state.RawInteriorAffineAlias {
		return true
	}
	for _, child := range state.Fields {
		if borrowedOwnerRefStateHasRawInteriorAlias(child) {
			return true
		}
	}
	return false
}

// stripRawInteriorAffineAlias removes raw interior-alias content from a borrowed-owner
// state (recursively), leaving any genuine owner-borrow content intact. Used when a cast
// severs the alias by producing a non-reference value (e.g. `.uintptr()`).
func stripRawInteriorAffineAlias(state borrowedOwnerRefState) borrowedOwnerRefState {
	if state.RawInteriorAffineAlias {
		state.HasDirect = false
		state.Direct = affineValueKey{}
		state.RawInteriorAffineAlias = false
	}
	for name, child := range state.Fields {
		stripped := stripRawInteriorAffineAlias(child)
		if hasBorrowedOwnerRefState(stripped) {
			state.Fields[name] = stripped
		} else {
			delete(state.Fields, name)
		}
	}
	return state
}

// poolInteriorBorrowKey reports whether the projection `expr` (`h.ptr`, `h.slots[i]`)
// produces a plain raw interior pointer out of a live Pooled handle, returning the
// affine-value key of that handle. Releasing the handle recycles the pointee, so any
// later use of a value carrying this alias is a use-after-free. Genuine owner/handle-typed
// members are left to the existing owner-borrow machinery.
func (a *Analyzer) poolInteriorBorrowKey(expr ast.Expr, base ast.Expr) (affineValueKey, bool) {
	// Check the base is a live Pooled handle FIRST. The base is a sub-expression that is
	// already analyzed by the time this runs, so this is cheap and — crucially — never
	// re-enters analysis of `expr`. (`expr` itself must NOT be re-analyzed here: this is
	// invoked from the field/index use-site check during analysis of `expr`, so calling
	// analyzeExpr(expr) would recurse infinitely.)
	baseKey, ok := a.lookupAffineValueKey(base)
	if !ok {
		return affineValueKey{}, false
	}
	baseType := a.exprTypes[base]
	if baseType == nil {
		baseType = a.analyzeExpr(base)
	}
	if !isRegionPoolHandleType(baseType) {
		return affineValueKey{}, false
	}
	// The projected member must be a plain raw reference (the interior pointer). Use only
	// the cached type — do not analyze `expr`. A genuine owner/handle-typed member is left
	// to the existing owner-borrow machinery.
	valueType := a.exprTypes[expr]
	if valueType == nil {
		return affineValueKey{}, false
	}
	if _, ok := borrowableOwnerRefElemType(valueType); ok {
		return affineValueKey{}, false
	}
	if _, ok := valueType.(*RefType); !ok {
		return affineValueKey{}, false
	}
	return baseKey, true
}

// isRegionPoolHandleType reports whether t is the stdlib region-pool handle
// `Pooled[T]` (heap.elisa). Releasing such a handle recycles the slot its `.ptr`
// field references, so a raw interior pointer copied out of it dangles afterward.
// This is deliberately NARROWER than "any affine handle": other affine handles
// (Thread/Task/MutexGuard) hold interior pointers into externally-owned storage that
// consumption does not free (e.g. a guard's raw OS-mutex pointer stays valid after
// `move`), so they must NOT be flagged. Matching by name mirrors the existing
// handle-name conventions (affineHandleKind, isBuiltinProtocolOwnerType); if more
// recycle-on-release pool handles appear, promote this to a struct attribute.
func isRegionPoolHandleType(t Type) bool {
	switch tt := t.(type) {
	case *StructType:
		return tt.Name == "Pooled"
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		return ok && base.Name == "Pooled"
	default:
		return false
	}
}

// reportRawInteriorAffineAliasUse rejects a use of a local that holds a raw interior
// pointer copied out of an affine handle once that handle has been consumed (released):
// the slot has been recycled, so the pointer dangles. Returns true if it reported.
func (a *Analyzer) reportRawInteriorAffineAliasUse(expr ast.Expr, sym *Symbol) bool {
	if sym == nil || a.currentBorrowedOwnerRefs == nil {
		return false
	}
	state, ok := a.currentBorrowedOwnerRefs[sym]
	if !ok || !state.RawInteriorAffineAlias || !state.HasDirect {
		return false
	}
	origin, ok := a.lookupAffineValueStateForKey(state.Direct)
	if !ok || origin.ConsumedBy == "" {
		return false
	}
	a.errorf(expr.Pos(), consumedFactUseMessage("interior reference", affineValueDisplayName(expr), origin.ConsumedBy))
	return true
}

func (a *Analyzer) recordBorrowedOwnerRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentBorrowedOwnerRefs == nil || sym == nil {
		return
	}
	valueState, hasValueState := a.borrowedOwnerRefStateForExpr(value)
	// A raw interior pointer aliased out of a Pooled handle (directly, or buried in a
	// struct/aggregate field) must be tracked even when the bound type is not an
	// owner-ref carrier — that is what lets a borrow stashed in a local or data
	// structure and used after release be caught. The cast-sever in
	// borrowedOwnerRefStateForExpr already drops aliases that a non-reference cast
	// (e.g. a `uintptr` address snapshot) turned into a plain value.
	if hasValueState && borrowedOwnerRefStateHasRawInteriorAlias(valueState) {
		a.currentBorrowedOwnerRefs[sym] = cloneBorrowedOwnerRefState(valueState)
		return
	}
	if !a.containsBorrowedOwnerRefValues(sym.Type, map[string]bool{}) {
		delete(a.currentBorrowedOwnerRefs, sym)
		return
	}
	if hasValueState {
		a.currentBorrowedOwnerRefs[sym] = valueState
		return
	}
	if _, ok := borrowableOwnerRefElemType(sym.Type); ok {
		a.currentBorrowedOwnerRefs[sym] = borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}
		return
	}
	delete(a.currentBorrowedOwnerRefs, sym)
}

func (a *Analyzer) recordBorrowedOwnerRefTarget(target ast.Expr, expected Type, value ast.Expr) {
	if a.currentBorrowedOwnerRefs == nil {
		return
	}
	root, steps, ok := a.lookupBorrowedOwnerRefTargetPath(target)
	if !ok || root == nil {
		return
	}
	if len(steps) == 0 {
		a.recordBorrowedOwnerRefBinding(root, value)
		return
	}
	if !a.containsBorrowedOwnerRefValues(root.Type, map[string]bool{}) {
		delete(a.currentBorrowedOwnerRefs, root)
		return
	}
	current := a.currentBorrowedOwnerRefs[root]
	if a.containsBorrowedOwnerRefValues(expected, map[string]bool{}) {
		if state, ok := a.borrowedOwnerRefStateForExpr(value); ok {
			current = assignBorrowedOwnerRefStateAtPath(current, steps, state)
		} else {
			current = clearBorrowedOwnerRefStateAtPath(current, steps)
		}
	} else {
		current = clearBorrowedOwnerRefStateAtPath(current, steps)
	}
	if hasBorrowedOwnerRefState(current) {
		a.currentBorrowedOwnerRefs[root] = current
	} else {
		delete(a.currentBorrowedOwnerRefs, root)
	}
}

func (a *Analyzer) recordValueBinding(sym *Symbol, value ast.Expr) {
	if a.currentValueBindings == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	if sym.Mutable || value == nil {
		sym.AliasOf = nil
		delete(a.currentValueBindings, sym)
		return
	}
	sym.AliasOf = nil
	if aliasRoot := directValueBindingAliasRoot(a.currentScope, value); aliasRoot != nil && aliasRoot != sym {
		sym.AliasOf = aliasRoot
	}
	a.currentValueBindings[sym] = value
}

func (a *Analyzer) recordSpecializedValueTypeBinding(sym *Symbol, valueType Type) {
	if a.currentSpecializedValueTypes == nil || sym == nil {
		return
	}
	if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
		return
	}
	trackedType := sym.Type
	trackedAny := false
	if specializedType, ok := a.specializeCallbackCarryingType(trackedType, valueType); ok {
		trackedType = specializedType
		trackedAny = true
	}
	if namedStateType, ok := applyNamedStateFromActualType(trackedType, valueType); ok {
		trackedType = namedStateType
		trackedAny = true
	}
	if trackedAny {
		a.currentSpecializedValueTypes[sym] = a.cloneTrackedValueType(trackedType)
		a.refreshTerminalTypestateTracking(sym, trackedType)
		return
	}
	delete(a.currentSpecializedValueTypes, sym)
	a.refreshTerminalTypestateTracking(sym, sym.Type)
}

func (a *Analyzer) currentTrackedValueType(sym *Symbol) Type {
	if sym == nil {
		return nil
	}
	current := sym.Type
	if specializedType, ok := a.lookupCurrentSpecializedValueType(sym); ok {
		current = specializedType
	}
	if a.currentScope != nil {
		ident := &ast.Ident{Name: sym.Name}
		if refinedType, ok := a.lookupRefinedExprType(ident); ok {
			if specializedType, ok := a.specializeCallbackCarryingType(refinedType, current); ok {
				current = specializedType
			} else {
				current = refinedType
			}
		}
	}
	return current
}

func (a *Analyzer) bindTrackedValueType(sym *Symbol, tracked Type) {
	if a.currentSpecializedValueTypes == nil || sym == nil || tracked == nil {
		return
	}
	a.currentSpecializedValueTypes[sym] = a.cloneTrackedValueType(tracked)
	if a.currentScope != nil && sym.Name != "" {
		a.currentScope.Refinements[sym.Name] = tracked
	}
	a.refreshTerminalTypestateTracking(sym, tracked)
}
