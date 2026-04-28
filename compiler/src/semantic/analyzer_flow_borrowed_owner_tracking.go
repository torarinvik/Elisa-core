package semantic

import (
	"llcontext/src/ast"
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

func (a *Analyzer) recordBorrowedOwnerRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentBorrowedOwnerRefs == nil || sym == nil {
		return
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
