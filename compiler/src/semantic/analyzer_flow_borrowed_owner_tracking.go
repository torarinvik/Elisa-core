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
	case *DArrayViewType:
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

// affineInteriorBorrowOrigin reports whether `expr` produces a raw interior pointer
// borrowed out of a live affine handle, returning the affine-value key of that handle.
// It recognizes a direct interior projection (`h.ptr`, `h.slots[i]` where `h` is an
// affine handle and the projected member is a plain reference) and copy-hops of an
// already-tracked interior alias (`c = b`). Consuming the returned handle recycles the
// pointee, so any later use of the bound local is a use-after-free. This is what makes
// a deliberately-stashed `b = h.ptr` survive `release(move h)` checkable, closing the
// hole that affine consumption alone (which only reaches the handle's own sub-paths)
// leaves open.
func (a *Analyzer) affineInteriorBorrowOrigin(expr ast.Expr) (affineValueKey, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.affineInteriorBorrowOrigin(n.Inner)
	case *ast.CastExpr:
		// Only follow a cast that keeps the value a reference into the pooled object
		// (e.g. reinterpreting `heap T&` as `u8&`). A cast to a non-reference — such
		// as `.uintptr()` yielding a copied address integer — severs the alias: the
		// result no longer dereferences the slot, so using it after release is safe.
		castType := a.exprTypes[expr]
		if castType == nil {
			castType = a.analyzeExpr(expr)
		}
		if _, ok := castType.(*RefType); !ok {
			return affineValueKey{}, false
		}
		return a.affineInteriorBorrowOrigin(n.Operand)
	case *ast.MoveExpr:
		return a.affineInteriorBorrowOrigin(n.Operand)
	case *ast.Ident:
		// copy-hop: `c = b` inherits the origin of an already-tracked interior alias.
		if a.currentScope == nil || a.currentBorrowedOwnerRefs == nil {
			return affineValueKey{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return affineValueKey{}, false
		}
		state, ok := a.currentBorrowedOwnerRefs[sym]
		if ok && state.RawInteriorAffineAlias && state.HasDirect {
			return state.Direct, true
		}
		return affineValueKey{}, false
	case *ast.FieldExpr:
		return a.affineInteriorBorrowOriginFromProjection(expr, n.Object)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return affineValueKey{}, false
		}
		return a.affineInteriorBorrowOriginFromProjection(expr, n.Object)
	default:
		return affineValueKey{}, false
	}
}

func (a *Analyzer) affineInteriorBorrowOriginFromProjection(expr ast.Expr, base ast.Expr) (affineValueKey, bool) {
	valueType := a.exprTypes[expr]
	if valueType == nil {
		valueType = a.analyzeExpr(expr)
	}
	// A genuine owner/handle-typed member is handled by the existing owner-borrow
	// machinery; this path is only for plain raw interior pointers into the object.
	if _, ok := borrowableOwnerRefElemType(valueType); ok {
		return affineValueKey{}, false
	}
	if _, ok := valueType.(*RefType); !ok {
		return affineValueKey{}, false
	}
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
	// Only a binding that is itself a reference can dangle: an alias copied into a
	// non-reference local (e.g. a `uintptr` address snapshot) is just a value and is
	// safe to read after release.
	if _, isRef := sym.Type.(*RefType); isRef {
		if key, ok := a.affineInteriorBorrowOrigin(value); ok {
			a.currentBorrowedOwnerRefs[sym] = borrowedOwnerRefState{HasDirect: true, Direct: key, RawInteriorAffineAlias: true}
			return
		}
	}
	if !a.containsBorrowedOwnerRefValues(sym.Type, map[string]bool{}) {
		delete(a.currentBorrowedOwnerRefs, sym)
		return
	}
	if state, ok := a.borrowedOwnerRefStateForExpr(value); ok {
		a.currentBorrowedOwnerRefs[sym] = state
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
		return
	}
	delete(a.currentSpecializedValueTypes, sym)
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
}
