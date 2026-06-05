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

// emitBuiltinDArraySviewCall lowers `d.sview()` for `d: darray[u8] @r` to a
// bounded StringView `{data, len}` value: data = the darray's items pointer,
// len = count. Read-only; no allocation. Replaces the unsafe
// `out[0].ref[static u8&]` idiom with a length-carrying view.
func (s *functionState) emitBuiltinDArraySviewCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "as_sview" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.SViewType)
	if !ok || resultType == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	darrayPtr, _, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	dataValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.sview.data"))
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	countValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.sview.count"))
	sviewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	// StringView is { ptr data, i64 len }; coerce count to the len field type.
	lenFieldType := C.LLVMStructGetTypeAtIndex(sviewLLVMType, 1)
	lenValue := countValue
	if lenFieldType != usizeLLVMType {
		lenValue = C.LLVMBuildIntCast2(s.builder, countValue, lenFieldType, 0, cStringFree("darray.sview.len"))
	}
	view := C.LLVMGetUndef(sviewLLVMType)
	view = C.LLVMBuildInsertValue(s.builder, view, dataValue, 0, cStringFree("darray.sview.insert.data"))
	view = C.LLVMBuildInsertValue(s.builder, view, lenValue, 1, cStringFree("darray.sview.insert.len"))
	return view, resultType, true, nil
}

// emitBuiltinDArrayCstrCall lowers `d.cstr()` for `d: darray[u8] @r` to a
// NUL-terminated C-string pointer. A bare darray is not NUL-terminated, so this
// is NOT a reinterpret: it ensures capacity for count+1 and writes a 0 sentinel
// at items[count] WITHOUT changing count (std::string::c_str() semantics), then
// returns the items pointer. The capacity bump sources the darray's region arena
// (regionArenaOwner, ambient fallback) — exactly like a push.
func (s *functionState) emitBuiltinDArrayCstrCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "as_cstr" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.DStrType)
	if !ok || resultType == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	owner, ok := s.darrayGrowthOwner(fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("darray cstr requires an active in <arena>: scope to NUL-terminate")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.cstr.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, _, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.cstr.count"))
	neededValue := C.LLVMBuildAdd(s.builder, currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree("darray.cstr.needed"))
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.cstr"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.cstr.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("darray.cstr.nul.slot"))
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(elemLLVMType, 0, 0), slotPtr)
	// A cstr lowers to an opaque pointer; the items pointer (start of bytes) is
	// the C-string, now NUL-terminated at [count].
	return itemsValue, resultType, true, nil
}
