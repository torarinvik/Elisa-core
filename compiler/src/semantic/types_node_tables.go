package semantic

func cloneFuncGuardEffects(effects []FuncGuardEffect) []FuncGuardEffect {
	if len(effects) == 0 {
		return nil
	}
	cloned := make([]FuncGuardEffect, len(effects))
	copy(cloned, effects)
	return cloned
}

func cloneFuncPoststates(poststates []FuncPoststate) []FuncPoststate {
	if len(poststates) == 0 {
		return nil
	}
	cloned := make([]FuncPoststate, len(poststates))
	for i, poststate := range poststates {
		cloned[i] = FuncPoststate{
			Position:   poststate.Position,
			Condition:  poststate.Condition,
			ParamIndex: poststate.ParamIndex,
			Path:       cloneBorrowReturnAnnotationSteps(poststate.Path),
			Kind:       poststate.Kind,
			StateCases: append([]string(nil), poststate.StateCases...),
			RefState:   poststate.RefState,
		}
	}
	return cloned
}

func NodeKeyInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "NodeKey" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

func NodeKeyEnumType(t Type) (*EnumType, bool) {
	gi, ok := NodeKeyInstance(t)
	if !ok {
		return nil, false
	}
	enumType, ok := StripAggregateStateType(gi.Args[0]).(*EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, false
	}
	return enumType, true
}

func NodeTableInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "NodeTable" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func NodeTableParts(t Type) (*EnumType, Type, bool) {
	gi, ok := NodeTableInstance(t)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := StripAggregateStateType(gi.Args[0]).(*EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, false
	}
	return enumType, gi.Args[1], true
}
