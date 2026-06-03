package semantic

import (
	"elisacore/src/ast"
)

func aggregateStateStates(t *AggregateStateType) []RefState {
	if t == nil {
		return nil
	}
	if len(t.States) != 0 {
		states := make([]RefState, len(t.States))
		copy(states, t.States)
		return states
	}
	return []RefState{t.State}
}

func cloneAggregateStateWithBase(base Type, states []RefState) *AggregateStateType {
	cloned := &AggregateStateType{Base: base}
	if len(states) > 0 {
		cloned.State = states[0]
		cloned.States = append([]RefState(nil), states...)
	}
	return cloned
}

func StripAggregateStateType(t Type) Type {
	agg, ok := t.(*AggregateStateType)
	if !ok || agg == nil {
		return t
	}
	return agg.Base
}

func DefaultNamedStateType(t Type) Type {
	if t == nil {
		return nil
	}
	if _, ok := t.(*GenericInstanceType); ok {
		if _, hasState := namedStateCurrentArg(t); hasState {
			return t
		}
	}
	base, ok := t.(*StructType)
	if !ok || base == nil || len(base.NamedStateCases) == 0 {
		return t
	}
	idx := namedStateArgIndex(base)
	if idx < 0 {
		return t
	}
	params := genericParamsForStructType(base)
	args := make([]Type, len(params))
	for i, param := range params {
		switch param.Kind {
		case ast.GenericParamType:
			args[i] = &TypeParamType{Name: param.Name}
		case ast.GenericParamRegion:
			args[i] = &RegionParamType{Name: param.Name}
		case ast.GenericParamRefStorage:
			args[i] = &RefStorageParamType{Name: param.Name}
		case ast.GenericParamState:
			args[i] = fullNamedStateType(base)
		case ast.GenericParamValue:
			args[i] = &ConstParamType{Name: param.Name, ValueType: &BuiltinType{Name: param.InterfaceBound}}
		}
	}
	return &GenericInstanceType{Name: base.Name, Base: base, Args: args}
}

func DefaultStatefulType(t Type) Type {
	return DefaultAggregateStateType(DefaultNamedStateType(t))
}

func AggregateStateParamCount(t Type) int {
	switch tt := StripAggregateStateType(t).(type) {
	case *StructType:
		if tt == nil || tt.Decl == nil {
			return 0
		}
		if tt.Decl.StateParamCount > 0 {
			return tt.Decl.StateParamCount
		}
		if tt.Decl.HasStateParam {
			return 1
		}
		return 0
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil || base.Decl == nil {
			return 0
		}
		if base.Decl.StateParamCount > 0 {
			return base.Decl.StateParamCount
		}
		if base.Decl.HasStateParam {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func SupportsAggregateStateType(t Type) bool {
	return AggregateStateParamCount(t) > 0
}

func DefaultAggregateStateType(t Type) Type {
	if t == nil {
		return nil
	}
	if agg, ok := t.(*AggregateStateType); ok {
		if len(aggregateStateStates(agg)) == AggregateStateParamCount(agg.Base) {
			return t
		}
		return t
	}
	count := AggregateStateParamCount(t)
	if count == 0 {
		return t
	}
	states := make([]RefState, count)
	for i := range states {
		states[i] = RefStateNullable
	}
	return cloneAggregateStateWithBase(StripAggregateStateType(t), states)
}
