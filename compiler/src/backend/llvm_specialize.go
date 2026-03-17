//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func (g *llvmGenerator) ensureSpecializedFunction(decl *ast.FuncDecl, base *semantic.FuncType, typeBindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, nil, fmt.Errorf("generic specialization requires a function declaration and type")
	}
	orderedArgs := make([]semantic.Type, 0, len(decl.TypeParams))
	for _, name := range decl.TypeParams {
		bound, ok := typeBindings[name]
		if !ok {
			return nil, nil, fmt.Errorf("missing specialization binding for type parameter %s in %s", name, decl.Name)
		}
		orderedArgs = append(orderedArgs, bound)
	}
	specializedName := mangleGenericType(decl.Name, orderedArgs)
	if existing, ok := g.functions[specializedName]; ok {
		specializedType := specializeFuncType(base, typeBindings)
		return existing, specializedType, nil
	}
	specializedType := specializeFuncType(base, typeBindings)
	fnValue, err := g.addFunction(specializedName, specializedType)
	if err != nil {
		return nil, nil, err
	}
	g.setDefinedFunctionLinkage(specializedName, fnValue)
	g.functions[specializedName] = fnValue
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, nil, err
	}
	return fnValue, specializedType, nil
}

func specializeFuncType(base *semantic.FuncType, typeBindings map[string]semantic.Type) *semantic.FuncType {
	if base == nil {
		return nil
	}
	params := make([]semantic.Type, 0, len(base.Params))
	for _, param := range base.Params {
		params = append(params, substituteType(param, typeBindings))
	}
	return &semantic.FuncType{
		Name:                   base.Name,
		TypeParams:             nil,
		ShapeParams:            append([]string(nil), base.ShapeParams...),
		FreshReturnShapeParams: append([]string(nil), base.FreshReturnShapeParams...),
		Params:                 params,
		Return:                 substituteType(base.Return, typeBindings),
		Variadic:               base.Variadic,
	}
}

func inferTypeBindingsFromCall(fn *semantic.FuncType, args []ast.Expr, argTypes []semantic.Type) map[string]semantic.Type {
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
	_ = args
	return bindings
}

func dynDictRuntimeInstance(t semantic.Type) (*semantic.GenericInstanceType, bool) {
	gi, ok := t.(*semantic.GenericInstanceType)
	if !ok || gi.Name != "DynDict" || len(gi.Args) != 1 {
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
			collectSpecializationBindings(patternDynDict.Args[0], actualDict.Value, bindings)
			return
		}
	}
	if patternDict, ok := pattern.(*semantic.DictType); ok {
		if actualDynDict, ok := dynDictRuntimeInstance(actual); ok {
			collectSpecializationBindings(patternDict.Value, actualDynDict.Args[0], bindings)
			return
		}
	}
	switch p := pattern.(type) {
	case *semantic.TypeParamType:
		if _, ok := bindings[p.Name]; !ok {
			bindings[p.Name] = actual
		}
	case *semantic.RefType:
		if a, ok := actual.(*semantic.RefType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.ArrayType:
		if a, ok := actual.(*semantic.ArrayType); ok {
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
