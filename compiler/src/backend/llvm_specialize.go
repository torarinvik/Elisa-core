//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"reflect"
	"sort"
)

func (g *llvmGenerator) ensureSpecializedFunction(decl *ast.FuncDecl, base *semantic.FuncType, typeBindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, nil, fmt.Errorf("generic specialization requires a function declaration and type")
	}
	params := g.functionSpecializationParams(base)
	var argsBuffer [8]semantic.Type
	orderedArgs := argsBuffer[:0]
	if len(params) > len(argsBuffer) {
		orderedArgs = make([]semantic.Type, 0, len(params))
	}
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
	specializedName, err := g.mangleGenericType(specializationBase, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	localCapture := false
	if g.partSyntheticDepth > 0 {
		var safe bool
		localCapture, safe = g.partSubmitTypes.classify(orderedArgs)
		if !safe {
			return nil, nil, errPartitionDeclined
		}
	}
	if localCapture && g.partitionNativeNameReserved(specializedName) {
		return nil, nil, errPartitionDeclined
	}
	queueSpecialization := g.partitionSpecializationQueue != nil && !localCapture
	specializedType := g.specializedFuncTypes[specializedName]
	if queueSpecialization {
		candidate := g.cachedSpecializedFuncType(base, typeBindings)
		if specializedType != nil && !sameSpecializedFunctionType(specializedType, candidate) {
			return nil, nil, errPartitionDeclined
		}
		specializedType = candidate
	} else if specializedType == nil {
		specializedType = g.cachedSpecializedFuncType(base, typeBindings)
	}
	if specializedType == nil {
		return nil, nil, errPartitionDeclined
	}
	g.specializedFuncTypes[specializedName] = specializedType
	if queueSpecialization {
		if !g.partitionSpecializationQueue.hasName(specializedName) && g.partitionNativeNameReserved(specializedName) {
			return nil, nil, errPartitionDeclined
		}
		if err := g.partitionSpecializationQueue.addFunction(g.result, specializedName, decl, base, specializedType, typeBindings, g.partitionSpecializationParts); err != nil {
			return nil, nil, err
		}
	}
	if existing, ok := g.functions[specializedName]; ok {
		if localCapture && !g.partLocalHelpers[llvmValueName(existing)] {
			return nil, nil, errPartitionDeclined
		}
		if queueSpecialization {
			if g.partLocalHelpers[llvmValueName(existing)] {
				return nil, nil, errPartitionDeclined
			}
			if g.partBody == nil || g.partBody(specializedName) {
				if C.LLVMCountBasicBlocks(existing) == 0 {
					if err := g.defineFunctionBodyWithBindings(decl, specializedType, existing, typeBindings); err != nil {
						return nil, nil, err
					}
				}
				g.markPartitionSpecializationDefined(specializedName)
			}
		}
		return existing, specializedType, nil
	}
	if localCapture && C.LLVMGetNamedFunction(g.module, cStringFree(specializedName)) != nil {
		return nil, nil, errPartitionDeclined
	}
	fnValue, err := g.addFunction(specializedName, specializedType)
	if err != nil {
		return nil, nil, err
	}
	g.setDefinedFunctionLinkage(specializedName, fnValue, specializedType)
	if localCapture {
		C.LLVMSetLinkage(fnValue, C.LLVMPrivateLinkage)
		if g.partLocalHelpers == nil {
			g.partLocalHelpers = make(map[string]bool)
		}
		g.partLocalHelpers[llvmValueName(fnValue)] = true
	}
	g.functions[specializedName] = fnValue
	if queueSpecialization && g.partBody != nil && !g.partBody(specializedName) {
		return fnValue, specializedType, nil
	}
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, nil, err
	}
	if queueSpecialization {
		g.markPartitionSpecializationDefined(specializedName)
	}
	return fnValue, specializedType, nil
}

// ensureSpecializedImplMethod monomorphizes a parametric static-impl method. Unlike a
// generic function, the method's type params come from the enclosing `impl[T] ...`, not the
// method's own GenericParams, so args are ordered by implTypeParams. Otherwise it mirrors
// ensureSpecializedFunction: mangle a per-binding name, specialize the signature, and emit
// the body with the bindings in scope so references to T resolve. Returns the specialized
// symbol name so callers can declare/call it.
func (g *llvmGenerator) ensureSpecializedImplMethod(decl *ast.FuncDecl, base *semantic.FuncType, implTypeParams []string, owner *semantic.StaticImpl, methodName string, typeBindings map[string]semantic.Type) (C.LLVMValueRef, string, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, "", nil, fmt.Errorf("impl-method specialization requires a function declaration and type")
	}
	orderedArgs := make([]semantic.Type, 0, len(implTypeParams))
	for _, name := range implTypeParams {
		bound, ok := typeBindings[name]
		if !ok {
			return nil, "", nil, fmt.Errorf("missing specialization binding for impl type parameter %s in %s", name, decl.Name)
		}
		orderedArgs = append(orderedArgs, bound)
	}
	specializationBase := decl.Name
	if base.Name != "" {
		specializationBase = base.Name
	}
	var err error
	specializationBase, err = genericImplSpecializationBase(specializationBase, owner, methodName)
	if err != nil {
		return nil, "", nil, err
	}

	specializedName, err := g.mangleGenericType(specializationBase, orderedArgs)
	if err != nil {
		return nil, "", nil, err
	}
	localCapture := false
	if g.partSyntheticDepth > 0 {
		var safe bool
		localCapture, safe = g.partSubmitTypes.classify(orderedArgs)
		if !safe {
			return nil, "", nil, errPartitionDeclined
		}
	}
	if localCapture && g.partitionNativeNameReserved(specializedName) {
		return nil, "", nil, errPartitionDeclined
	}
	queueSpecialization := g.partitionSpecializationQueue != nil && !localCapture
	specializedType := g.specializedFuncTypes[specializedName]
	if queueSpecialization {
		candidate := g.cachedSpecializedFuncType(base, typeBindings)
		if specializedType != nil && !sameSpecializedFunctionType(specializedType, candidate) {
			return nil, "", nil, errPartitionDeclined
		}
		specializedType = candidate
	} else if specializedType == nil {
		specializedType = g.cachedSpecializedFuncType(base, typeBindings)
	}
	if specializedType == nil {
		return nil, "", nil, errPartitionDeclined
	}
	g.specializedFuncTypes[specializedName] = specializedType
	if queueSpecialization {
		if !g.partitionSpecializationQueue.hasName(specializedName) && g.partitionNativeNameReserved(specializedName) {
			return nil, "", nil, errPartitionDeclined
		}
		if err := g.partitionSpecializationQueue.addImplMethod(g.result, specializedName, decl, base, specializedType, implTypeParams, owner, methodName, typeBindings, g.partitionSpecializationParts); err != nil {
			return nil, "", nil, err
		}
	}
	if existing, ok := g.functions[specializedName]; ok {
		if localCapture && !g.partLocalHelpers[llvmValueName(existing)] {
			return nil, "", nil, errPartitionDeclined
		}
		if queueSpecialization {
			if g.partLocalHelpers[llvmValueName(existing)] {
				return nil, "", nil, errPartitionDeclined
			}
			if g.partBody == nil || g.partBody(specializedName) {
				if C.LLVMCountBasicBlocks(existing) == 0 {
					if err := g.defineFunctionBodyWithBindings(decl, specializedType, existing, typeBindings); err != nil {
						return nil, "", nil, err
					}
				}
				g.markPartitionSpecializationDefined(specializedName)
			}
		}
		return existing, specializedName, specializedType, nil
	}
	if localCapture && C.LLVMGetNamedFunction(g.module, cStringFree(specializedName)) != nil {
		return nil, "", nil, errPartitionDeclined
	}
	fnValue, err := g.addFunction(specializedName, specializedType)
	if err != nil {
		return nil, "", nil, err
	}
	g.setDefinedFunctionLinkage(specializedName, fnValue, specializedType)
	if localCapture {
		C.LLVMSetLinkage(fnValue, C.LLVMPrivateLinkage)
		if g.partLocalHelpers == nil {
			g.partLocalHelpers = make(map[string]bool)
		}
		g.partLocalHelpers[llvmValueName(fnValue)] = true
	}
	g.functions[specializedName] = fnValue
	if queueSpecialization && g.partBody != nil && !g.partBody(specializedName) {
		return fnValue, specializedName, specializedType, nil
	}
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, "", nil, err
	}
	if queueSpecialization {
		g.markPartitionSpecializationDefined(specializedName)
	}
	return fnValue, specializedName, specializedType, nil
}

func (g *llvmGenerator) ensureSpecializedExternFunction(decl *ast.ExternFuncDecl, base *semantic.FuncType, typeBindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, nil, fmt.Errorf("generic extern specialization requires a function declaration and type")
	}
	params := g.functionSpecializationParams(base)
	orderedArgs := make([]semantic.Type, 0, len(params))
	for _, param := range params {
		name := param.Name
		bound, ok := typeBindings[name]
		if !ok {
			return nil, nil, fmt.Errorf("missing specialization binding for type parameter %s in extern %s", name, decl.Name)
		}
		orderedArgs = append(orderedArgs, bound)
	}
	specializationBase := decl.Name
	if base.Name != "" {
		specializationBase = base.Name
	}
	specializedName, err := g.mangleGenericType(specializationBase, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	symbolName := legacyGenericExternName(specializationBase, orderedArgs)

	if g.partSyntheticDepth > 0 {
		local, safe := g.partSubmitTypes.classify(orderedArgs)
		if !safe || local {
			return nil, nil, errPartitionDeclined
		}
	}
	specializedType := g.specializedFuncTypes[specializedName]
	if specializedType == nil {
		specializedType = g.cachedSpecializedFuncType(base, typeBindings)
		g.specializedFuncTypes[specializedName] = specializedType
	}
	if err := g.checkGenericExternABI(symbolName, specializedType); err != nil {
		return nil, nil, err
	}
	value, err := g.ensureFunctionDeclared(symbolName, specializedType)
	return value, specializedType, err
}

// explicitFunctionApplicationParams mirrors the source application surface:
// ordinary generics and every explicitly declared region, including lifetimes
// that do not affect layout. Inferred allocation regions are never source args.
func explicitFunctionApplicationParams(base *semantic.FuncType) []ast.GenericParam {
	params := append([]ast.GenericParam(nil), funcGenericParams(base)...)
	seen := map[string]bool{}
	for _, param := range params {
		seen[param.Name] = true
	}
	for _, name := range base.InferredRegionParams {
		seen[name] = true
	}
	for _, name := range base.RegionParams {
		if !seen[name] {
			params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
			seen[name] = true
		}
	}
	return params
}

// functionSpecializationParams includes ordinary type/value generics plus region
// parameters that occur in a region-indexed nominal type. Region-polymorphic
// functions may also carry inferred allocation regions (for example __rg_out)
// that only select a hidden Arena argument; those must remain runtime parameters,
// not become mandatory monomorphization keys.
func functionSpecializationParams(base *semantic.FuncType) []ast.GenericParam {
	if len(base.RegionParams) == 0 {
		// No layout region can be added, so keep the canonical generic-parameter
		// slice read-only and skip the maps and recursive parameter-type walk.
		return funcGenericParams(base)
	}
	params := append([]ast.GenericParam(nil), funcGenericParams(base)...)
	seen := make(map[string]bool, len(params))
	for _, param := range params {
		seen[param.Name] = true
	}
	layoutRegions := make(map[string]bool)
	for _, param := range base.Params {
		collectNominalRegionParams(param, layoutRegions)
	}
	collectNominalRegionParams(base.Return, layoutRegions)
	for _, name := range base.RegionParams {
		if layoutRegions[name] && !seen[name] {
			params = append(params, ast.GenericParam{Kind: ast.GenericParamRegion, Name: name})
			seen[name] = true
		}
	}
	return params
}

func (g *llvmGenerator) functionSpecializationParams(base *semantic.FuncType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.RegionParams) == 0 || g == nil || g.sharedLookups == nil {
		return functionSpecializationParams(base)
	}
	shared := g.sharedLookups.programLookups()
	if params, ok := shared.specializationParams.Load(base); ok {
		return params.([]ast.GenericParam)
	}
	params := functionSpecializationParams(base)
	actual, _ := shared.specializationParams.LoadOrStore(base, params)
	return actual.([]ast.GenericParam)
}

func collectNominalRegionParams(t semantic.Type, regions map[string]bool) {
	if t == nil {
		return
	}
	switch tt := t.(type) {
	case *semantic.GenericInstanceType:
		params := structGenericParams(asStructType(tt.Base))
		for index, param := range params {
			if index >= len(tt.Args) || param.Kind != ast.GenericParamRegion {
				continue
			}
			if region, ok := tt.Args[index].(*semantic.RegionParamType); ok && region != nil {
				regions[region.Name] = true
			}
		}
		for _, arg := range tt.Args {
			collectNominalRegionParams(arg, regions)
		}
	case *semantic.RefType:
		collectNominalRegionParams(tt.Elem, regions)
	case *semantic.ArrayType:
		collectNominalRegionParams(tt.Elem, regions)
	case *semantic.DArrayType:
		collectNominalRegionParams(tt.Elem, regions)
	case *semantic.ViewType:
		collectNominalRegionParams(tt.Elem, regions)
	case *semantic.DictType:
		collectNominalRegionParams(tt.Key, regions)
		collectNominalRegionParams(tt.Value, regions)
	case *semantic.SetType:
		collectNominalRegionParams(tt.Elem, regions)
	case *semantic.OptionalType:
		collectNominalRegionParams(tt.Value, regions)
	case *semantic.ErrorUnionType:
		collectNominalRegionParams(tt.Value, regions)
	case *semantic.TupleType:
		for _, field := range tt.Fields {
			collectNominalRegionParams(field.Type, regions)
		}
	case *semantic.FuncType:
		for _, param := range tt.Params {
			collectNominalRegionParams(param, regions)
		}
		collectNominalRegionParams(tt.Return, regions)
	}
}

func specializeFuncType(base *semantic.FuncType, typeBindings map[string]semantic.Type, impls map[string]*semantic.StaticImpl) *semantic.FuncType {
	if base == nil {
		return nil
	}
	regionParams := make([]string, 0, len(base.RegionParams))
	for _, name := range base.RegionParams {
		if bound, ok := typeBindings[name]; ok {
			if regionName := regionTypeArgumentName(bound); regionName != "" {
				name = regionName
			}
		}
		regionParams = append(regionParams, name)
	}
	params := make([]semantic.Type, 0, len(base.Params))
	for _, param := range base.Params {
		params = append(params, substituteType(param, typeBindings, impls))
	}
	specialized := &semantic.FuncType{
		Name:                        base.Name,
		TypeParams:                  nil,
		RegionParams:                regionParams,
		PermissionParams:            append([]string(nil), base.PermissionParams...),
		GenericParams:               nil,
		ShapeParams:                 append([]string(nil), base.ShapeParams...),
		FreshReturnShapeParams:      append([]string(nil), base.FreshReturnShapeParams...),
		InlineMode:                  base.InlineMode,
		HasInlineMode:               base.HasInlineMode,
		HasNoRecurse:                base.HasNoRecurse,
		HasAsyncEntry:               base.HasAsyncEntry,
		HasSegmentAgnostic:          base.HasSegmentAgnostic,
		HasSegmentEstablishing:      base.HasSegmentEstablishing,
		HasReentrantSafe:            base.HasReentrantSafe,
		SegmentTransition:           base.SegmentTransition,
		TemperatureMode:             base.TemperatureMode,
		HasTemperatureMode:          base.HasTemperatureMode,
		CallConv:                    base.CallConv,
		IsNativeExtern:              base.IsNativeExtern,
		IntrinsicName:               base.IntrinsicName,
		BoundaryPointerParamIndices: append([]int(nil), base.BoundaryPointerParamIndices...),
		SpecSignature:               semantic.CloneSpecSignature(base.SpecSignature),
		RefinementEnsures:           append([]semantic.RefinementEnsure(nil), base.RefinementEnsures...),
		Params:                      params,
		ExplicitParamCount:          base.ExplicitParamCount,
		ExplicitParamNames:          append([]string(nil), base.ExplicitParamNames...),
		ExplicitParamDefaultExprs:   append([]ast.Expr(nil), base.ExplicitParamDefaultExprs...),
		ExplicitParamHasDefault:     append([]bool(nil), base.ExplicitParamHasDefault...),
		ImplicitParamNames:          append([]string(nil), base.ImplicitParamNames...),
		// A specialization has the SAME region-polymorphism as its template: it threads the caller's
		// region via the `__region_auto` implicit param (copied above) and its body must adopt it
		// (regionPolyAutoAdopts checks s.fnType.RegionPolymorphic). Dropping this flag here made every
		// GENERIC region-polymorphic builder create+free its own arena instead of adopting → the
		// returned container's buffer dangled → silent use-after-free. Must mirror the template.
		RegionPolymorphic: base.RegionPolymorphic,
		Return:            substituteType(base.Return, typeBindings, impls),
		Variadic:          base.Variadic,
		SinkParams:        append([]bool(nil), base.SinkParams...),
		SinkParamsKnown:   base.SinkParamsKnown,
	}
	return specialized
}

// cachedSpecializedFuncType shares immutable signature substitution across
// partition generators. The full binding map is part of the key: layout-only
// specialization names intentionally omit region arguments that can still
// affect signature metadata and substituted region names.
func (g *llvmGenerator) cachedSpecializedFuncType(base *semantic.FuncType, typeBindings map[string]semantic.Type) *semantic.FuncType {
	if g == nil || g.result == nil || g.sharedLookups == nil {
		var impls map[string]*semantic.StaticImpl
		if g != nil && g.result != nil {
			impls = g.result.StaticImpls
		}
		return specializeFuncType(base, typeBindings, impls)
	}
	shared := g.sharedLookups.programLookups()
	key, ok := specializedFuncTypeLookupKeyFor(g.result, base, typeBindings)
	if !ok {
		return specializeFuncType(base, typeBindings, g.result.StaticImpls)
	}
	if cached, found := shared.specializedFuncTypes.Load(key); found {
		entry := cached.(specializedFuncTypeCacheValue)
		if specializedFuncTypeCacheValueMatches(key, entry, base, g.result) {
			return entry.specialized
		}
	}
	specialized := specializeFuncType(base, typeBindings, g.result.StaticImpls)
	entry := specializedFuncTypeCacheValue{base: base, result: g.result, specialized: specialized}
	for index := 0; index < int(key.count); index++ {
		entry.names[index] = key.names[index]
		entry.types[index] = typeBindings[key.names[index]]
	}
	actual, loaded := shared.specializedFuncTypes.LoadOrStore(key, entry)
	if !loaded {
		return specialized
	}
	previous := actual.(specializedFuncTypeCacheValue)
	if specializedFuncTypeCacheValueMatches(key, previous, base, g.result) {
		return previous.specialized
	}
	return specialized
}

func specializedFuncTypeLookupKeyFor(result *semantic.Result, base *semantic.FuncType, bindings map[string]semantic.Type) (specializedFuncTypeLookupKey, bool) {
	var key specializedFuncTypeLookupKey
	if result == nil || base == nil || len(bindings) > specializedFuncTypeCacheMaxBindings {
		return key, false
	}
	baseValue, resultValue := reflect.ValueOf(base), reflect.ValueOf(result)
	key.base, key.result = baseValue.Pointer(), resultValue.Pointer()
	count := len(bindings)
	names := key.names[:count]
	index := 0
	for name := range bindings {
		// The map length is bounded above, so filling the fixed array cannot
		// allocate on the lookup path.
		names[index] = name
		index++
	}
	sort.Strings(names)
	key.count = uint8(count)
	for index, name := range names {
		typ := bindings[name]
		if typ == nil {
			return specializedFuncTypeLookupKey{}, false
		}
		value := reflect.ValueOf(typ)
		if value.Kind() != reflect.Ptr || value.IsNil() {
			return specializedFuncTypeLookupKey{}, false
		}
		key.types[index] = value.Pointer()
	}
	return key, true
}

func specializedFuncTypeCacheValueMatches(key specializedFuncTypeLookupKey, entry specializedFuncTypeCacheValue, base *semantic.FuncType, result *semantic.Result) bool {
	if entry.base != base || entry.result != result || entry.specialized == nil {
		return false
	}
	for index := 0; index < int(key.count); index++ {
		typ := entry.types[index]
		if entry.names[index] != key.names[index] || typ == nil {
			return false
		}
		value := reflect.ValueOf(typ)
		if value.Kind() != reflect.Ptr || value.IsNil() || value.Pointer() != key.types[index] {
			return false
		}
	}
	return true
}
