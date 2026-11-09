package semantic

import (
	"elisacore/src/ast"
	"maps"
)

func (a *Analyzer) specializeFunctionValueType(expected Type, actual Type) (Type, bool) {
	expectedFunc, ok := expected.(*FuncType)
	if !ok {
		return expected, false
	}
	actualFunc, ok := actual.(*FuncType)
	if !ok {
		return expected, false
	}
	specialized, _ := a.substituteType(expectedFunc, nil, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return expected, false
	}
	changed := false
	if len(specialized.ExplicitParamNames) == 0 && len(actualFunc.ExplicitParamNames) != 0 {
		specialized.ExplicitParamNames = append([]string(nil), actualFunc.ExplicitParamNames...)
		changed = true
	}
	if !funcTypeHasAnyExplicitDefault(specialized) && funcTypeHasAnyExplicitDefault(actualFunc) {
		specialized.ExplicitParamDefaultExprs = append([]ast.Expr(nil), actualFunc.ExplicitParamDefaultExprs...)
		specialized.ExplicitParamHasDefault = append([]bool(nil), actualFunc.ExplicitParamHasDefault...)
		changed = true
	}
	if len(specialized.ImplicitParamNames) == 0 && len(actualFunc.ImplicitParamNames) != 0 {
		specialized.ImplicitParamNames = append([]string(nil), actualFunc.ImplicitParamNames...)
		changed = true
	}
	if specialized.Name == "func" && actualFunc.Name != "" && actualFunc.Name != specialized.Name {
		specialized.Name = actualFunc.Name
		changed = true
	}
	limit := len(specialized.Params)
	if len(actualFunc.Params) < limit {
		limit = len(actualFunc.Params)
	}
	for i := 0; i < limit; i++ {
		if paramType, ok := a.specializeFunctionValueType(specialized.Params[i], actualFunc.Params[i]); ok {
			specialized.Params[i] = paramType
			changed = true
		}
	}
	if returnType, ok := a.specializeFunctionValueType(specialized.Return, actualFunc.Return); ok {
		specialized.Return = returnType
		changed = true
	}
	if hasRegionProvenance(actualFunc.ReturnProvenance) {
		specialized.ReturnProvenance = cloneRegionRefState(actualFunc.ReturnProvenance)
		specialized.ReturnProvenanceKnown = true
		changed = true
	}
	if hasBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs) {
		specialized.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(actualFunc.ReturnBorrowedOwnerRefs)
		specialized.ReturnBorrowedOwnerRefsKnown = true
		changed = true
	}
	return specialized, changed
}

func cloneStructTypeWithFields(base *StructType, fields map[string]Field) *StructType {
	if base == nil {
		return nil
	}
	cloned := *base
	cloned.Fields = fields
	return &cloned
}

func cloneStructFields(fields map[string]Field) map[string]Field {
	if len(fields) == 0 {
		return map[string]Field{}
	}
	return maps.Clone(fields)
}

func (a *Analyzer) lookupResolvedFieldType(actual Type, name string) (Type, bool) {
	// One field of a struct: resolve just that field instead of building (and
	// substituting) the whole resolved field list, as resolvedStructFields does.
	actual = StripAggregateStateType(actual)
	var base *StructType
	var inst *GenericInstanceType
	switch tt := actual.(type) {
	case *StructType:
		base = tt
	case *GenericInstanceType:
		if sb, ok := tt.Base.(*StructType); ok {
			base, inst = sb, tt
		}
	}
	if base != nil {
		if base.Decl == nil {
			return nil, false
		}
		field, ok := base.Fields[name]
		if !ok || !structDeclHasField(base.Decl, name) {
			return nil, false
		}
		if inst == nil {
			return field.Type, true
		}
		bindings := genericBindingsForStructInstance(base, inst.Args)
		regionBindings := regionBindingsForStructInstance(base, inst.Args)
		if len(bindings) != 0 || len(regionBindings) != 0 {
			return a.substituteType(field.Type, bindings, nil, regionBindings, nil), true
		}
		return field.Type, true
	}
	fields, ok := a.resolvedStructFields(actual)
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}

func (a *Analyzer) specializeCallbackCarryingType(expected Type, actual Type) (Type, bool) {
	return a.specializeCallbackCarryingTypeWithDepth(expected, actual, 0)
}

// Generic substitution can create fresh instance objects on each visit, so pointer
// identity alone cannot bound traversal of recursive by-value field graphs.
func (a *Analyzer) specializeCallbackCarryingTypeWithDepth(expected Type, actual Type, depth int) (Type, bool) {
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("callback-carrying type specialization", semanticTraversalDepthLimit)
		return expected, false
	}
	if expected == nil || actual == nil {
		return expected, false
	}
	if specialized, ok := a.specializeFunctionValueType(expected, actual); ok {
		return specialized, true
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingTypeWithDepth(tt.Base, StripAggregateStateType(actual), depth+1)
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		var fields map[string]Field
		for name, field := range tt.Fields {
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeWithDepth(field.Type, actualFieldType, depth+1)
			if !fieldChanged {
				continue
			}
			if fields == nil {
				fields = cloneStructFields(tt.Fields)
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		regionBindings := regionBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		var fields map[string]Field
		for name, field := range baseStruct.Fields {
			expectedFieldType := field.Type
			if len(bindings) != 0 || len(regionBindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, regionBindings, nil)
			}
			actualFieldType, ok := a.lookupResolvedFieldType(actual, name)
			if !ok {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeWithDepth(expectedFieldType, actualFieldType, depth+1)
			if !fieldChanged {
				continue
			}
			if fields == nil {
				fields = cloneStructFields(baseStruct.Fields)
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}

func (a *Analyzer) specializeCallbackCarryingTypeFromExpr(expected Type, actualExpr ast.Expr) (Type, bool) {
	if expected == nil || actualExpr == nil {
		return expected, false
	}
	if actualType := a.exprType(actualExpr); actualType != nil {
		if specialized, ok := a.specializeCallbackCarryingType(expected, actualType); ok {
			return specialized, true
		}
	}
	if specialized, ok := a.functionValueTypeForExpr(actualExpr); ok {
		if next, changed := a.specializeFunctionValueType(expected, specialized); changed {
			return next, true
		}
	}
	switch tt := expected.(type) {
	case *AggregateStateType:
		nextBase, changed := a.specializeCallbackCarryingTypeFromExpr(tt.Base, actualExpr)
		if !changed {
			return expected, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *StructType:
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(field.Type, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok {
			return expected, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, tt.Args)
		regionBindings := regionBindingsForStructInstance(baseStruct, tt.Args)
		changed := false
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			fieldExpr, ok := a.resolveProjectedFieldValueExpr(actualExpr, name)
			if !ok || fieldExpr == nil {
				continue
			}
			expectedFieldType := field.Type
			if len(bindings) != 0 || len(regionBindings) != 0 {
				expectedFieldType = a.substituteType(expectedFieldType, bindings, nil, regionBindings, nil)
			}
			nextType, fieldChanged := a.specializeCallbackCarryingTypeFromExpr(expectedFieldType, fieldExpr)
			if !fieldChanged {
				continue
			}
			field.Type = nextType
			fields[name] = field
			changed = true
		}
		if !changed {
			return expected, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return expected, false
	}
}

// joinedPoolScopeForArg resolves a pool_submit1 pool argument (`(&pool).cast[...]`
// or a bare pool binding) to the enclosing `pool`/`nursery` statement that owns
// it. Tasks submitted to that pool are joined when the statement exits.
func (a *Analyzer) joinedPoolScopeForArg(poolArg ast.Expr) *poolScopeState {
	for poolArg != nil {
		switch n := poolArg.(type) {
		case *ast.ParenExpr:
			poolArg = n.Inner
		case *ast.CastExpr:
			poolArg = n.Operand
		case *ast.AddrOfExpr:
			poolArg = n.Operand
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			for i := len(a.currentPoolScopes) - 1; i >= 0; i-- {
				pool := &a.currentPoolScopes[i]
				if pool.Node != nil && sym.Node == ast.Node(pool.Node) {
					return pool
				}
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}
