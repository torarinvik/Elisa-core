//go:build cgo

package backend

/*
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/semantic"
	"fmt"
)

func (ops *packedStoreOps) allocateStorage(enumType *semantic.EnumType, totalSizeValue C.LLVMValueRef, hasTail bool, fixedTagValue C.LLVMValueRef, name string) (C.LLVMValueRef, C.LLVMValueRef, C.LLVMValueRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, nil, nil, fmt.Errorf("missing packed enum storage metadata")
	}
	rowSizeValue, err := ops.rowBytesValue("packed.row_bytes")
	if err != nil {
		return nil, nil, nil, err
	}
	switch ops.s.g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, nil, nil, err
		}
		allocResultType := ops.s.g.namedType("PackedStoreIndexAllocResult")
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_index_result"
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_index_result"
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue}
		}
		allocHelperType := ops.cachedRuntimeHelperType(allocHelperName, func() *semantic.FuncType {
			params := []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.s.g.namedType("u32")}
			if hasTail {
				params = []semantic.Type{ops.arenaRefType(), ops.s.g.namedType("usize"), ops.voidRefType()}
			}
			return &semantic.FuncType{Name: allocHelperName, Params: params, Return: allocResultType}
		})
		allocCallee, err := ops.s.g.ensureFunctionDeclared(allocHelperName, allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, allocArgs, "packed.index.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.alloc.ptr"))
		indexValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.alloc.index"))
		enumValue, err := ops.s.coerceValue(indexValue, ops.s.g.namedType("u32"), enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	case packedEnumABIVariantSparse:
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, nil, nil, err
		}
		allocResultType := ops.s.g.namedType("PackedStoreIndexAllocResult")
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for variant-sparse packed enum allocation")
		}
		allocHelperName := "ctx_packed_store_alloc_fixed_tagged_variant_sparse_result"
		allocArgs := []C.LLVMValueRef{arenaValue, stateValue, fixedTagValue}
		if hasTail {
			allocHelperName = "ctx_packed_store_alloc_tagged_variant_sparse_result"
			allocArgs = []C.LLVMValueRef{arenaValue, totalSizeValue, stateValue, fixedTagValue}
		}
		allocHelperType := ops.cachedRuntimeHelperType(allocHelperName, func() *semantic.FuncType {
			params := []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.s.g.namedType("u32")}
			if hasTail {
				params = []semantic.Type{ops.arenaRefType(), ops.s.g.namedType("usize"), ops.voidRefType(), ops.s.g.namedType("u32")}
			}
			return &semantic.FuncType{Name: allocHelperName, Params: params, Return: allocResultType}
		})
		allocCallee, err := ops.s.g.ensureFunctionDeclared(allocHelperName, allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, allocArgs, "packed.variant_sparse.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.alloc.ptr"))
		indexValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.alloc.index"))
		if err := ops.s.emitHandleOverflowGuard(enumType, indexValue); err != nil {
			return nil, nil, nil, err
		}
		enumValue, err := ops.s.coerceValue(indexValue, ops.s.g.namedType("u32"), enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	case packedEnumABIAoS:
		// docs/76 Slice 2: bump one node record onto the contiguous record array; the caller then
		// stores the full rowType (tag + common) at the returned record ptr and the payload after it,
		// exactly as for the other modes — so AoS needs no tag column and no tag write here.
		if hasTail {
			return nil, nil, nil, fmt.Errorf("packed enum AoS layout does not support tail payloads")
		}
		arenaValue, err := ops.arenaValue("packed.arena")
		if err != nil {
			return nil, nil, nil, err
		}
		stateValue, err := ops.stateValue("packed.state")
		if err != nil {
			return nil, nil, nil, err
		}
		allocResultType := ops.s.g.namedType("PackedStoreIndexAllocResult")
		if allocResultType == nil {
			return nil, nil, nil, fmt.Errorf("missing builtin PackedStoreIndexAllocResult type for AoS packed enum allocation")
		}
		allocHelperType := ops.cachedRuntimeHelperType("ctx_aos_store_alloc", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "ctx_aos_store_alloc", Params: []semantic.Type{ops.arenaRefType(), ops.voidRefType()}, Return: allocResultType}
		})
		allocCallee, err := ops.s.g.ensureFunctionDeclared("ctx_aos_store_alloc", allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocLLVMFnType, err := ops.s.g.lowerFunctionType(allocHelperType)
		if err != nil {
			return nil, nil, nil, err
		}
		allocResult := ops.s.buildCall(allocLLVMFnType, allocCallee, []C.LLVMValueRef{arenaValue, stateValue}, "packed.aos.alloc")
		allocPtr := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 0, cStringFree("packed.aos.alloc.ptr"))
		indexValue := C.LLVMBuildExtractValue(ops.s.builder, allocResult, 1, cStringFree("packed.aos.alloc.index"))
		if enumType.HandleIsPointer() {
			// docs/82 `handle: ptr`: the handle is the record address itself (the AoS chunk
			// store never relocates, so the address is stable for the store's lifetime).
			handleType, err := ops.s.g.lowerType(enumType)
			if err != nil {
				return nil, nil, nil, err
			}
			handleValue := C.LLVMBuildPtrToInt(ops.s.builder, allocPtr, handleType, cStringFree("packed.aos.alloc.handle"))
			return allocPtr, handleValue, rowSizeValue, nil
		}
		if err := ops.s.emitHandleOverflowGuard(enumType, indexValue); err != nil {
			return nil, nil, nil, err
		}
		enumValue, err := ops.s.coerceValue(indexValue, ops.s.g.namedType("u32"), enumType)
		if err != nil {
			return nil, nil, nil, err
		}
		return allocPtr, enumValue, rowSizeValue, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported packed enum ABI mode %d", ops.s.g.packedModeForEnum(enumType))
	}
}

// emitHandleOverflowGuard is the docs/76/82 loud-overflow check for narrowed handles: when the
// root's `layout(handle: uN)` width is below the runtime's native u32 index, an allocation whose
// index reaches the width's null sentinel would silently truncate into a wrong (or null) handle —
// so it traps at the allocation site instead, the same discipline as reserve_commit exhaustion.
// Default-width (u32) and wider stores emit nothing: the runtime index can never exceed them.
func (s *functionState) emitHandleOverflowGuard(enumType *semantic.EnumType, indexValue C.LLVMValueRef) error {
	if enumType == nil {
		return nil
	}
	root := enumType.Root()
	bits := root.ResolvedIndexWidthBits()
	if bits >= 32 {
		return nil
	}
	sentinel := C.LLVMConstInt(C.LLVMTypeOf(indexValue), C.ulonglong(root.NullSentinelValue()), 0)
	fits := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, sentinel, cStringFree("handle.fits"))
	okBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("handle.ok"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("handle.overflow"))
	C.LLVMBuildCondBr(s.builder, fits, okBB, failBB)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if err := s.emitTrapUnreachable("handle.overflow.trap"); err != nil {
		return err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, okBB)
	return nil
}

func (ops *packedStoreOps) recordTag(tagValue C.LLVMValueRef, _ string) error {
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	tagType := ops.s.g.namedType("u32")
	recordType := ops.cachedRuntimeHelperType("ctx_packed_store_record_tag", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_record_tag", Params: []semantic.Type{ops.arenaRefType(), tagType, ops.voidRefType()}, Return: ops.s.g.namedType("void")}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_tag", recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, tagValue, stateValue}, "")
	return nil
}
func (ops *packedStoreOps) recordPrefixWords(rowPtr C.LLVMValueRef, _ string) error {
	if ops == nil || ops.s == nil || ops.s.g == nil || ops.s.g.packedLoweringForStore(ops.storeType) != packedEnumABIIndexSOA {
		return nil
	}
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	recordType := ops.cachedRuntimeHelperType("ctx_packed_store_record_prefix_words", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "ctx_packed_store_record_prefix_words", Params: []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.voidRefType()}, Return: ops.s.g.namedType("void")}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared("ctx_packed_store_record_prefix_words", recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, rowPtr, stateValue}, "")
	return nil
}
func (ops *packedStoreOps) recordSideWords(wordsPtr C.LLVMValueRef, handleValue C.LLVMValueRef, _ string) error {
	if ops == nil || ops.s == nil || ops.s.g == nil || ops.storeType == nil || ops.storeType.Enum == nil {
		return nil
	}
	if !packedModeUsesDenseIndexHandle(ops.s.g.packedLoweringForStore(ops.storeType)) {
		return nil
	}
	arenaValue, err := ops.arenaValue("packed.arena")
	if err != nil {
		return err
	}
	stateValue, err := ops.stateValue("packed.state")
	if err != nil {
		return err
	}
	// The write is addressed by the node's OWN handle: a parent's record is allocated before its
	// payload args (children) are emitted, so an append/"last allocated" contract would land the
	// parent's commons on the last child. AoS stores write lockstep side chunks; column stores
	// write side columns — same (arena, words, state, index) contract.
	u32Type := ops.s.g.namedType("u32")
	indexValue, err := ops.s.coerceValue(handleValue, ops.storeType.Enum, u32Type)
	if err != nil {
		return err
	}
	helperName := "ctx_packed_store_record_side_words_at"
	if packedModeIsAoS(ops.s.g.packedLoweringForStore(ops.storeType)) {
		helperName = "ctx_aos_store_record_side_words_at"
	}
	recordType := ops.cachedRuntimeHelperType(helperName, func() *semantic.FuncType {
		return &semantic.FuncType{Name: helperName, Params: []semantic.Type{ops.arenaRefType(), ops.voidRefType(), ops.voidRefType(), u32Type}, Return: ops.s.g.namedType("void")}
	})
	recordCallee, err := ops.s.g.ensureFunctionDeclared(helperName, recordType)
	if err != nil {
		return err
	}
	recordLLVMType, err := ops.s.g.lowerFunctionType(recordType)
	if err != nil {
		return err
	}
	ops.s.buildCall(recordLLVMType, recordCallee, []C.LLVMValueRef{arenaValue, wordsPtr, stateValue, indexValue}, "")
	return nil
}
