//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

// emitBuiltinDArraySviewCall lowers `d.sview()` for `d: darray[u8] @r` to a
// bounded StringView `{data, len}` value: data = the darray's items pointer,
// len = count. Read-only; no allocation. Replaces the unsafe
// `out[0].ref[static u8&]` idiom with a length-carrying view.
func (s *functionState) emitBuiltinDArraySviewCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
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
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	dataValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.sview.data"))
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	countValue := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.sview.count"))
	// An empty darray may have a nil items pointer, but StringView.data is a
	// non-null reference even when len is zero. Give empty views a process-lived
	// NUL byte rather than allowing a null pointer into the StringView carrier.
	empty := llvmBuildICmp(
		s.builder,
		llvmIRIntPredicate(llvmIntEQ),
		countValue,
		llvmConstInt(usizeLLVMType, 0, 0),
		cStringFree("darray.sview.empty"),
	)
	emptyBacking := llvmBuildGlobalStringPtr(s.builder, cStringFree(""), cStringFree("darray.sview.empty.backing"))
	dataValue = llvmBuildSelect(s.builder, empty, emptyBacking, dataValue, cStringFree("darray.sview.data.valid"))
	sviewLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	// StringView is { ptr data, i64 len }; coerce count to the len field type.
	lenFieldType := llvmStructGetTypeAtIndex(sviewLLVMType, 1)
	lenValue := countValue
	if lenFieldType != usizeLLVMType {
		lenValue = llvmBuildIntCast2(s.builder, countValue, lenFieldType, 0, cStringFree("darray.sview.len"))
	}
	view := llvmGetUndef(sviewLLVMType)
	view = llvmBuildInsertValue(s.builder, view, dataValue, 0, cStringFree("darray.sview.insert.data"))
	view = llvmBuildInsertValue(s.builder, view, lenValue, 1, cStringFree("darray.sview.insert.len"))
	return view, resultType, true, nil
}

// emitBuiltinDArrayCstrCall lowers `d.cstr()` for `d: darray[u8] @r` to a
// NUL-terminated C-string pointer. A bare darray is not NUL-terminated, so this
// is NOT a reinterpret: it ensures capacity for count+1 and writes a 0 sentinel
// at items[count] WITHOUT changing count (std::string::c_str() semantics), then
// returns the items pointer. The capacity bump sources the darray's region arena
// (regionArenaOwner, ambient fallback) — exactly like a push.
func (s *functionState) emitBuiltinDArrayCstrCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "as_cstr" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	resultType, ok := s.exprType(expr).(*semantic.CStrType)
	if !ok || resultType == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	owner, ok := s.darrayGrowthOwner(expr, fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("%s", semantic.NoCstrCopyRegionMessage())
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
	currentCount := llvmBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.cstr.count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, llvmConstInt(usizeLLVMType, 1, 0), "darray.cstr.needed")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.cstr"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := llvmPointerTypeInContext(s.g.context, 0)
	itemsValue := llvmBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.cstr.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	slotPtr := llvmBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]llvmIRValueRef{currentCount}), 1, cStringFree("darray.cstr.nul.slot"))
	llvmBuildStore(s.builder, llvmConstInt(elemLLVMType, 0, 0), slotPtr)
	// A cstr lowers to an opaque pointer; the items pointer (start of bytes) is
	// the C-string, now NUL-terminated at [count].
	return itemsValue, resultType, true, nil
}

// emitInlineStringViewIndex matches string_view_index on 64-bit targets. Keep
// the byte load behind the bounds branch so an empty view or an index at the
// end never touches the backing pointer. LLVM can remove the check in loops
// whose index is already bounded by the view length.
func (s *functionState) emitInlineStringViewIndex(view, index llvmIRValueRef, resultType semantic.Type) (llvmIRValueRef, error) {
	resultLLVM, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, err
	}
	ctx := s.g.context
	length := llvmBuildExtractValue(s.builder, view, 1, cStringFree("sview.index.len"))
	validLength := llvmBuildICmp(s.builder, llvmIntSGE, length, llvmConstNull(llvmTypeOf(length)), cStringFree("sview.index.valid.len"))
	// With a nonnegative length, an unsigned comparison also rejects negative indexes.
	validIndex := llvmBuildICmp(s.builder, llvmIntULT, index, length, cStringFree("sview.index.in.range"))
	valid := llvmBuildAnd(s.builder, validLength, validIndex, cStringFree("sview.index.valid"))
	origin := llvmGetInsertBlock(s.builder)
	read := llvmAppendBasicBlockInContext(ctx, s.fnValue, cStringFree("sview.index.read"))
	join := llvmAppendBasicBlockInContext(ctx, s.fnValue, cStringFree("sview.index.join"))
	llvmBuildCondBr(s.builder, valid, read, join)
	llvmPositionBuilderAtEnd(s.builder, read)
	data := llvmBuildExtractValue(s.builder, view, 0, cStringFree("sview.index.data"))
	byteType := llvmInt8TypeInContext(ctx)
	indices := []llvmIRValueRef{index}
	address := llvmBuildGEP2(s.builder, byteType, data, llvmValueSlicePtr(indices), 1, cStringFree("sview.index.ptr"))
	byteValue := llvmBuildLoad2(s.builder, byteType, address, cStringFree("sview.index.byte"))
	value := llvmBuildZExt(s.builder, byteValue, resultLLVM, cStringFree("sview.index.wide"))
	llvmBuildBr(s.builder, join)
	llvmPositionBuilderAtEnd(s.builder, join)
	result := llvmBuildPhi(s.builder, resultLLVM, cStringFree("sview.index"))
	values := []llvmIRValueRef{llvmConstNull(resultLLVM), value}
	blocks := []llvmIRBlockRef{origin, read}
	llvmAddIncoming(result, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), 2)
	return result, nil
}
