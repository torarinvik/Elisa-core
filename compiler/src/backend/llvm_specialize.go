//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (g *llvmGenerator) ensureSpecializedFunction(decl *ast.FuncDecl, base *semantic.FuncType, typeBindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, nil, fmt.Errorf("generic specialization requires a function declaration and type")
	}
	params := funcGenericParams(base)
	orderedArgs := make([]semantic.Type, 0, len(params))
	for _, param := range params {
		name := param.Name
		bound, ok := typeBindings[name]
		if !ok {
			return nil, nil, fmt.Errorf("missing specialization binding for type parameter %s in %s", name, decl.Name)
		}
		orderedArgs = append(orderedArgs, bound)
	}
	specializationBase := decl.Name
	if base.Name != "" {
		specializationBase = base.Name
	}
	specializedName := mangleGenericType(specializationBase, orderedArgs)
	specializedType := g.specializedFuncTypes[specializedName]
	if specializedType == nil {
		specializedType = specializeFuncType(base, typeBindings, g.result.StaticImpls)
		g.specializedFuncTypes[specializedName] = specializedType
	}
	if existing, ok := g.functions[specializedName]; ok {
		return existing, specializedType, nil
	}
	fnValue, err := g.addFunction(specializedName, specializedType)
	if err != nil {
		return nil, nil, err
	}
	g.setDefinedFunctionLinkage(specializedName, fnValue, specializedType)
	g.functions[specializedName] = fnValue
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, nil, err
	}
	return fnValue, specializedType, nil
}

func specializeFuncType(base *semantic.FuncType, typeBindings map[string]semantic.Type, impls map[string]*semantic.StaticImpl) *semantic.FuncType {
	if base == nil {
		return nil
	}
	params := make([]semantic.Type, 0, len(base.Params))
	for _, param := range base.Params {
		params = append(params, substituteType(param, typeBindings, impls))
	}
	specialized := &semantic.FuncType{
		Name:                      base.Name,
		TypeParams:                nil,
		RefStorageParams:          nil,
		RefStateParams:            nil,
		RegionParams:              append([]string(nil), base.RegionParams...),
		PermissionParams:          append([]string(nil), base.PermissionParams...),
		GenericParams:             nil,
		ShapeParams:               append([]string(nil), base.ShapeParams...),
		FreshReturnShapeParams:    append([]string(nil), base.FreshReturnShapeParams...),
		InlineMode:                base.InlineMode,
		HasInlineMode:             base.HasInlineMode,
		HasNoRecurse:              base.HasNoRecurse,
		TemperatureMode:           base.TemperatureMode,
		HasTemperatureMode:        base.HasTemperatureMode,
		Params:                    params,
		ExplicitParamCount:        base.ExplicitParamCount,
		ExplicitParamNames:        append([]string(nil), base.ExplicitParamNames...),
		ExplicitParamDefaultExprs: append([]ast.Expr(nil), base.ExplicitParamDefaultExprs...),
		ExplicitParamHasDefault:   append([]bool(nil), base.ExplicitParamHasDefault...),
		ImplicitParamNames:        append([]string(nil), base.ImplicitParamNames...),
		Return:                    substituteType(base.Return, typeBindings, impls),
		Variadic:                  base.Variadic,
		SinkParams:                append([]bool(nil), base.SinkParams...),
		SinkParamsKnown:           base.SinkParamsKnown,
	}
	semantic.AppendSpecializedBoundaryTreeStoreParams(specialized)
	return specialized
}

func inferTypeBindingsFromCall(fn *semantic.FuncType, args []ast.Expr, argTypes []semantic.Type, resultType semantic.Type) map[string]semantic.Type {
	bindings := map[string]semantic.Type{}
	if fn == nil {
		return bindings
	}
	limit := len(fn.Params)
	if len(argTypes) < limit {
		limit = len(argTypes)
	}
	for i := 0; i < limit; i++ {
		collectSpecializationBindings(fn.Params[i], argTypes[i], bindings)
	}
	if resultType != nil && fn.Return != nil {
		collectSpecializationBindings(fn.Return, resultType, bindings)
		collectSpecializationBindings(semantic.StripAggregateStateType(fn.Return), semantic.StripAggregateStateType(resultType), bindings)
	}
	params := funcGenericParams(fn)
	for _, param := range params {
		if _, ok := bindings[param.Name]; ok || param.Kind != ast.GenericParamValue {
			continue
		}
		for _, argType := range argTypes {
			if value, ok := findConstGenericArgInType(argType, param.Name); ok {
				bindings[param.Name] = value
				break
			}
		}
	}
	_ = args
	return bindings
}

func findConstGenericArgInType(t semantic.Type, name string) (*semantic.ConstValueType, bool) {
	switch tt := t.(type) {
	case *semantic.GenericInstanceType:
		params := structGenericParams(asStructType(tt.Base))
		for i, param := range params {
			if i >= len(tt.Args) {
				break
			}
			if param.Name == name {
				if value, ok := tt.Args[i].(*semantic.ConstValueType); ok {
					return value, true
				}
			}
			if value, ok := findConstGenericArgInType(tt.Args[i], name); ok {
				return value, true
			}
		}
	case *semantic.RefType:
		return findConstGenericArgInType(tt.Elem, name)
	case *semantic.ArrayType:
		return findConstGenericArgInType(tt.Elem, name)
	case *semantic.DArrayType:
		return findConstGenericArgInType(tt.Elem, name)
	}
	return nil, false
}

func asStructType(t semantic.Type) *semantic.StructType {
	st, _ := t.(*semantic.StructType)
	return st
}

func inferTypeBindingsFromFuncTypes(pattern *semantic.FuncType, actual *semantic.FuncType) map[string]semantic.Type {
	bindings := map[string]semantic.Type{}
	if pattern == nil || actual == nil {
		return bindings
	}
	collectSpecializationBindings(pattern, actual, bindings)
	return bindings
}

func mergeTypeBindings(dst map[string]semantic.Type, src map[string]semantic.Type) map[string]semantic.Type {
	if len(src) == 0 {
		if dst == nil {
			return map[string]semantic.Type{}
		}
		return dst
	}
	if dst == nil {
		dst = make(map[string]semantic.Type, len(src))
	}
	for name, typ := range src {
		if typ == nil {
			continue
		}
		if _, exists := dst[name]; !exists {
			dst[name] = typ
		}
	}
	return dst
}

func dynDictRuntimeInstance(t semantic.Type) (*semantic.GenericInstanceType, bool) {
	gi, ok := t.(*semantic.GenericInstanceType)
	if !ok || gi.Name != "DynDict" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func collectSpecializationBindings(pattern semantic.Type, actual semantic.Type, bindings map[string]semantic.Type) {
	if pattern == nil || actual == nil {
		return
	}
	if patternDynDict, ok := dynDictRuntimeInstance(pattern); ok {
		if actualDict, ok := actual.(*semantic.DictType); ok {
			collectSpecializationBindings(patternDynDict.Args[0], actualDict.Key, bindings)
			collectSpecializationBindings(patternDynDict.Args[1], actualDict.Value, bindings)
			return
		}
	}
	if patternDict, ok := pattern.(*semantic.DictType); ok {
		if actualDynDict, ok := dynDictRuntimeInstance(actual); ok {
			collectSpecializationBindings(patternDict.Key, actualDynDict.Args[0], bindings)
			collectSpecializationBindings(patternDict.Value, actualDynDict.Args[1], bindings)
			return
		}
	}
	switch p := pattern.(type) {
	case *semantic.TypeParamType:
		if _, ok := bindings[p.Name]; !ok {
			bindings[p.Name] = actual
		}
	case *semantic.ConstParamType:
		if _, ok := bindings[p.Name]; !ok {
			if value, ok := actual.(*semantic.ConstValueType); ok {
				bindings[p.Name] = value
			}
		}
	case *semantic.RefStorageParamType:
		if _, ok := bindings[p.Name]; !ok {
			bindings[p.Name] = actual
		}
	case *semantic.RefStateParamType:
		if _, ok := bindings[p.Name]; !ok {
			bindings[p.Name] = actual
		}
	case *semantic.IDType:
		if a, ok := actual.(*semantic.IDType); ok {
			collectSpecializationBindings(p.Tag, a.Tag, bindings)
			collectSpecializationBindings(p.Storage, a.Storage, bindings)
		}
	case *semantic.RefType:
		if a, ok := actual.(*semantic.RefType); ok {
			if p.StateParam != "" {
				if _, ok := bindings[p.StateParam]; !ok {
					bindings[p.StateParam] = &semantic.RefStateValueType{State: a.State}
				}
			}
			if p.StorageParam != "" {
				if _, ok := bindings[p.StorageParam]; !ok {
					bindings[p.StorageParam] = &semantic.RefStorageValueType{Storage: a.Storage}
				}
			}
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.ArrayType:
		if a, ok := actual.(*semantic.ArrayType); ok {
			if p.ConstParam != "" && a.HasConstSize {
				if _, ok := bindings[p.ConstParam]; !ok {
					bindings[p.ConstParam] = &semantic.ConstValueType{Value: semantic.ConstValue{Kind: semantic.ConstInt, Int: a.ConstSize}}
				}
			}
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.ViewType:
		if a, ok := actual.(*semantic.ViewType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.DArrayType:
		if a, ok := actual.(*semantic.DArrayType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.DArrayViewType:
		if a, ok := actual.(*semantic.DArrayViewType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.DictType:
		if a, ok := actual.(*semantic.DictType); ok {
			collectSpecializationBindings(p.Key, a.Key, bindings)
			collectSpecializationBindings(p.Value, a.Value, bindings)
		}
	case *semantic.GenericInstanceType:
		if a, ok := actual.(*semantic.GenericInstanceType); ok && p.Name == a.Name && len(p.Args) == len(a.Args) {
			for i := range p.Args {
				collectSpecializationBindings(p.Args[i], a.Args[i], bindings)
			}
		}
	case *semantic.FuncType:
		if a, ok := actual.(*semantic.FuncType); ok {
			limit := len(p.Params)
			if len(a.Params) < limit {
				limit = len(a.Params)
			}
			for i := 0; i < limit; i++ {
				collectSpecializationBindings(p.Params[i], a.Params[i], bindings)
			}
			collectSpecializationBindings(p.Return, a.Return, bindings)
		}
	}
}
