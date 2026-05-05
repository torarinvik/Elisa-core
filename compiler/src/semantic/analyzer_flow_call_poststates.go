package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) rememberConditionalCallPoststates(call *ast.CallExpr, fnType *FuncType, originalByRoot map[*Symbol]Type) {
	if a == nil || call == nil || fnType == nil || !funcPoststatesHaveConditional(fnType.Poststates) {
		return
	}
	if a.conditionalCallPoststateOriginals == nil {
		a.conditionalCallPoststateOriginals = map[*ast.CallExpr]map[*Symbol]Type{}
	}
	a.conditionalCallPoststateOriginals[call] = a.cloneTrackedTypesByRoot(originalByRoot)
}

func (a *Analyzer) updateNamedStateTypeAtPath(root *Symbol, current Type, structSteps []borrowReturnAnnotationStep, steps []borrowReturnAnnotationStep, pos lexer.Pos, value ast.Expr, valueType Type, unknown bool) (Type, bool) {
	if current == nil {
		return nil, false
	}
	switch tt := current.(type) {
	case *AggregateStateType:
		nextBase, ok := a.updateNamedStateTypeAtPath(root, tt.Base, structSteps, steps, pos, value, valueType, unknown)
		if !ok {
			return nil, false
		}
		return cloneAggregateStateWithBase(nextBase, aggregateStateStates(tt)), true
	case *RefType:
		nextElem, ok := a.updateNamedStateTypeAtPath(root, tt.Elem, structSteps, steps, pos, value, valueType, unknown)
		if !ok {
			return nil, false
		}
		cloned := *tt
		cloned.Elem = nextElem
		return &cloned, true
	}
	updatedCurrent := current
	changed := false
	if base, ok := trackedNamedStateStructBase(updatedCurrent); ok && base != nil {
		if len(steps) == 0 {
			if unknown {
				if widened := replaceTrackedNamedStateArg(updatedCurrent, fullNamedStateType(base)); widened != nil {
					return widened, !SameType(widened, current)
				}
			} else if trackedType, ok := applyNamedStateFromActualType(updatedCurrent, valueType); ok {
				return trackedType, !SameType(trackedType, current)
			}
		} else if namedStateAssignmentAffectsDerivedState(base, steps) {
			state := fullNamedStateType(base)
			if !unknown && len(steps) == 1 && steps[0].Field != "" {
				if inferredState, ok := a.inferDirectFieldAssignedNamedState(pos, root, structSteps, base, steps[0].Field, value); ok {
					state = inferredState
				}
			}
			if replaced := replaceTrackedNamedStateArg(updatedCurrent, state); replaced != nil {
				updatedCurrent = replaced
				changed = !SameType(replaced, current)
			}
		}
	}
	if len(steps) == 0 {
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
	step := steps[0]
	if step.Field == "" {
		if widened, ok := a.widenNamedStatesDeep(updatedCurrent); ok {
			return widened, true
		}
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
	nextStructSteps := append(cloneBorrowReturnAnnotationSteps(structSteps), cloneBorrowReturnAnnotationStep(step))
	switch currentType := updatedCurrent.(type) {
	case *StructType:
		field, ok := currentType.Fields[step.Field]
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		nextFieldType, ok := a.updateNamedStateTypeAtPath(root, field.Type, nextStructSteps, steps[1:], pos, value, valueType, unknown)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		fields := cloneStructFields(currentType.Fields)
		field.Type = nextFieldType
		fields[step.Field] = field
		return cloneStructTypeWithFields(currentType, fields), true
	case *GenericInstanceType:
		baseStruct, ok := currentType.Base.(*StructType)
		if !ok || baseStruct == nil {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		currentFieldType, ok := a.lookupResolvedFieldType(currentType, step.Field)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		nextFieldType, ok := a.updateNamedStateTypeAtPath(root, currentFieldType, nextStructSteps, steps[1:], pos, value, valueType, unknown)
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		fields := cloneStructFields(baseStruct.Fields)
		field, ok := fields[step.Field]
		if !ok {
			if changed {
				return updatedCurrent, true
			}
			return nil, false
		}
		field.Type = nextFieldType
		fields[step.Field] = field
		clonedBase := cloneStructTypeWithFields(baseStruct, fields)
		cloned := *currentType
		cloned.Base = clonedBase
		return &cloned, true
	default:
		if changed {
			return updatedCurrent, true
		}
		return nil, false
	}
}

func (a *Analyzer) recordNamedStateAssignmentTarget(target ast.Expr, value ast.Expr, valueType Type) {
	if a.currentSpecializedValueTypes == nil || target == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(target)
	if !ok || root == nil {
		return
	}
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.updateNamedStateTypeAtPath(root, current, nil, steps, target.Pos(), value, valueType, false)
	if !ok || updatedType == nil {
		return
	}
	a.bindTrackedValueType(root, updatedType)
}

func (a *Analyzer) recordNamedStateAugAssignTarget(target ast.Expr) {
	if a.currentSpecializedValueTypes == nil || target == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(target)
	if !ok || root == nil {
		return
	}
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.updateNamedStateTypeAtPath(root, current, nil, steps, target.Pos(), nil, nil, true)
	if !ok || updatedType == nil {
		return
	}
	a.bindTrackedValueType(root, updatedType)
}

func (a *Analyzer) recordNamedStateCallArgMutation(call *ast.CallExpr, arg ast.Expr, paramIndex int, paramType Type) {
	if a.currentSpecializedValueTypes == nil || arg == nil || paramType == nil {
		return
	}
	refType, ok := paramType.(*RefType)
	if !ok || refType == nil {
		return
	}
	root, steps, ok := a.namedStateMutationTargetPath(arg)
	if !ok || root == nil {
		return
	}
	current := a.currentTrackedValueType(root)
	updatedType, ok := a.widenNamedStatesDeepAtPath(current, steps)
	if !ok || updatedType == nil {
		return
	}
	a.noteConservativeCallWidening(root, steps, conservativeCallWideningSource(call, arg, paramIndex), call.Pos(), "ref call without matching ensures", current.String(), updatedType.String())
	a.bindTrackedValueType(root, updatedType)
}

func funcPoststatesForParam(poststates []FuncPoststate, paramIndex int) []FuncPoststate {
	if len(poststates) == 0 {
		return nil
	}
	filtered := make([]FuncPoststate, 0, 1)
	for _, poststate := range poststates {
		if poststate.ParamIndex == paramIndex {
			filtered = append(filtered, poststate)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func (a *Analyzer) recordCallArgPoststates(call *ast.CallExpr, arg ast.Expr, paramIndex int, paramType Type, poststates []FuncPoststate, originalByRoot map[*Symbol]Type) {
	if len(poststates) == 0 {
		a.recordNamedStateCallArgMutation(call, arg, paramIndex, paramType)
		return
	}
	if a.currentSpecializedValueTypes == nil || arg == nil || paramType == nil {
		return
	}
	if _, ok := paramType.(*RefType); !ok {
		return
	}
	root, argSteps, ok := a.namedStateMutationTargetPath(arg)
	if !ok || root == nil {
		return
	}
	original := originalByRoot[root]
	if original == nil {
		original = a.currentTrackedValueType(root)
		originalByRoot[root] = original
	}
	updated, changed := a.computeCallArgPoststateTrackedType(original, argSteps, poststates, false, false)
	if changed && updated != nil {
		a.bindTrackedValueType(root, updated)
	}
}
