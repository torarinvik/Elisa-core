//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

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
	storeType := semantic.PackedEnumStoreWithState(root.StoreType, s.g.result.NamedTypes["Local"])
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
	fnType := C.LLVMGlobalGetValueType(getter)
	value := C.LLVMBuildCall2(s.builder, fnType, getter, nil, 0, cStringFree("program.store"))
	return packedStoreBinding{value: value, typ: storeType}, true, nil
}

func (s *functionState) emitProgramStoreGetter(name string, root *semantic.EnumType, storeType *semantic.PackedEnumStoreType, storeLLVMType C.LLVMTypeRef) (C.LLVMValueRef, error) {
	g := s.g
	globalName := C.CString("__elisa_program_store." + root.Name)
	defer C.free(unsafe.Pointer(globalName))
	global := C.LLVMAddGlobal(g.module, storeLLVMType, globalName)
	C.LLVMSetInitializer(global, C.LLVMConstNull(storeLLVMType))
	C.LLVMSetLinkage(global, C.LLVMLinkOnceAnyLinkage)

	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))
	fnType := C.LLVMFunctionType(storeLLVMType, nil, 0, 0)
	fn := C.LLVMAddFunction(g.module, nameC, fnType)
	C.LLVMSetLinkage(fn, C.LLVMLinkOnceAnyLinkage)

	builder := C.LLVMCreateBuilderInContext(g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("entry"))
	initBB := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("init"))
	readyBB := C.LLVMAppendBasicBlockInContext(g.context, fn, cStringFree("ready"))
	C.LLVMPositionBuilderAtEnd(builder, entry)
	current := C.LLVMBuildLoad2(builder, storeLLVMType, global, cStringFree("store"))
	state := C.LLVMBuildExtractValue(builder, current, 2, cStringFree("state"))
	isNull := C.LLVMBuildIsNull(builder, state, cStringFree("uninit"))
	C.LLVMBuildCondBr(builder, isNull, initBB, readyBB)
	C.LLVMPositionBuilderAtEnd(builder, readyBB)
	C.LLVMBuildRet(builder, current)

	C.LLVMPositionBuilderAtEnd(builder, initBB)
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
	C.LLVMBuildStore(builder, created, global)
	C.LLVMBuildRet(builder, created)
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
	arenaType := s.g.result.NamedTypes["Arena"]
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
	nameC := C.CString(key)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddGlobal(s.g.module, llvmType, nameC)
	C.LLVMSetInitializer(value, C.LLVMConstNull(llvmType))
	C.LLVMSetLinkage(value, C.LLVMLinkOnceAnyLinkage)
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
