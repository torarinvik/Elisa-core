package semantic

func (a *Analyzer) widenNamedStatesDeep(t Type) (Type, bool) {
	return a.widenNamedStatesDeepWithSeen(t, map[Type]bool{})
}

func (a *Analyzer) widenNamedStatesDeepWithSeen(t Type, seen map[Type]bool) (Type, bool) {
	if t == nil {
		return nil, false
	}
	if seen[t] {
		return nil, false
	}
	seen[t] = true
	defer delete(seen, t)
	switch tt := t.(type) {
	case *AggregateStateType:
		nextBase, ok := a.widenNamedStatesDeepWithSeen(tt.Base, seen)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.widenNamedStatesDeepWithSeen(tt.Elem, seen)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *StructType:
		if base, ok := namedStateStructBase(tt); ok && base != nil {
			if widened := replaceTrackedNamedStateArg(tt, fullNamedStateType(base)); widened != nil && !SameType(widened, t) {
				if next, ok := a.widenNamedStatesDeepWithSeen(widened, seen); ok {
					return next, true
				}
				return widened, true
			}
		}
		changed := false
		fields := cloneStructFields(tt.Fields)
		for name, field := range tt.Fields {
			nextField, ok := a.widenNamedStatesDeepWithSeen(field.Type, seen)
			if !ok {
				continue
			}
			field.Type = nextField
			fields[name] = field
			changed = true
		}
		if !changed {
			return nil, false
		}
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		current := tt
		changed := false
		if base, ok := current.Base.(*StructType); ok && base != nil && len(base.NamedStateCases) != 0 {
			fullState := fullNamedStateType(base)
			if currentState, ok := namedStateCurrentArg(current); ok && currentState != nil && !sameNamedStateType(currentState, fullState) {
				idx := namedStateArgIndex(base)
				if idx >= 0 && idx < len(current.Args) {
					args := append([]Type(nil), current.Args...)
					args[idx] = fullState
					cloned := *current
					cloned.Args = args
					current = &cloned
					changed = true
				}
			}
		}
		baseStruct, ok := current.Base.(*StructType)
		if !ok || baseStruct == nil {
			if changed {
				return current, true
			}
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		for name, field := range baseStruct.Fields {
			currentFieldType, ok := a.lookupResolvedFieldType(current, name)
			if !ok {
				currentFieldType = field.Type
			}
			nextField, ok := a.widenNamedStatesDeepWithSeen(currentFieldType, seen)
			if !ok {
				continue
			}
			field.Type = nextField
			fields[name] = field
			changed = true
		}
		if !changed {
			return nil, false
		}
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *current
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) widenNamedStatesDeepAtPath(current Type, steps []borrowReturnAnnotationStep) (Type, bool) {
	if current == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.widenNamedStatesDeep(current)
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.widenNamedStatesDeepAtPath(tt.Base, steps)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.widenNamedStatesDeepAtPath(tt.Elem, steps)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	step := steps[0]
	if step.Field == "" {
		return a.widenNamedStatesDeep(current)
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.widenNamedStatesDeepAtPath(field.Type, steps[1:])
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.widenNamedStatesDeepAtPath(currentFieldType, steps[1:])
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return a.widenNamedStatesDeep(current)
	}
}

func (a *Analyzer) projectTrackedValueTypeAtPath(current Type, steps []borrowReturnAnnotationStep) (Type, bool) {
	if current == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.cloneTrackedValueType(current), true
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		return a.projectTrackedValueTypeAtPath(tt.Base, steps)
	case *RefType:
		return a.projectTrackedValueTypeAtPath(tt.Elem, steps)
	}
	step := steps[0]
	if step.Field != "" {
		fieldType, ok := a.lookupResolvedFieldType(current, step.Field)
		if !ok {
			return nil, false
		}
		return a.projectTrackedValueTypeAtPath(fieldType, steps[1:])
	}
	if step.Wildcard || step.Index != nil {
		switch tt := current.(type) {
		case *ArrayType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		case *DArrayType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		case *ViewType:
			return a.projectTrackedValueTypeAtPath(tt.Elem, steps[1:])
		}
	}
	return nil, false
}

func (a *Analyzer) replaceTrackedValueTypeAtPath(current Type, steps []borrowReturnAnnotationStep, replacement Type) (Type, bool) {
	if current == nil || replacement == nil {
		return nil, false
	}
	if len(steps) == 0 {
		return a.cloneTrackedValueType(replacement), true
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.replaceTrackedValueTypeAtPath(tt.Base, steps, replacement)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps, replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.replaceTrackedValueTypeAtPath(tt.Elem, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextFieldType, ok := a.replaceTrackedValueTypeAtPath(field.Type, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextFieldType, ok := a.replaceTrackedValueTypeAtPath(currentFieldType, steps[1:], replacement)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyNamedStatePoststateAtPath(current Type, steps []borrowReturnAnnotationStep, stateCases []string) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.applyNamedStatePoststateAtPath(tt.Base, steps, stateCases)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.applyNamedStatePoststateAtPath(tt.Elem, steps, stateCases)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	if len(steps) == 0 {
		base, ok := namedStateStructBase(current)
		if !ok || base == nil {
			return nil, false
		}
		desired := newNamedStateType(base.Name, base.NamedStateCases, stateCases)
		if desired == nil {
			return nil, false
		}
		replaced := replaceTrackedNamedStateArg(current, desired)
		if replaced == nil {
			return nil, false
		}
		return replaced, true
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyNamedStatePoststateAtPath(field.Type, steps[1:], stateCases)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyNamedStatePoststateAtPath(currentFieldType, steps[1:], stateCases)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyRefStatePoststateAtPath(current Type, steps []borrowReturnAnnotationStep, desired RefState) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.applyRefStatePoststateAtPath(tt.Base, steps, desired)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		if len(steps) == 0 {
			return cloneRefTypeWithState(tt, desired), true
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps, desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *DArrayType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	case *ViewType:
		step := steps[0]
		if !step.Wildcard && step.Index == nil {
			return nil, false
		}
		nextElem, ok := a.applyRefStatePoststateAtPath(tt.Elem, steps[1:], desired)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	if len(steps) == 0 {
		return nil, false
	}
	step := steps[0]
	if step.Field == "" {
		return nil, false
	}
	switch tt := current.(type) {
	case *StructType:
		field, ok := tt.Fields[step.Field]
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyRefStatePoststateAtPath(field.Type, steps[1:], desired)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		field.Type = nextField
		fields[step.Field] = field
		return cloneStructTypeWithFields(tt, fields), true
	case *GenericInstanceType:
		baseStruct, ok := tt.Base.(*StructType)
		if !ok || baseStruct == nil {
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(tt, step.Field)
		if !ok {
			return nil, false
		}
		nextField, ok := a.applyRefStatePoststateAtPath(currentFieldType, steps[1:], desired)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			return nil, false
		}
		field.Type = nextField
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *tt
		cloned.Base = clonedBase
		return &cloned, true
	default:
		return nil, false
	}
}

func (a *Analyzer) applyFuncPoststateAtPath(original Type, current Type, steps []borrowReturnAnnotationStep, poststate FuncPoststate) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch poststate.Kind {
	case FuncPoststateKindPreserve:
		replacement, ok := a.projectTrackedValueTypeAtPath(original, steps)
		if !ok {
			return nil, false
		}
		return a.replaceTrackedValueTypeAtPath(current, steps, replacement)
	case FuncPoststateKindNamedState:
		return a.applyNamedStatePoststateAtPath(current, steps, poststate.StateCases)
	case FuncPoststateKindRefState:
		return a.applyRefStatePoststateAtPath(current, steps, poststate.RefState)
	default:
		return nil, false
	}
}

func splitFuncPoststatesByCondition(poststates []FuncPoststate) (always []FuncPoststate, whenTrue []FuncPoststate, whenFalse []FuncPoststate) {
	for _, poststate := range poststates {
		switch poststate.Condition.Kind {
		case FuncPoststateConditionReturnBool:
			if poststate.Condition.ReturnBool {
				whenTrue = append(whenTrue, poststate)
			} else {
				whenFalse = append(whenFalse, poststate)
			}
		default:
			always = append(always, poststate)
		}
	}
	return always, whenTrue, whenFalse
}

func (a *Analyzer) applyFuncPoststateListAtArgPath(original Type, current Type, argSteps []borrowReturnAnnotationStep, poststates []FuncPoststate) (Type, bool) {
	if current == nil || len(poststates) == 0 {
		return current, false
	}
	updated := current
	changed := false
	for _, poststate := range poststates {
		fullPath := joinBorrowReturnAnnotationSteps(argSteps, poststate.Path)
		next, ok := a.applyFuncPoststateAtPath(original, updated, fullPath, poststate)
		if !ok || next == nil {
			continue
		}
		updated = next
		changed = true
	}
	return updated, changed
}
