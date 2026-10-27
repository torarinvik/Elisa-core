//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"reflect"
	"sort"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (g *llvmGenerator) ensureSpecializedFunction(decl *ast.FuncDecl, base *semantic.FuncType, typeBindings map[string]semantic.Type) (C.LLVMValueRef, *semantic.FuncType, error) {
	if decl == nil || base == nil {
		return nil, nil, fmt.Errorf("generic specialization requires a function declaration and type")
	}
	params := g.functionSpecializationParams(base)
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
	specializedType := g.specializedFuncTypes[specializedName]
	if specializedType == nil {
		specializedType = g.cachedSpecializedFuncType(base, typeBindings)
		g.specializedFuncTypes[specializedName] = specializedType
	}
	if existing, ok := g.functions[specializedName]; ok {
		if localCapture && !g.partLocalHelpers[llvmValueName(existing)] {
			return nil, nil, errPartitionDeclined
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
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, nil, err
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
	specializedType := g.specializedFuncTypes[specializedName]
	if specializedType == nil {
		specializedType = g.cachedSpecializedFuncType(base, typeBindings)
		g.specializedFuncTypes[specializedName] = specializedType
	}
	if existing, ok := g.functions[specializedName]; ok {
		if localCapture && !g.partLocalHelpers[llvmValueName(existing)] {
			return nil, "", nil, errPartitionDeclined
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
	if err := g.defineFunctionBodyWithBindings(decl, specializedType, fnValue, typeBindings); err != nil {
		return nil, "", nil, err
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

func dynSetRuntimeInstance(t semantic.Type) (*semantic.GenericInstanceType, bool) {
	gi, ok := t.(*semantic.GenericInstanceType)
	if !ok || gi.Name != "DynSet" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

// bindBackendErrorSetParamContribution mirrors the analyzer's join-aware
// error-set-param binding so monomorphization sees the identical set. Repeat
// encounters union; an empty contribution (infallible callback) is the unit.
func bindBackendErrorSetParamContribution(bindings map[string]semantic.Type, name string, contribution *semantic.ErrorSetType) {
	if contribution == nil {
		return
	}
	if existing, bound := bindings[name].(*semantic.ErrorSetType); bound {
		bindings[name] = semantic.UnionErrorSets(existing, contribution)
		return
	}
	if _, ok := bindings[name]; !ok {
		bindings[name] = contribution
	}
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
	if patternDynSet, ok := dynSetRuntimeInstance(pattern); ok {
		if actualSet, ok := actual.(*semantic.SetType); ok {
			collectSpecializationBindings(patternDynSet.Args[0], actualSet.Elem, bindings)
			return
		}
	}
	if patternSet, ok := pattern.(*semantic.SetType); ok {
		if actualDynSet, ok := dynSetRuntimeInstance(actual); ok {
			collectSpecializationBindings(patternSet.Elem, actualDynSet.Args[0], bindings)
			return
		}
	}
	switch p := pattern.(type) {
	case *semantic.RegionParamType:
		if actualRegion := regionTypeArgumentName(actual); actualRegion != "" {
			if _, exists := bindings[p.Name]; !exists {
				bindings[p.Name] = &semantic.RegionValueType{Name: actualRegion}
			}
		}
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
	case *semantic.IDType:
		if a, ok := actual.(*semantic.IDType); ok {
			collectSpecializationBindings(p.Tag, a.Tag, bindings)
			collectSpecializationBindings(p.Storage, a.Storage, bindings)
		}
	case *semantic.AddressSpaceType:
		if a, ok := actual.(*semantic.AddressSpaceType); ok && p.Space == a.Space {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
			collectSpecializationBindings(p.Storage, a.Storage, bindings)
		}
	case *semantic.RefType:
		if a, ok := actual.(*semantic.RefType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		} else {
			// Parameter is `T&` but the argument is a value, auto-ref'd at the call site
			// (e.g. `write_span(byte[0], n)` against `data: T&`). Bind the element type
			// parameter against the value's own type so T specializes to the value type.
			collectSpecializationBindings(p.Elem, actual, bindings)
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
	case *semantic.DArrayType:
		if a, ok := actual.(*semantic.DArrayType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.ViewType:
		if a, ok := actual.(*semantic.ViewType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.DictType:
		if a, ok := actual.(*semantic.DictType); ok {
			collectSpecializationBindings(p.Key, a.Key, bindings)
			collectSpecializationBindings(p.Value, a.Value, bindings)
		}
	case *semantic.SetType:
		if a, ok := actual.(*semantic.SetType); ok {
			collectSpecializationBindings(p.Elem, a.Elem, bindings)
		}
	case *semantic.GenericInstanceType:
		if a, ok := actual.(*semantic.GenericInstanceType); ok && p.Name == a.Name && len(p.Args) == len(a.Args) {
			for i := range p.Args {
				collectSpecializationBindings(p.Args[i], a.Args[i], bindings)
			}
		}
	case *semantic.ErrorUnionType:
		if a, ok := actual.(*semantic.ErrorUnionType); ok {
			collectSpecializationBindings(p.Value, a.Value, bindings)
			if p.Errors != nil && len(p.Errors.Params) == 1 && a.Errors != nil {
				bindBackendErrorSetParamContribution(bindings, p.Errors.Params[0], semantic.SubtractErrorTags(a.Errors, p.Errors))
			}
		} else {
			// Infallible argument bound into an `error[R]` pattern -> R := empty.
			if p.Errors != nil && len(p.Errors.Params) == 1 {
				bindBackendErrorSetParamContribution(bindings, p.Errors.Params[0], &semantic.ErrorSetType{Name: "error[]"})
			}
			collectSpecializationBindings(p.Value, actual, bindings)
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

// orderedGenericTypeArgs returns the concrete type arguments in DECLARATION order, which
// is the order semantic.withGenericParams binds them in. The bindings map is keyed by
// parameter name and therefore unordered; ordering here keeps the analyzer's binding of
// `T` matched to the argument the backend specialized on.
//
// Falls back to the declaration's own generic params when the specialized signature no
// longer carries them (specializeFuncType clears them once bound).
func orderedGenericTypeArgs(fnType *semantic.FuncType, decl *ast.FuncDecl, bindings map[string]semantic.Type) []semantic.Type {
	if len(bindings) == 0 {
		return nil
	}
	params := funcGenericParams(fnType)
	if len(params) == 0 && decl != nil {
		params = decl.GenericParams
	}
	args := make([]semantic.Type, 0, len(params))
	for _, param := range params {
		bound, ok := bindings[param.Name]
		if !ok {
			return nil
		}
		args = append(args, bound)
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

// Legacy external spellings remain ABI names, not specialization authority.
// Distinct structural arguments may share one spelling only when the current
// lowered signature, calling convention and ABI parameter attributes agree.
func (g *llvmGenerator) checkGenericExternABI(name string, fn *semantic.FuncType) error {
	cname := C.CString(g.llvmSymbolName(name))
	defer C.free(unsafe.Pointer(cname))
	existing := C.LLVMGetNamedFunction(g.module, cname)
	if existing == nil {
		return nil
	}
	if err := g.ensureDeclaredFunctionType(name, existing, fn); err != nil {
		return err
	}
	cc, _ := g.llvmCallConvForFunc(fn)
	if C.LLVMGetFunctionCallConv(existing) != cc {
		return fmt.Errorf("generic extern ABI calling convention conflict for %s", name)
	}
	typ, err := g.lowerFunctionTypeForSymbol(name, fn)
	if err != nil {
		return err
	}
	probeName := C.CString(".elisa.generic.abi.probe")
	defer C.free(unsafe.Pointer(probeName))
	probe := C.LLVMAddFunction(g.module, probeName, typ)
	defer C.LLVMDeleteFunction(probe)
	g.applyAggregateAbiAttrs(probe, fn)
	for index := C.LLVMAttributeIndex(0); index <= C.LLVMAttributeIndex(C.LLVMCountParams(probe)); index++ {
		count := C.LLVMGetAttributeCountAtIndex(existing, index)
		if count != C.LLVMGetAttributeCountAtIndex(probe, index) {
			return fmt.Errorf("generic extern ABI attribute conflict for %s", name)
		}
		if count == 0 {
			continue
		}
		left := make([]C.LLVMAttributeRef, int(count))
		right := make([]C.LLVMAttributeRef, int(count))
		C.LLVMGetAttributesAtIndex(existing, index, &left[0])
		C.LLVMGetAttributesAtIndex(probe, index, &right[0])
		have := make(map[C.LLVMAttributeRef]bool, len(left))
		for _, attr := range left {
			have[attr] = true
		}
		for _, attr := range right {
			if !have[attr] {
				return fmt.Errorf("generic extern ABI attribute conflict for %s", name)
			}
		}
	}
	return nil
}

func genericImplSpecializationBase(base string, owner *semantic.StaticImpl, method string) (string, error) {
	if owner == nil {
		return "", fmt.Errorf("missing generic impl owner")
	}
	receiver, err := semantic.TypeSymbolKey(owner.Receiver)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%s|%d:%s|%d:%s|%s", len(owner.InterfaceName), owner.InterfaceName, len(receiver), receiver, len(method), method, base), nil
}
