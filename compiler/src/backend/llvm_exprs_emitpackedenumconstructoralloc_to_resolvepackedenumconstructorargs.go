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
)

func (s *functionState) emitPackedEnumConstructorAlloc(callExpr *ast.CallExpr, storeValue C.LLVMValueRef, enumType *semantic.EnumType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string) (C.LLVMValueRef, semantic.Type, error) {
	if enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("missing packed enum constructor metadata")
	}
	orderedArgs, commonArgs, err := s.resolvePackedEnumConstructorArgs(callExpr, enumType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("enum constructor %s.%s expects %d arguments, got %d", enumType.Name, variant.Name, len(variant.Payload), len(args))
	}
	tagValue, err := s.enumTagConstant(enumType, variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	if s.canInlinePackedEnumVariant(enumType, variant) {
		var payloadValue C.LLVMValueRef
		var payloadType semantic.Type
		if len(orderedArgs) == 1 {
			payloadType = variant.Payload[0]
			payloadValue, _, err = s.emitExpr(orderedArgs[0], payloadType)
			if err != nil {
				return nil, nil, err
			}
		}
		inlineHandle, err := s.buildInlinePackedEnumHandle(tagValue, payloadValue, payloadType)
		if err != nil {
			return nil, nil, err
		}
		return inlineHandle, enumType, nil
	}
	rowType, err := s.g.ensurePackedEnumStorageType(enumType)
	if err != nil {
		return nil, nil, err
	}
	sideWords, err := s.g.packedEnumCommonSideTableWordCount(enumType)
	if err != nil {
		return nil, nil, err
	}
	tailPlan, err := s.preparePackedEnumTailPayloadPlan(variant, orderedArgs)
	if err != nil {
		return nil, nil, err
	}
	allocPtr, enumValue, rowSizeValue, err := s.emitPackedEnumStorageAlloc(storeValue, enumType, tailPlan, tagValue)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMConstNull(rowType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, tagValue, 0, cStringFree("packed.enum.tag.ins"))
	commonValues := make(map[string]C.LLVMValueRef, len(enumType.Root().Decl.Common))
	for _, commonDecl := range enumType.Root().Decl.Common { // docs/77: common(...) lives on the root
		arg, ok := commonArgs[commonDecl.Name]
		if !ok {
			continue
		}
		layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
		if err != nil {
			return nil, nil, err
		}
		fieldValue, _, err := s.emitExpr(arg, layout.Field.Type)
		if err != nil {
			return nil, nil, err
		}
		commonValues[commonDecl.Name] = fieldValue
		if layout.StoredInline {
			rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, fieldValue, C.unsigned(layout.RowFieldIndex), cStringFree("packed.enum.common.ins"))
		}
	}
	rowStore := C.LLVMBuildStore(s.builder, rowValue, allocPtr)
	s.g.setInstrAlignment(rowStore, s.g.payloadSlotBytes(enumType))
	if len(variant.Payload) > 0 {
		payloadPtr, err := s.enumPayloadPtr(allocPtr, enumType)
		if err != nil {
			return nil, nil, err
		}
		var tailDataPtr C.LLVMValueRef
		if tailPlan != nil {
			tailDataPtr, err = s.emitPackedEnumTailDataPtr(allocPtr, rowSizeValue)
			if err != nil {
				return nil, nil, err
			}
		}
		if len(variant.Payload) == 1 {
			argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, 0, orderedArgs[0], tailPlan, tailDataPtr, storeValue, enumType)
			if err != nil {
				return nil, nil, err
			}
			if !llvmValueIsZeroConstant(argValue) {
				st := C.LLVMBuildStore(s.builder, argValue, payloadPtr)
				s.g.setInstrAlignment(st, s.g.payloadSlotBytes(enumType))
			}
		} else {
			payloadType, err := s.g.lowerEnumVariantPayloadType(variant)
			if err != nil {
				return nil, nil, err
			}
			aggregate := C.LLVMGetUndef(payloadType)
			allZero := true
			for i, payload := range variant.Payload {
				_ = payload
				argValue, err := s.emitPackedEnumConstructorPayloadValue(variant, i, orderedArgs[i], tailPlan, tailDataPtr, storeValue, enumType)
				if err != nil {
					return nil, nil, err
				}
				if !llvmValueIsZeroConstant(argValue) {
					allZero = false
				}
				aggregate = C.LLVMBuildInsertValue(s.builder, aggregate, argValue, C.unsigned(i), cStringFree("packed.enum.payload.ins"))
			}
			if !allZero {
				st := C.LLVMBuildStore(s.builder, aggregate, payloadPtr)
				s.g.setInstrAlignment(st, s.g.payloadSlotBytes(enumType))
			}
		}
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	if sideWords > 0 {
		wordBytes := uint64(s.g.wordBits / 8)
		if wordBytes == 0 {
			wordBytes = 8
		}
		sideBufferType := &semantic.ArrayType{Elem: s.g.namedType("uintptr"), HasConstSize: true, ConstSize: int64(sideWords)}
		sideBufferPtr, err := s.createEntryAlloca("packed.side.words", sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		sideBufferLLVMType, err := s.g.lowerType(sideBufferType)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMBuildStore(s.builder, C.LLVMConstNull(sideBufferLLVMType), sideBufferPtr)
		for _, commonDecl := range enumType.Root().Decl.Common { // docs/77: common(...) lives on the root
			layout, err := s.g.packedEnumCommonFieldLayout(enumType, commonDecl.Name)
			if err != nil {
				return nil, nil, err
			}
			if layout.StoredInline {
				continue
			}
			fieldValue, ok := commonValues[commonDecl.Name]
			if !ok {
				continue
			}
			if err := s.emitPackSideTableFieldValue(sideBufferPtr, layout.SideWordOffset*wordBytes, fieldValue, layout.Field.Type, "packed.side.pack"); err != nil {
				return nil, nil, err
			}
		}
		if err := ops.recordSideWords(sideBufferPtr, enumValue, "packed.side.record"); err != nil {
			return nil, nil, err
		}
	}
	if err := ops.recordPrefixWords(allocPtr, "packed.prefix.record"); err != nil {
		return nil, nil, err
	}
	mode := s.g.packedModeForEnum(enumType)
	if mode != packedEnumABIVariantSparse && tailPlan != nil {
		if err := s.emitPackedStoreRecordTag(storeValue, enumType.StoreType, tagValue); err != nil {
			return nil, nil, err
		}
	}
	return enumValue, enumType, nil
}
func (s *functionState) canInlinePackedEnumVariant(enumType *semantic.EnumType, variant *semantic.EnumVariant) bool {
	return false
}
func (s *functionState) buildInlinePackedEnumHandle(tagValue C.LLVMValueRef, payloadValue C.LLVMValueRef, payloadType semantic.Type) (C.LLVMValueRef, error) {
	uintptrLLVMType, err := s.g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, err
	}
	handleValue := C.LLVMBuildZExt(s.builder, tagValue, uintptrLLVMType, cStringFree("packed.inline.tag.zext"))
	handleValue = C.LLVMBuildShl(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 49, 0), cStringFree("packed.inline.tag.shift"))
	if payloadValue != nil && payloadType != nil {
		payloadBits := C.LLVMBuildZExt(s.builder, payloadValue, uintptrLLVMType, cStringFree("packed.inline.payload.zext"))
		payloadBits = C.LLVMBuildShl(s.builder, payloadBits, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.payload.shift"))
		handleValue = C.LLVMBuildOr(s.builder, handleValue, payloadBits, cStringFree("packed.inline.payload.or"))
	}
	return C.LLVMBuildOr(s.builder, handleValue, C.LLVMConstInt(uintptrLLVMType, 1, 0), cStringFree("packed.inline.handle")), nil
}
func (s *functionState) emitPackedStoreRecordTag(storeValue C.LLVMValueRef, storeType *semantic.PackedEnumStoreType, tagValue C.LLVMValueRef) error {
	if storeType == nil {
		return fmt.Errorf("missing packed store type for tag record")
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
	return ops.recordTag(tagValue, "packed.tag.record")
}

type packedEnumTailPayloadPlan struct {
	index         int
	viewType      *semantic.ViewType
	elemSizeValue C.LLVMValueRef
	lenValue      C.LLVMValueRef
	byteCount     C.LLVMValueRef
	sourceData    C.LLVMValueRef
	literal       *ast.ListLitExpr
}

func (s *functionState) preparePackedEnumTailPayloadPlan(variant *semantic.EnumVariant, orderedArgs []ast.Expr) (*packedEnumTailPayloadPlan, error) {
	if variant == nil {
		return nil, nil
	}
	tailIndex, ok := variant.TailPayloadIndex()
	if !ok {
		return nil, nil
	}
	viewType, ok := variant.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, fmt.Errorf("packed enum %s tail payload metadata is inconsistent", variant.Name)
	}
	if tailIndex >= len(orderedArgs) {
		return nil, fmt.Errorf("packed enum %s tail payload argument is missing", variant.Name)
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(viewType.Elem)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	arg := orderedArgs[tailIndex]
	if literal, ok := arg.(*ast.ListLitExpr); ok {
		lenValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(len(literal.Elems)), 0)
		byteCount := C.LLVMConstInt(usizeLLVMType, C.ulonglong(uint64(len(literal.Elems))*elemSize), 0)
		return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, literal: literal}, nil
	}
	sourceType := s.exprType(arg)
	if _, ok := sourceType.(*semantic.ViewType); !ok {
		if _, ok := sourceType.(*semantic.ViewType); !ok {
			return nil, fmt.Errorf("packed enum %s tail payload expects a list literal or view-compatible source, got %s", variant.Name, sourceType.String())
		}
	}
	viewValue, _, err := s.emitExpr(arg, sourceType)
	if err != nil {
		return nil, err
	}
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("packed.tail.src.len"))
	sourceData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("packed.tail.src.data"))
	byteCount, err := s.emitCheckedElemByteCount(lenValue, elemSize, "packed.tail")
	if err != nil {
		return nil, err
	}
	return &packedEnumTailPayloadPlan{index: tailIndex, viewType: viewType, elemSizeValue: elemSizeValue, lenValue: lenValue, byteCount: byteCount, sourceData: sourceData}, nil
}
func (s *functionState) emitPackedEnumTailDataPtr(allocPtr C.LLVMValueRef, rowSizeValue C.LLVMValueRef) (C.LLVMValueRef, error) {
	i8Type, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	indices := []C.LLVMValueRef{rowSizeValue}
	return C.LLVMBuildGEP2(s.builder, i8Type, allocPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("packed.tail.data")), nil
}
func (s *functionState) emitPackedEnumConstructorPayloadValue(variant *semantic.EnumVariant, index int, arg ast.Expr, tailPlan *packedEnumTailPayloadPlan, tailDataPtr C.LLVMValueRef, storeValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	if tailPlan != nil && index == tailPlan.index {
		return s.emitPackedEnumTailPayloadValue(tailPlan, tailDataPtr)
	}
	argValue, argType, err := s.emitExpr(arg, variant.Payload[index])
	if err != nil {
		return nil, err
	}
	if nicheEnumType, ok := s.g.optionalEnumNicheField(variant.Payload[index]); ok {
		return s.packOptionalEnumNicheValue(argValue, nicheEnumType, "packed.enum.payload")
	}
	if programStoreHoldsPayload(enumType, argType) {
		return argValue, nil // docs/128 §8: already in the store's program arena; nothing to adopt
	}
	return s.emitPackedEnumPayloadDArrayAdopt(argValue, variant.Payload[index], storeValue, enumType)
}

// emitPackedEnumPayloadDArrayAdopt copies a darray payload's backing into the packed store's
// arena before the darray struct is written into the record. A side-table darray (`Call.arguments`,
// `Func.body`, ...) is usually BUILT in the constructing function's own region — region inference
// sees the ctor return only a u32 handle, so nothing marks the backing as escaping, and once the
// builder's region is freed the record holds a dangling data pointer (the comprehension-filter
// parse crash: the payload's `arguments` pointed one page past its freed chunk). The ctor consumes
// the darray affinely (consumeAffineValueExpr), so a copy cannot break aliasing; capacity clamps to
// count — a later grow re-allocates in the ambient region instead of writing the store arena.
// Elements are copied SHALLOWLY: handle/scalar/sview elements (every AST side-table today) are
// self-contained; an element type that itself owns region storage still needs its own adoption.
func (s *functionState) emitPackedEnumPayloadDArrayAdopt(argValue C.LLVMValueRef, payloadType semantic.Type, storeValue C.LLVMValueRef, enumType *semantic.EnumType) (C.LLVMValueRef, error) {
	darrayType, ok := payloadType.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return argValue, nil
	}
	if enumType == nil || enumType.StoreType == nil || storeValue == nil {
		return argValue, nil
	}
	elemSize, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, err
	}
	countValue := C.LLVMBuildExtractValue(s.builder, argValue, 1, cStringFree("packed.payload.adopt.count"))
	oldData := C.LLVMBuildExtractValue(s.builder, argValue, 0, cStringFree("packed.payload.adopt.src"))
	byteCount, err := s.emitCheckedElemByteCount(countValue, elemSize, "packed.payload.adopt")
	if err != nil {
		return nil, err
	}
	ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: enumType.StoreType}
	arenaValue, err := ops.arenaValue("packed.payload.adopt.arena")
	if err != nil {
		return nil, err
	}
	arenaType := s.g.namedType("Arena")
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.namedType("void"), State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.namedType("usize")
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	newData := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaValue, byteCount}, "packed.payload.adopt.alloc")
	if err := s.emitPackedEnumTailMemcpy(newData, oldData, byteCount); err != nil {
		return nil, err
	}
	// DEEP adoption: the shallow memcpy above copies the element structs verbatim, so any OWNED
	// container field inside an element (e.g. `MatchArm.body: darray[Stmt]`) still points at the
	// builder's region, which frees on return — a use-after-free (the self-host next_token crash).
	// Recursively copy each such field's backing into the store arena too. sview/view/handle fields
	// are borrows into persistent buffers and need no adoption.
	if err := s.emitDeepAdoptElementContainers(newData, countValue, darrayType.Elem, arenaValue, allocLLVMType, allocCallee); err != nil {
		return nil, err
	}
	adopted := C.LLVMBuildInsertValue(s.builder, argValue, newData, 0, cStringFree("packed.payload.adopt.data"))
	adopted = C.LLVMBuildInsertValue(s.builder, adopted, countValue, 2, cStringFree("packed.payload.adopt.cap"))
	return adopted, nil
}
