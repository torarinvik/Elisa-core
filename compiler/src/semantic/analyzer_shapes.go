package semantic

import (
	"fmt"

	"llcontext/src/lexer"
)

func freshReturnTracker(t Type) map[string]freshReturnStatus {
	if t == nil {
		return nil
	}
	seen := map[string]bool{}
	collectShapeParamsInType(t, seen)
	if len(seen) == 0 {
		return nil
	}
	tracker := make(map[string]freshReturnStatus, len(seen))
	for name := range seen {
		tracker[name] = freshReturnUnknown
	}
	return tracker
}

func collectShapeParamsInType(t Type, out map[string]bool) {
	if t == nil || out == nil {
		return
	}
	switch n := t.(type) {
	case *RefType:
		collectShapeParamsInType(n.Elem, out)
	case *ArrayType:
		collectShapeParamsInType(n.Elem, out)
	case *DArrayType:
		if param, ok := n.Shape.(*ShapeParam); ok {
			out[param.Name] = true
		}
		collectShapeParamsInType(n.Elem, out)
	case *DArrayViewType:
		collectShapeParamsInType(n.Elem, out)
	case *DStrType:
		if param, ok := n.Shape.(*ShapeParam); ok {
			out[param.Name] = true
		}
	case *GenericInstanceType:
		for _, arg := range n.Args {
			collectShapeParamsInType(arg, out)
		}
	case *FuncType:
		for _, param := range n.Params {
			collectShapeParamsInType(param, out)
		}
		collectShapeParamsInType(n.Return, out)
	}
}

func inferredFreshReturnShapeParams(status map[string]freshReturnStatus) []string {
	if len(status) == 0 {
		return nil
	}
	out := make([]string, 0, len(status))
	for name, shapeStatus := range status {
		if shapeStatus == freshReturnAlways {
			out = append(out, name)
		}
	}
	return out
}

func mergeShapeParamNames(existing []string, extra []string) []string {
	if len(extra) == 0 {
		return append([]string(nil), existing...)
	}
	seen := make(map[string]bool, len(existing)+len(extra))
	out := make([]string, 0, len(existing)+len(extra))
	for _, name := range existing {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range extra {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func knownFreshReturnShapeParams(name string, ret Type) []string {
	spec, ok := shapeTransformTable[name]
	if !ok || len(spec.FreshReturnShapeParams) == 0 {
		return nil
	}
	returnShapeParams := map[string]bool{}
	collectShapeParamsInType(ret, returnShapeParams)
	out := make([]string, 0, len(spec.FreshReturnShapeParams))
	for _, param := range spec.FreshReturnShapeParams {
		if returnShapeParams[param] {
			out = append(out, param)
		}
	}
	return out
}

func (a *Analyzer) recordFreshReturnBindings(actual Type) {
	if a.currentReturn == nil || actual == nil || len(a.returnFreshShapeStatus) == 0 {
		return
	}
	shapeBindings := map[string]Shape{}
	a.collectTypeBindings(a.currentReturn, actual, map[string]Type{}, shapeBindings)
	for name, current := range a.returnFreshShapeStatus {
		shape, ok := shapeBindings[name]
		if !ok {
			continue
		}
		_, isFresh := shape.(*FreshShape)
		switch current {
		case freshReturnUnknown:
			if isFresh {
				a.returnFreshShapeStatus[name] = freshReturnAlways
			} else {
				a.returnFreshShapeStatus[name] = freshReturnNotFresh
			}
		case freshReturnAlways:
			if !isFresh {
				a.returnFreshShapeStatus[name] = freshReturnNotFresh
			}
		}
	}
}

func (a *Analyzer) reportShapeMismatchNotes(pos lexer.Pos, expected Type, actual Type) {
	for _, note := range shapeMismatchNotes(expected, actual) {
		a.errorf(pos, "note: %s", note)
	}
}

func shapeMismatchNotes(expected Type, actual Type) []string {
	actualFresh := collectFreshShapesInType(actual)
	if len(actualFresh) == 0 {
		return runtimeBackedShapeMismatchNotes(expected, actual)
	}
	notes := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, fresh := range actualFresh {
		if fresh == nil {
			continue
		}
		note := freshShapeOriginNote(fresh)
		if note == "" || seen[note] {
			continue
		}
		seen[note] = true
		notes = append(notes, note)
	}
	expectedFresh := collectFreshShapesInType(expected)
	if len(expectedFresh) > 0 {
		sameFresh := false
		for _, lhs := range expectedFresh {
			for _, rhs := range actualFresh {
				if lhs != nil && rhs != nil && lhs.ID == rhs.ID {
					sameFresh = true
					break
				}
			}
			if sameFresh {
				break
			}
		}
		if !sameFresh {
			note := "separate calls that produce fresh shapes do not share the same logical shape identity"
			if !seen[note] {
				notes = append(notes, note)
			}
		}
	}
	for _, note := range runtimeBackedShapeMismatchNotes(expected, actual) {
		if !seen[note] {
			seen[note] = true
			notes = append(notes, note)
		}
	}
	return notes
}

func runtimeBackedShapeMismatchNotes(expected Type, actual Type) []string {
	return nil
}

func collectFreshShapesInType(t Type) []*FreshShape {
	if t == nil {
		return nil
	}
	seen := map[int]bool{}
	out := make([]*FreshShape, 0)
	collectFreshShapesInto(t, seen, &out)
	return out
}

func collectFreshShapesInto(t Type, seen map[int]bool, out *[]*FreshShape) {
	if t == nil {
		return
	}
	switch n := t.(type) {
	case *RefType:
		collectFreshShapesInto(n.Elem, seen, out)
	case *ArrayType:
		collectFreshShapesInto(n.Elem, seen, out)
	case *DArrayType:
		if fresh, ok := n.Shape.(*FreshShape); ok && !seen[fresh.ID] {
			seen[fresh.ID] = true
			*out = append(*out, fresh)
		}
		collectFreshShapesInto(n.Elem, seen, out)
	case *DArrayViewType:
		collectFreshShapesInto(n.Elem, seen, out)
	case *DStrType:
		if fresh, ok := n.Shape.(*FreshShape); ok && !seen[fresh.ID] {
			seen[fresh.ID] = true
			*out = append(*out, fresh)
		}
	case *GenericInstanceType:
		for _, arg := range n.Args {
			collectFreshShapesInto(arg, seen, out)
		}
	case *FuncType:
		for _, param := range n.Params {
			collectFreshShapesInto(param, seen, out)
		}
		collectFreshShapesInto(n.Return, seen, out)
	}
}

func freshShapeOriginNote(shape *FreshShape) string {
	if shape == nil {
		return ""
	}
	label := shape.Label
	if label == "" {
		label = "shape"
	}
	if shape.Origin != "" {
		return fmt.Sprintf("%s returns a fresh logical shape for %s", shape.Origin, label)
	}
	return fmt.Sprintf("this expression has a fresh logical shape for %s", label)
}
