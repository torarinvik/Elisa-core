package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) resolveEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, bool) {
	if expr == nil || variant == nil {
		return nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(variant.Payload) && expr.ResolvedCommonArgs == nil {
		return expr.ResolvedArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
		return expr.Args, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "enum constructor %q cannot mix positional and named arguments", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if !variant.HasNamedPayloads() {
		a.errorf(expr.Pos(), "enum constructor %q does not declare named payload fields", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, false
	}
	if len(expr.Args) != len(variant.Payload) {
		a.errorf(expr.Pos(), "enum constructor %q expects %d arguments, got %d", enumType.Name+"."+variant.Name, len(variant.Payload), len(expr.Args))
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seen := make([]bool, len(variant.Payload))
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		index, found := variant.PayloadIndex(name)
		if !found {
			a.errorf(arg.Pos(), "enum constructor %q has no payload field %q", enumType.Name+"."+variant.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		if seen[index] {
			a.errorf(arg.Pos(), "enum constructor %q payload field %q is specified more than once", enumType.Name+"."+variant.Name, name)
			a.analyzeExpr(arg)
			ok = false
			continue
		}
		ordered[index] = arg
		seen[index] = true
	}
	for i, wasSeen := range seen {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label != "" {
				a.errorf(expr.Pos(), "enum constructor %q is missing payload field %q", enumType.Name+"."+variant.Name, label)
			} else {
				a.errorf(expr.Pos(), "enum constructor %q is missing argument %d", enumType.Name+"."+variant.Name, i+1)
			}
			ok = false
		}
	}
	if !ok {
		return nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = nil
	return ordered, true
}

func (a *Analyzer) resolvePackedEnumConstructorArgs(expr *ast.CallExpr, enumType *EnumType, variant *EnumVariant) ([]ast.Expr, map[string]ast.Expr, bool) {
	if expr == nil || enumType == nil || variant == nil {
		return nil, nil, false
	}
	if expr.ResolvedArgsValid && len(expr.ResolvedArgs) == len(variant.Payload) {
		return expr.ResolvedArgs, expr.ResolvedCommonArgs, true
	}
	namedCount := expr.NamedArgCount()
	if namedCount == 0 {
		expr.ResolvedArgsValid = true
		expr.ResolvedArgs = expr.Args
		expr.ResolvedCommonArgs = nil
		return expr.Args, nil, true
	}
	if namedCount != len(expr.Args) {
		a.errorf(expr.Pos(), "enum constructor %q cannot mix positional and named arguments", enumType.Name+"."+variant.Name)
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return nil, nil, false
	}
	ordered := make([]ast.Expr, len(variant.Payload))
	seenPayload := make([]bool, len(variant.Payload))
	commonArgs := make(map[string]ast.Expr)
	ok := true
	for i, arg := range expr.Args {
		name := expr.ArgName(i)
		if index, found := variant.PayloadIndex(name); found {
			if seenPayload[index] {
				a.errorf(arg.Pos(), "enum constructor %q payload field %q is specified more than once", enumType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			ordered[index] = arg
			seenPayload[index] = true
			continue
		}
		if _, found := enumType.Common[name]; found {
			if _, exists := commonArgs[name]; exists {
				a.errorf(arg.Pos(), "packed enum constructor %q common field %q is specified more than once", enumType.Name+"."+variant.Name, name)
				a.analyzeExpr(arg)
				ok = false
				continue
			}
			commonArgs[name] = arg
			continue
		}
		a.errorf(arg.Pos(), "packed enum constructor %q has no payload or common field %q", enumType.Name+"."+variant.Name, name)
		a.analyzeExpr(arg)
		ok = false
	}
	for i, wasSeen := range seenPayload {
		if !wasSeen {
			label := variant.PayloadLabel(i)
			if label != "" {
				a.errorf(expr.Pos(), "enum constructor %q is missing payload field %q", enumType.Name+"."+variant.Name, label)
			} else {
				a.errorf(expr.Pos(), "enum constructor %q is missing argument %d", enumType.Name+"."+variant.Name, i+1)
			}
			ok = false
		}
	}
	if !ok {
		return nil, nil, false
	}
	expr.ResolvedArgsValid = true
	expr.ResolvedArgs = ordered
	expr.ResolvedCommonArgs = commonArgs
	return ordered, commonArgs, true
}

func (a *Analyzer) collectRuntimeBridgeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef, regionParams map[string]bool) bool {
	bridge, ok := classifyRuntimeBridge(pattern, actual)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		if patternDArray, ok := pattern.(*DArrayType); ok {
			a.collectTypeBindings(patternDArray.Elem, bridge.DynArray.Args[0], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		if patternDynArray, ok := dynArrayRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynArray.Args[0], bridge.DArray.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		return true
	case runtimeBridgeDictDynDict:
		if patternDict, ok := pattern.(*DictType); ok {
			a.collectTypeBindings(patternDict.Key, bridge.DynDict.Args[0], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(patternDict.Value, bridge.DynDict.Args[1], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		if patternDynDict, ok := dynDictRuntimeInstance(pattern); ok {
			a.collectTypeBindings(patternDynDict.Args[0], bridge.Dict.Key, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(patternDynDict.Args[1], bridge.Dict.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			return true
		}
		return true
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDStrU8Ref:
		return true
	default:
		return false
	}
}

func regionParamSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func (a *Analyzer) collectRegionBinding(patternRegion, actualRegion string, bindings map[string]string, regionParams map[string]bool) {
	if patternRegion == "" || actualRegion == "" || bindings == nil || regionParams == nil || !regionParams[patternRegion] {
		return
	}
	if _, exists := bindings[patternRegion]; !exists {
		bindings[patternRegion] = actualRegion
	}
}

func (a *Analyzer) collectPermissionBinding(name string, refs []ast.PermissionRef, permissionBindings map[string][]ast.PermissionRef) {
	if name == "" || permissionBindings == nil {
		return
	}
	canonical := canonicalizePermissionRefs(refs)
	if _, exists := permissionBindings[name]; !exists {
		permissionBindings[name] = canonical
	}
}

func (a *Analyzer) collectTypeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape, regionBindings map[string]string, permissionBindings map[string][]ast.PermissionRef, regionParams map[string]bool) {
	if pattern == nil || actual == nil {
		return
	}
	if a.collectRuntimeBridgeBindings(pattern, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams) {
		return
	}
	switch p := pattern.(type) {
	case *TypeParamType:
		if existing, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		} else if _, existingIsParam := existing.(*TypeParamType); existingIsParam {
			// A prior binding to an (unresolved) type parameter — e.g. the
			// return-type match binding this param to the CALLER's own unbound
			// param when the call sits in a no-expected-type position — is only a
			// placeholder. A concrete argument-derived type is strictly better, so
			// let it override. Without this, a type param that appears only in
			// value positions (e.g. fold's accumulator `A`, inferred from `init`)
			// never resolves once the speculative return binding has claimed it.
			if _, actualIsParam := actual.(*TypeParamType); !actualIsParam {
				bindings[p.Name] = actual
			}
		}
	case *ConstParamType:
		if _, exists := bindings[p.Name]; !exists {
			if value, ok := actual.(*ConstValueType); ok {
				bindings[p.Name] = value
			}
		}
	case *IDType:
		if act, ok := actual.(*IDType); ok {
			a.collectTypeBindings(p.Tag, act.Tag, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(p.Storage, act.Storage, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *ErrorUnionType:
		if act, ok := actual.(*ErrorUnionType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			// A single error-set param binds to whatever the argument's set carries
			// beyond the pattern's own concrete tags (`error[R, Timeout]` matched
			// against `error[IoErr, Timeout]` binds R := IoErr). Repeat encounters
			// JOIN (set union) instead of pinning to the first one, so inference is
			// argument-order independent: R is "everything any matched site may
			// raise", which is exactly what the combinator propagates. Patterns
			// with two or more params cannot split an argument set unambiguously,
			// so they bind nothing here and surface as "cannot infer" at validation.
			if p.Errors != nil && len(p.Errors.Params) == 1 && act.Errors != nil {
				bindErrorSetParamContribution(bindings, p.Errors.Params[0], SubtractErrorTags(act.Errors, p.Errors))
			}
			return
		}
		// The pattern is an error union but the argument is infallible (a plain
		// value, e.g. a `func() -> i64` callback fed to `func() -> i64 error[R]`):
		// bind a lone param to the EMPTY set so a pure function fits a fallible
		// combinator. `T error[∅]` normalizes back to `T` during substitution.
		if p.Errors != nil && len(p.Errors.Params) == 1 {
			bindErrorSetParamContribution(bindings, p.Errors.Params[0], &ErrorSetType{Name: "error[]"})
		}
		a.collectTypeBindings(p.Value, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
	case *OptionalType:
		if act, ok := actual.(*OptionalType); ok {
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *RefType:
		if act, ok := actual.(*RefType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		} else {
			// Parameter is `T&` but the argument is a value (it will be auto-ref'd at the
			// call site, e.g. `write_span(byte[0], n)` against `data: T&`). Bind the element
			// type parameter against the value's own type so T resolves to the value type.
			a.collectTypeBindings(p.Elem, actual, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *ArrayType:
		if act, ok := actual.(*ArrayType); ok {
			if p.ConstParam != "" && act.HasConstSize {
				if _, exists := bindings[p.ConstParam]; !exists {
					bindings[p.ConstParam] = &ConstValueType{Value: ConstValue{Kind: ConstInt, Int: act.ConstSize}}
				}
			}
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *DArrayType:
		if act, ok := actual.(*DArrayType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *ViewType:
		if act, ok := actual.(*ViewType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *DStrType:
		if act, ok := actual.(*DStrType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DictType:
		if act, ok := actual.(*DictType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
			a.collectTypeBindings(p.Key, act.Key, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			a.collectTypeBindings(p.Value, act.Value, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *SViewType:
		if act, ok := actual.(*SViewType); ok {
			a.collectRegionBinding(p.Region, act.Region, regionBindings, regionParams)
		}
	case *EnumType:
		_, _ = actual.(*EnumType)
	case *GenericInstanceType:
		if act, ok := actual.(*GenericInstanceType); ok && p.Name == act.Name && len(p.Args) == len(act.Args) {
			for i := range p.Args {
				a.collectTypeBindings(p.Args[i], act.Args[i], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
		}
	case *TupleType:
		if act, ok := actual.(*TupleType); ok && len(p.Fields) == len(act.Fields) {
			for i := range p.Fields {
				a.collectTypeBindings(p.Fields[i].Type, act.Fields[i].Type, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
		}
	case *AggregateStateType:
		if act, ok := actual.(*AggregateStateType); ok {
			a.collectTypeBindings(p.Base, act.Base, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
		}
	case *FuncType:
		if act, ok := actual.(*FuncType); ok {
			limit := len(p.Params)
			if len(act.Params) < limit {
				limit = len(act.Params)
			}
			for i := 0; i < limit; i++ {
				a.collectTypeBindings(p.Params[i], act.Params[i], bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			}
			a.collectTypeBindings(p.Return, act.Return, bindings, shapeBindings, regionBindings, permissionBindings, regionParams)
			if name, ok := funcTypeHasSinglePermissionRowParam(p); ok {
				a.collectPermissionBinding(name, functionPermissionRefs(act), permissionBindings)
			}
		}
	}
}

func (a *Analyzer) collectShapeBinding(pattern, actual Shape, bindings map[string]Shape) {
	param, ok := pattern.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; !exists {
		bindings[param.Name] = actual
	}
}

func (a *Analyzer) matchReturnType(actual Type) Type {
	if a.currentReturn == nil || actual == nil {
		return a.currentReturn
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	permissionBindings := map[string][]ast.PermissionRef{}
	a.collectTypeBindings(a.currentReturn, actual, bindings, shapeBindings, nil, permissionBindings, nil)
	return a.substituteType(a.currentReturn, bindings, shapeBindings, nil, permissionBindings)
}

func (a *Analyzer) bindFreshReturnShapes(fn *FuncType, bindings map[string]Shape) {
	if fn == nil || fn.Return == nil {
		return
	}
	for _, name := range fn.FreshReturnShapeParams {
		a.bindFreshShape(&ShapeParam{Name: name}, fn.Name, bindings)
	}
}

func (a *Analyzer) bindFreshShape(shape Shape, origin string, bindings map[string]Shape) {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; exists {
		return
	}
	a.freshShapeCounter++
	bindings[param.Name] = &FreshShape{ID: a.freshShapeCounter, Label: param.Name, Origin: origin}
}
