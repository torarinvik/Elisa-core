package semantic

import (
	"strconv"
	"strings"

	"elisacore/src/ast"
)

func (a *Analyzer) cloneAffineValueStates() map[affineValueKey]affineValueState {
	if a.currentAffineValues == nil {
		return nil
	}
	cloned := make(map[affineValueKey]affineValueState, len(a.currentAffineValues))
	for key, state := range a.currentAffineValues {
		cloned[key] = state
	}
	return cloned
}

func cloneBorrowedOwnerRefState(state borrowedOwnerRefState) borrowedOwnerRefState {
	cloned := borrowedOwnerRefState{HasDirect: state.HasDirect, Direct: state.Direct}
	if len(state.Fields) != 0 {
		cloned.Fields = make(map[string]borrowedOwnerRefState, len(state.Fields))
		for key, child := range state.Fields {
			cloned.Fields[key] = cloneBorrowedOwnerRefState(child)
		}
	}
	return cloned
}

func cloneBorrowedOwnerRefStateShallowFields(state borrowedOwnerRefState) borrowedOwnerRefState {
	return borrowedOwnerRefState{HasDirect: state.HasDirect, Direct: state.Direct}
}

func cloneBorrowedOwnerRefSummaryTarget(target borrowedOwnerRefSummaryTarget) borrowedOwnerRefSummaryTarget {
	cloned := borrowedOwnerRefSummaryTarget{ParamIndex: target.ParamIndex}
	if len(target.Path) != 0 {
		cloned.Path = append([]borrowReturnAnnotationStep(nil), target.Path...)
	}
	return cloned
}

func cloneBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary) borrowedOwnerRefSummary {
	cloned := borrowedOwnerRefSummary{HasDirect: summary.HasDirect}
	if summary.HasDirect {
		cloned.Direct = cloneBorrowedOwnerRefSummaryTarget(summary.Direct)
	}
	if len(summary.Fields) != 0 {
		cloned.Fields = make(map[string]borrowedOwnerRefSummary, len(summary.Fields))
		for key, child := range summary.Fields {
			cloned.Fields[key] = cloneBorrowedOwnerRefSummary(child)
		}
	}
	return cloned
}

func borrowedOwnerRefSummaryTargetEqual(left borrowedOwnerRefSummaryTarget, right borrowedOwnerRefSummaryTarget) bool {
	if left.ParamIndex != right.ParamIndex || len(left.Path) != len(right.Path) {
		return false
	}
	for i := range left.Path {
		leftStep := left.Path[i]
		rightStep := right.Path[i]
		if leftStep.Field != rightStep.Field || leftStep.Wildcard != rightStep.Wildcard {
			return false
		}
		if (leftStep.Index == nil) != (rightStep.Index == nil) {
			return false
		}
		if leftStep.Index != nil && rightStep.Index != nil && *leftStep.Index != *rightStep.Index {
			return false
		}
	}
	return true
}

func hasBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary) bool {
	return summary.HasDirect || len(summary.Fields) != 0
}

func mergeBorrowedOwnerRefSummary(dst borrowedOwnerRefSummary, src borrowedOwnerRefSummary) (borrowedOwnerRefSummary, bool) {
	merged := borrowedOwnerRefSummary{}
	if dst.HasDirect && src.HasDirect && borrowedOwnerRefSummaryTargetEqual(dst.Direct, src.Direct) {
		merged.HasDirect = true
		merged.Direct = cloneBorrowedOwnerRefSummaryTarget(dst.Direct)
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := mergeBorrowedOwnerRefSummary(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]borrowedOwnerRefSummary{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasBorrowedOwnerRefSummary(merged) {
		return borrowedOwnerRefSummary{}, false
	}
	return merged, true
}

func assignBorrowedOwnerRefSummaryAtPath(dst borrowedOwnerRefSummary, steps []borrowReturnAnnotationStep, value borrowedOwnerRefSummary) borrowedOwnerRefSummary {
	if len(steps) == 0 {
		if merged, ok := mergeBorrowedOwnerRefSummary(dst, value); ok {
			return merged
		}
		return cloneBorrowedOwnerRefSummary(value)
	}
	if dst.Fields == nil {
		dst.Fields = map[string]borrowedOwnerRefSummary{}
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child := dst.Fields[key]
	dst.Fields[key] = assignBorrowedOwnerRefSummaryAtPath(child, steps[1:], value)
	return dst
}

func parseAffineValueKeyPath(path string) ([]borrowReturnAnnotationStep, bool) {
	if path == "" {
		return nil, true
	}
	parts := strings.Split(path, ".")
	steps := make([]borrowReturnAnnotationStep, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			token := part[1 : len(part)-1]
			if token == "*" {
				steps = append(steps, borrowReturnAnnotationStep{Wildcard: true})
				continue
			}
			value, err := strconv.ParseInt(token, 10, 64)
			if err != nil {
				return nil, false
			}
			valueCopy := value
			steps = append(steps, borrowReturnAnnotationStep{Index: &valueCopy})
			continue
		}
		steps = append(steps, borrowReturnAnnotationStep{Field: part})
	}
	return steps, true
}

func borrowedOwnerRefSummaryFromAffineValueKey(key affineValueKey) (borrowedOwnerRefSummaryTarget, bool) {
	if key.Root == nil || key.Root.Kind != SymbolParam {
		return borrowedOwnerRefSummaryTarget{}, false
	}
	steps, ok := parseAffineValueKeyPath(key.Path)
	if !ok {
		return borrowedOwnerRefSummaryTarget{}, false
	}
	return borrowedOwnerRefSummaryTarget{ParamIndex: key.Root.ParamIndex, Path: steps}, true
}

func abstractParamOnlyBorrowedOwnerRefSummary(state borrowedOwnerRefState) (borrowedOwnerRefSummary, bool) {
	if !hasBorrowedOwnerRefState(state) {
		return borrowedOwnerRefSummary{}, false
	}
	out := borrowedOwnerRefSummary{}
	if state.HasDirect {
		target, ok := borrowedOwnerRefSummaryFromAffineValueKey(state.Direct)
		if ok {
			out.HasDirect = true
			out.Direct = target
		}
	}
	if len(state.Fields) != 0 {
		for key, child := range state.Fields {
			filtered, ok := abstractParamOnlyBorrowedOwnerRefSummary(child)
			if !ok {
				continue
			}
			if out.Fields == nil {
				out.Fields = map[string]borrowedOwnerRefSummary{}
			}
			out.Fields[key] = filtered
		}
	}
	if !hasBorrowedOwnerRefSummary(out) {
		return borrowedOwnerRefSummary{}, false
	}
	return out, true
}

func projectBorrowedOwnerRefStateAtSteps(state borrowedOwnerRefState, steps []borrowReturnAnnotationStep) (borrowedOwnerRefState, bool) {
	current := state
	for _, step := range steps {
		var ok bool
		switch {
		case step.Field != "":
			current, ok = projectBorrowedOwnerRefFieldState(current, step.Field)
		case step.Wildcard:
			current, ok = projectBorrowedOwnerRefIndexKeyState(current, regionAnyIndexFieldKey())
		case step.Index != nil:
			current, ok = projectBorrowedOwnerRefIndexKeyState(current, regionIndexFieldKey(*step.Index))
		default:
			return borrowedOwnerRefState{}, false
		}
		if !ok {
			return borrowedOwnerRefState{}, false
		}
	}
	return current, true
}

func summarizeBorrowedOwnerRefIndexStates(state borrowedOwnerRefState) (borrowedOwnerRefState, bool) {
	if !hasBorrowedOwnerRefState(state) {
		return borrowedOwnerRefState{}, false
	}
	summary := cloneBorrowedOwnerRefStateShallowFields(state)
	if len(state.Fields) == 0 {
		return summary, true
	}
	indexStates := make([]borrowedOwnerRefState, 0, len(state.Fields))
	for name, fieldState := range state.Fields {
		if !isRegionIndexFieldKey(name) {
			continue
		}
		if !hasBorrowedOwnerRefState(fieldState) {
			continue
		}
		indexStates = append(indexStates, fieldState)
	}
	if len(indexStates) == 0 {
		summary.Fields = nil
		return summary, true
	}
	merged := cloneBorrowedOwnerRefState(indexStates[0])
	for i := 1; i < len(indexStates); i++ {
		next, ok := mergeBorrowedOwnerRefState(merged, indexStates[i])
		if !ok {
			summary.Fields = nil
			return summary, true
		}
		merged = next
	}
	if !hasBorrowedOwnerRefState(merged) {
		summary.Fields = nil
		return summary, true
	}
	summary.Fields = map[string]borrowedOwnerRefState{
		regionAnyIndexFieldKey(): merged,
	}
	return summary, true
}

func (a *Analyzer) instantiateBorrowedOwnerRefSummary(summary borrowedOwnerRefSummary, args []ast.Expr) (borrowedOwnerRefState, bool) {
	if !hasBorrowedOwnerRefSummary(summary) {
		return borrowedOwnerRefState{}, false
	}
	instantiated := borrowedOwnerRefState{}
	if summary.HasDirect {
		index := summary.Direct.ParamIndex
		if index >= 0 && index < len(args) {
			if argState, ok := a.borrowedOwnerRefStateForExpr(args[index]); ok {
				if len(summary.Direct.Path) == 0 {
					instantiated = cloneBorrowedOwnerRefState(argState)
				} else if projected, ok := projectBorrowedOwnerRefStateAtSteps(argState, summary.Direct.Path); ok {
					instantiated = cloneBorrowedOwnerRefState(projected)
				}
			}
		}
	}
	if len(summary.Fields) != 0 {
		for key, child := range summary.Fields {
			instChild, ok := a.instantiateBorrowedOwnerRefSummary(child, args)
			if !ok {
				continue
			}
			if instantiated.Fields == nil {
				instantiated.Fields = map[string]borrowedOwnerRefState{}
			}
			instantiated.Fields[key] = instChild
		}
	}
	if !hasBorrowedOwnerRefState(instantiated) {
		return borrowedOwnerRefState{}, false
	}
	return instantiated, true
}

func hasBorrowedOwnerRefState(state borrowedOwnerRefState) bool {
	return state.HasDirect || len(state.Fields) != 0
}

func mergeBorrowedOwnerRefState(dst borrowedOwnerRefState, src borrowedOwnerRefState) (borrowedOwnerRefState, bool) {
	merged := borrowedOwnerRefState{}
	if dst.HasDirect && src.HasDirect && dst.Direct == src.Direct {
		merged.HasDirect = true
		merged.Direct = dst.Direct
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := mergeBorrowedOwnerRefState(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]borrowedOwnerRefState{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasBorrowedOwnerRefState(merged) {
		return borrowedOwnerRefState{}, false
	}
	return merged, true
}

func assignBorrowedOwnerRefStateAtPath(dst borrowedOwnerRefState, steps []borrowReturnAnnotationStep, value borrowedOwnerRefState) borrowedOwnerRefState {
	if len(steps) == 0 {
		return cloneBorrowedOwnerRefState(value)
	}
	if dst.Fields == nil {
		dst.Fields = map[string]borrowedOwnerRefState{}
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child := dst.Fields[key]
	dst.Fields[key] = assignBorrowedOwnerRefStateAtPath(child, steps[1:], value)
	return dst
}

func clearBorrowedOwnerRefStateAtPath(dst borrowedOwnerRefState, steps []borrowReturnAnnotationStep) borrowedOwnerRefState {
	if len(steps) == 0 {
		return borrowedOwnerRefState{}
	}
	if len(dst.Fields) == 0 {
		return dst
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child, ok := dst.Fields[key]
	if !ok {
		return dst
	}
	child = clearBorrowedOwnerRefStateAtPath(child, steps[1:])
	if !hasBorrowedOwnerRefState(child) {
		delete(dst.Fields, key)
	} else {
		dst.Fields[key] = child
	}
	if len(dst.Fields) == 0 {
		dst.Fields = nil
	}
	return dst
}
