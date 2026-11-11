//go:build cgo

package backend

import (
	"fmt"
	"unsafe"

	"elisacore/src/semantic"
)

// programPackedStore returns the node store of a store-backed enum whose root declares a
// program-lived store region (docs/128 §8). There is one store per root for the whole
// program: a linkonce global holds it, and a linkonce getter creates it on first use on the
// program arena. Every function reaches the same store, so handles stay mutually valid.
func (s *functionState) programPackedStore(enumType *semantic.EnumType) (packedStoreBinding, bool, error) {
	if s == nil || enumType == nil || enumType.DeclaredStoreRegion() == "" {
		return packedStoreBinding{}, false, nil
	}
	root := enumType.Root()
	if root.StoreType == nil {
		return packedStoreBinding{}, false, fmt.Errorf("packed enum %s is missing store layout metadata", root.Name)
	}
	storeType := semantic.PackedEnumStoreWithState(root.StoreType, s.g.namedType("Local"))
	storeLLVMType, err := s.g.lowerPackedEnumStoreType(storeType)
	if err != nil {
		return packedStoreBinding{}, false, err
	}
	getterName := "__elisa_program_store_get." + root.Name
	getter, ok := s.g.globals[getterName]
	if !ok {
		getter, err = s.emitProgramStoreGetter(getterName, root, storeType, storeLLVMType)
		if err != nil {
			return packedStoreBinding{}, false, err
		}
		s.g.globals[getterName] = getter
	}
	fnType := llvmGlobalGetValueType(getter)
	value := llvmBuildCall2(s.builder, fnType, getter, nil, 0, cStringFree("program.store"))
	return packedStoreBinding{value: value, typ: storeType}, true, nil
}

func (s *functionState) emitProgramStoreGetter(name string, root *semantic.EnumType, storeType *semantic.PackedEnumStoreType, storeLLVMType llvmIRTypeRef) (llvmIRValueRef, error) {
	g := s.g
	globalName := llvmCString("__elisa_program_store." + root.Name)
	defer llvmFree(unsafe.Pointer(globalName))
	global := llvmAddGlobal(g.module, storeLLVMType, globalName)
	llvmSetInitializer(global, llvmConstNull(storeLLVMType))
	llvmSetLinkage(global, llvmLinkOnceAnyLinkage)

	nameC := llvmCString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	fnType := llvmFunctionType(storeLLVMType, nil, 0, 0)
	fn := llvmAddFunction(g.module, nameC, fnType)
	llvmSetLinkage(fn, llvmLinkOnceAnyLinkage)

	builder := llvmCreateBuilderInContext(g.context)
	defer llvmDisposeBuilder(builder)
	entry := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
	initBB := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("init"))
	readyBB := llvmAppendBasicBlockInContext(g.context, fn, cStringFree("ready"))
	llvmPositionBuilderAtEnd(builder, entry)
	current := llvmBuildLoad2(builder, storeLLVMType, global, cStringFree("store"))
	state := llvmBuildExtractValue(builder, current, 2, cStringFree("state"))
	isNull := llvmBuildIsNull(builder, state, cStringFree("uninit"))
	llvmBuildCondBr(builder, isNull, initBB, readyBB)
	llvmPositionBuilderAtEnd(builder, readyBB)
	llvmBuildRet(builder, current)

	llvmPositionBuilderAtEnd(builder, initBB)
	arena, ok, err := s.programRegionArena(root.DeclaredStoreRegion())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("enum %s: store region %q is not a program-lived region", root.Name, root.DeclaredStoreRegion())
	}
	inner := &functionState{g: g, fnValue: fn, builder: builder, fnType: &semantic.FuncType{Name: name}}
	created, err := inner.emitPackedStoreValueFromArenaPtr(arena.ptr, storeType)
	if err != nil {
		return nil, err
	}
	llvmBuildStore(builder, created, global)
	llvmBuildRet(builder, created)
	return fn, nil
}

// programRegionArena returns the arena of a program-lived region (docs/128 section 5): one
// zero-initialized Arena global per region name. A zero Arena is a valid lazy arena (the
// first allocation creates its first block), it is never reset or freed, and the OS
// releases it at exit. linkonce merges the definitions emitted by separately compiled
// objects into one arena.
func (s *functionState) programRegionArena(name string) (valueBinding, bool, error) {
	if s == nil || s.g == nil || !s.g.result.IsProgramRegion(name) {
		return valueBinding{}, false, nil
	}
	arenaType := s.g.namedType("Arena")
	if arenaType == nil {
		return valueBinding{}, false, fmt.Errorf("missing builtin Arena type for program-lived region %s", name)
	}
	key := "__elisa_program_region." + name
	if value, ok := s.g.globals[key]; ok {
		return valueBinding{ptr: value, typ: arenaType}, true, nil
	}
	llvmType, err := s.g.lowerType(arenaType)
	if err != nil {
		return valueBinding{}, false, err
	}
	nameC := llvmCString(key)
	defer llvmFree(unsafe.Pointer(nameC))
	value := llvmAddGlobal(s.g.module, llvmType, nameC)
	llvmSetInitializer(value, llvmConstNull(llvmType))
	llvmSetLinkage(value, llvmLinkOnceAnyLinkage)
	s.g.applyTypeAlignment(value, arenaType)
	s.g.globals[key] = value
	return valueBinding{ptr: value, typ: arenaType}, true, nil
}

// programStoreHoldsPayload: a darray payload whose backing (and every nested container
// backing) is already typed in the declared program-lived store region needs no adoption copy
// -- the program arena is the store's arena and is never freed (docs/128 §8).
func programStoreHoldsPayload(enumType *semantic.EnumType, argType semantic.Type) bool {
	if enumType == nil {
		return false
	}
	region := enumType.DeclaredStoreRegion()
	darrayType, ok := argType.(*semantic.DArrayType)
	if region == "" || !ok || darrayType == nil || darrayType.Region != region {
		return false
	}
	return nestedContainersInRegion(darrayType.Elem, region, map[semantic.Type]bool{})
}

func nestedContainersInRegion(t semantic.Type, region string, seen map[semantic.Type]bool) bool {
	if t == nil || seen[t] {
		return true
	}
	seen[t] = true
	switch tt := t.(type) {
	case *semantic.DArrayType:
		return tt.Region == region && nestedContainersInRegion(tt.Elem, region, seen)
	case *semantic.DictType, *semantic.SetType:
		return false
	case *semantic.StructType:
		for _, field := range tt.Fields {
			if !nestedContainersInRegion(field.Type, region, seen) {
				return false
			}
		}
	case *semantic.OptionalType:
		return nestedContainersInRegion(tt.Value, region, seen)
	}
	return true
}
