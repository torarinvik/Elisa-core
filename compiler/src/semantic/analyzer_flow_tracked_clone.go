package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) cloneBorrowedOwnerRefBindings() map[*Symbol]borrowedOwnerRefState {
	if a.currentBorrowedOwnerRefs == nil {
		return nil
	}
	cloned := make(map[*Symbol]borrowedOwnerRefState, len(a.currentBorrowedOwnerRefs))
	for sym, state := range a.currentBorrowedOwnerRefs {
		cloned[sym] = cloneBorrowedOwnerRefState(state)
	}
	return cloned
}

func (a *Analyzer) cloneFunctionValueType(fn *FuncType) *FuncType {
	if fn == nil {
		return nil
	}
	cloned, _ := a.substituteType(fn, nil, nil, nil, nil).(*FuncType)
	return cloned
}

func (a *Analyzer) cloneFunctionValueBindings() map[*Symbol]*FuncType {
	if a.currentFunctionValues == nil {
		return nil
	}
	cloned := make(map[*Symbol]*FuncType, len(a.currentFunctionValues))
	for sym, fn := range a.currentFunctionValues {
		cloned[sym] = a.cloneFunctionValueType(fn)
	}
	return cloned
}

func (a *Analyzer) cloneTrackedValueType(t Type) Type {
	return a.cloneTrackedValueTypeWithSeen(t, map[Type]Type{})
}

func (a *Analyzer) cloneTrackedValueTypeWithSeen(t Type, seen map[Type]Type) Type {
	return a.cloneTrackedValueTypeWithSeenDepth(t, seen, 0)
}

func (a *Analyzer) cloneTrackedValueTypeWithSeenDepth(t Type, seen map[Type]Type, depth int) Type {
	if t == nil {
		return nil
	}
	if depth > semanticCloneDepthLimit {
		a.reportSemanticDepthLimit("tracked-type clone", semanticCloneDepthLimit)
		return invalidType
	}
	if cloned, ok := seen[t]; ok {
		return cloned
	}
	switch tt := t.(type) {
	case *StructType:
		cloned := *tt
		cloned.Fields = map[string]Field{}
		clonedPtr := &cloned
		seen[t] = clonedPtr
		for name, field := range tt.Fields {
			field.Type = a.cloneTrackedValueTypeWithSeenDepth(field.Type, seen, depth+1)
			if IsInvalidType(field.Type) {
				seen[t] = invalidType
				return invalidType
			}
			cloned.Fields[name] = field
		}
		return clonedPtr
	case *GenericInstanceType:
		cloned := *tt
		cloned.Args = make([]Type, len(tt.Args))
		seen[t] = &cloned
		for i, arg := range tt.Args {
			cloned.Args[i] = a.cloneTrackedValueTypeWithSeenDepth(arg, seen, depth+1)
			if IsInvalidType(cloned.Args[i]) {
				seen[t] = invalidType
				return invalidType
			}
		}
		cloned.Base = a.cloneTrackedValueTypeWithSeenDepth(tt.Base, seen, depth+1)
		if IsInvalidType(cloned.Base) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *RefType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeenDepth(tt.Elem, seen, depth+1)
		if IsInvalidType(cloned.Elem) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *ArrayType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeenDepth(tt.Elem, seen, depth+1)
		if IsInvalidType(cloned.Elem) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *DArrayType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeenDepth(tt.Elem, seen, depth+1)
		if IsInvalidType(cloned.Elem) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *OptionalType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Value = a.cloneTrackedValueTypeWithSeenDepth(tt.Value, seen, depth+1)
		if IsInvalidType(cloned.Value) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *ViewType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeenDepth(tt.Elem, seen, depth+1)
		if IsInvalidType(cloned.Elem) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *DArrayViewType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Elem = a.cloneTrackedValueTypeWithSeenDepth(tt.Elem, seen, depth+1)
		if IsInvalidType(cloned.Elem) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *DictType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Key = a.cloneTrackedValueTypeWithSeenDepth(tt.Key, seen, depth+1)
		if IsInvalidType(cloned.Key) {
			seen[t] = invalidType
			return invalidType
		}
		cloned.Value = a.cloneTrackedValueTypeWithSeenDepth(tt.Value, seen, depth+1)
		if IsInvalidType(cloned.Value) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *FuncType:
		cloned, _ := a.substituteType(tt, nil, nil, nil, nil).(*FuncType)
		if cloned == nil {
			return nil
		}
		seen[t] = cloned
		return cloned
	case *ErrorUnionType:
		cloned := *tt
		seen[t] = &cloned
		cloned.Value = a.cloneTrackedValueTypeWithSeenDepth(tt.Value, seen, depth+1)
		if IsInvalidType(cloned.Value) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *PackedEnumStoreType:
		cloned := *tt
		seen[t] = &cloned
		cloned.State = a.cloneTrackedValueTypeWithSeenDepth(tt.State, seen, depth+1)
		if IsInvalidType(cloned.State) {
			seen[t] = invalidType
			return invalidType
		}
		return &cloned
	case *DStrType:
		cloned := *tt
		seen[t] = &cloned
		return &cloned
	case *SViewType:
		cloned := *tt
		seen[t] = &cloned
		return &cloned
	case *BuiltinType, *TypeParamType, *NeverType, *NullType, *InvalidType, *ErrorSetType, *EnumType, *OpaqueType:
		seen[t] = t
		return t
	default:
		cloned := a.substituteType(t, nil, nil, nil, nil)
		if IsInvalidType(cloned) {
			seen[t] = invalidType
			return invalidType
		}
		seen[t] = cloned
		return cloned
	}
}

func (a *Analyzer) cloneTrackedTypesByRoot(src map[*Symbol]Type) map[*Symbol]Type {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]Type, len(src))
	for sym, tracked := range src {
		cloned[sym] = a.cloneTrackedValueType(tracked)
	}
	return cloned
}

func trackedNamedStateStructBase(t Type) (*StructType, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return trackedNamedStateStructBase(tt.Base)
	default:
		return namedStateStructBase(t)
	}
}

func trackedNamedStateCurrentArg(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *AggregateStateType:
		return trackedNamedStateCurrentArg(tt.Base)
	default:
		return namedStateCurrentArg(t)
	}
}

func replaceTrackedNamedStateArg(template Type, state Type) Type {
	switch tt := template.(type) {
	case *AggregateStateType:
		inner := replaceTrackedNamedStateArg(tt.Base, state)
		if inner == nil {
			return nil
		}
		return cloneAggregateStateWithBase(inner, aggregateStateStates(tt))
	default:
		base, ok := namedStateStructBase(template)
		if !ok || base == nil {
			return nil
		}
		return instantiateNamedStateStructLiteralType(base, template, state)
	}
}

func applyNamedStateFromActualType(template Type, actual Type) (Type, bool) {
	templateBase, ok := trackedNamedStateStructBase(template)
	if !ok || templateBase == nil {
		return nil, false
	}
	actualBase, ok := trackedNamedStateStructBase(actual)
	if !ok || actualBase == nil || actualBase.Name != templateBase.Name {
		return nil, false
	}
	state, ok := trackedNamedStateCurrentArg(actual)
	if !ok || state == nil {
		return nil, false
	}
	replaced := replaceTrackedNamedStateArg(template, state)
	if replaced == nil {
		return nil, false
	}
	return replaced, true
}

func (a *Analyzer) mergeTrackedNamedStateValueTypes(dst Type, src Type) (Type, bool) {
	if dst == nil || src == nil {
		return nil, false
	}
	switch dt := dst.(type) {
	case *AggregateStateType:
		st, ok := src.(*AggregateStateType)
		if !ok || !sameAggregateStateLists(aggregateStateStates(dt), aggregateStateStates(st)) {
			return nil, false
		}
		mergedBase, ok := a.mergeTrackedNamedStateValueTypes(dt.Base, st.Base)
		if !ok {
			mergedBase, ok = a.mergeSpecializedValueTypes(dt.Base, st.Base)
			if !ok {
				return nil, false
			}
		}
		return cloneAggregateStateWithBase(mergedBase, aggregateStateStates(dt)), true
	case *StructStateCaseType, *StructStateSetType:
		merged := mergeNamedStateTypes(dst, src, nil)
		if IsInvalidType(merged) {
			return nil, false
		}
		return merged, true
	case *GenericInstanceType:
		st, ok := src.(*GenericInstanceType)
		if !ok || dt.Name != st.Name || len(dt.Args) != len(st.Args) {
			return nil, false
		}
		base, ok := dt.Base.(*StructType)
		if !ok || base == nil {
			return nil, false
		}
		stateIndex := namedStateArgIndex(base)
		if stateIndex < 0 {
			return nil, false
		}
		mergedBase, ok := a.mergeSpecializedValueTypes(dt.Base, st.Base)
		if !ok {
			return nil, false
		}
		args := make([]Type, len(dt.Args))
		for i := range dt.Args {
			if i == stateIndex {
				mergedArg := mergeNamedStateTypes(dt.Args[i], st.Args[i], base.NamedStateCases)
				if IsInvalidType(mergedArg) {
					return nil, false
				}
				args[i] = mergedArg
				continue
			}
			if !SameType(dt.Args[i], st.Args[i]) {
				return nil, false
			}
			args[i] = a.cloneTrackedValueType(dt.Args[i])
		}
		cloned := *dt
		cloned.Base = mergedBase
		cloned.Args = args
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) cloneSpecializedValueTypeBindings() map[*Symbol]Type {
	return a.cloneTrackedValueTypeMapWithSeen(a.currentSpecializedValueTypes, map[Type]Type{})
}

func (a *Analyzer) cloneTrackedValueTypeMapWithSeen(src map[*Symbol]Type, seen map[Type]Type) map[*Symbol]Type {
	if src == nil {
		return nil
	}
	if seen == nil {
		seen = map[Type]Type{}
	}
	cloned := make(map[*Symbol]Type, len(src))
	for sym, typ := range src {
		cloned[sym] = a.cloneTrackedValueTypeWithSeen(typ, seen)
	}
	return cloned
}

func (a *Analyzer) cloneValueBindings() map[*Symbol]ast.Expr {
	if a.currentValueBindings == nil {
		return nil
	}
	cloned := make(map[*Symbol]ast.Expr, len(a.currentValueBindings))
	for sym, expr := range a.currentValueBindings {
		cloned[sym] = expr
	}
	return cloned
}
