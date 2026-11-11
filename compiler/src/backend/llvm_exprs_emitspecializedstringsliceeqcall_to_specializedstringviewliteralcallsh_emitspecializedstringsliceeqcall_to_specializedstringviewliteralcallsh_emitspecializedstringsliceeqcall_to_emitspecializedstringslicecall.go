//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"unsafe"
)

func (s *functionState) emitSpecializedStringSliceEqCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice_eq" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 4 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftType := s.exprType(leftExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareCStr {
		return nil, nil, false, nil
	}
	start, ok := s.staticIntLiteral(expr.Args[1])
	if !ok || start < 0 {
		return nil, nil, false, nil
	}
	end, ok := s.staticIntLiteral(expr.Args[2])
	if !ok || end < start {
		return nil, nil, false, nil
	}
	rightExpr := expr.Args[3]
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareCStr {
		return nil, nil, false, nil
	}
	intType := s.g.namedType("int")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, start, end, "strsliceeq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightLen, rightLenType, rightKind, err := s.emitRuntimeStringCompareOperand(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if rightKind != runtimeStringCompareCStr || rightLen == nil || rightLenType == nil {
		return nil, nil, false, nil
	}
	rightLenI64, err := s.coerceValue(rightLen, rightLenType, s.g.namedType("i64"))
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftSliceLen, rightLenI64, cStringFree("strsliceeq.len.eq"))
	sliceZero := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftSliceLen, llvmConstInt(llvmInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceeq.len.zero"))
	dataEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftData, rightData, cStringFree("strsliceeq.data.eq"))
	usizeType := s.g.namedType("usize")
	lenValue, err := s.coerceValue(leftSliceLen, s.g.namedType("i64"), usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := llvmGetInsertBlock(s.builder)
	zeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.zero"))
	nonZeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.nonzero"))
	sameBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.same"))
	memcmpBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.memcmp"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceeq.merge"))
	llvmBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := llvmGetInsertBlock(s.builder)
	llvmBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	llvmPositionBuilderAtEnd(s.builder, nonZeroBB)
	llvmBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	llvmPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.semanticReadView().ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceeq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := llvmInt1TypeInContext(s.g.context)
	phi := llvmBuildPhi(s.builder, boolType, cStringFree("strsliceeq.result"))
	falseValue := llvmConstInt(boolType, 0, 0)
	trueValue := llvmConstInt(boolType, 1, 0)
	values := []llvmIRValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []llvmIRBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return llvmBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceeq.int")), intType, true, nil
}
func (s *functionState) emitSpecializedStringSlicesEqCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slices_eq" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 6 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	leftStartExpr := expr.Args[1]
	leftEndExpr := expr.Args[2]
	rightExpr := expr.Args[3]
	rightStartExpr := expr.Args[4]
	rightEndExpr := expr.Args[5]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if classifyRuntimeStringCompareKind(leftType) != runtimeStringCompareCStr || classifyRuntimeStringCompareKind(rightType) != runtimeStringCompareCStr {
		return nil, nil, false, nil
	}
	leftStart, ok := s.staticIntLiteral(leftStartExpr)
	if !ok || leftStart < 0 {
		return nil, nil, false, nil
	}
	leftEnd, ok := s.staticIntLiteral(leftEndExpr)
	if !ok || leftEnd < leftStart {
		return nil, nil, false, nil
	}
	rightStart, ok := s.staticIntLiteral(rightStartExpr)
	if !ok || rightStart < 0 {
		return nil, nil, false, nil
	}
	rightEnd, ok := s.staticIntLiteral(rightEndExpr)
	if !ok || rightEnd < rightStart {
		return nil, nil, false, nil
	}
	intType := s.g.namedType("int")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData, leftSliceLen, err := s.emitConstantClampedStringSliceOperand(leftExpr, leftType, leftStart, leftEnd, "strsliceseq.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightData, rightSliceLen, err := s.emitConstantClampedStringSliceOperand(rightExpr, rightType, rightStart, rightEnd, "strsliceseq.right")
	if err != nil {
		return nil, nil, true, err
	}
	lenEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftSliceLen, rightSliceLen, cStringFree("strsliceseq.len.eq"))
	sliceZero := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftSliceLen, llvmConstInt(llvmInt64TypeInContext(s.g.context), 0, 0), cStringFree("strsliceseq.len.zero"))
	dataEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftData, rightData, cStringFree("strsliceseq.data.eq"))
	usizeType := s.g.namedType("usize")
	lenValue, err := s.coerceValue(leftSliceLen, s.g.namedType("i64"), usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	entryBlock := llvmGetInsertBlock(s.builder)
	zeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.zero"))
	nonZeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.nonzero"))
	sameBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.same"))
	memcmpBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.memcmp"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("strsliceseq.merge"))
	llvmBuildCondBr(s.builder, lenEqual, zeroBB, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, zeroBB)
	zeroEnd := llvmGetInsertBlock(s.builder)
	llvmBuildCondBr(s.builder, sliceZero, mergeBB, nonZeroBB)

	llvmPositionBuilderAtEnd(s.builder, nonZeroBB)
	llvmBuildCondBr(s.builder, dataEqual, sameBB, memcmpBB)

	llvmPositionBuilderAtEnd(s.builder, sameBB)
	sameEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, memcmpBB)
	disjoint := s.g.semanticReadView().ExprsAreDisjoint(leftExpr, rightExpr)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, lenValue, "strsliceseq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := llvmInt1TypeInContext(s.g.context)
	phi := llvmBuildPhi(s.builder, boolType, cStringFree("strsliceseq.result"))
	falseValue := llvmConstInt(boolType, 0, 0)
	trueValue := llvmConstInt(boolType, 1, 0)
	values := []llvmIRValueRef{falseValue, trueValue, trueValue, cmp}
	blocks := []llvmIRBlockRef{entryBlock, zeroEnd, sameEnd, memcmpEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return llvmBuildZExt(s.builder, phi, intLLVMType, cStringFree("strsliceseq.int")), intType, true, nil
}
func (s *functionState) emitSpecializedRuntimeStringCompareCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	switch ident.Name {
	case "ctx_streq", "ctx_string_view_eq", "string_view_eq", "ctx_string_views_eq", "string_views_eq":
	default:
		return nil, nil, false, nil
	}
	if !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	if ident.Name == "ctx_streq" {
		if literalText, ok := s.staticCStringLiteral(rightExpr); ok {
			cmp, err := s.emitCStrStaticLiteralEqual(leftExpr, leftType, rightExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.namedType("int")
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return llvmBuildZExt(s.builder, cmp, intLLVMType, cStringFree("cstrlit.direct.int")), intType, true, nil
		}
		if literalText, ok := s.staticCStringLiteral(leftExpr); ok {
			cmp, err := s.emitCStrStaticLiteralEqual(rightExpr, rightType, leftExpr, literalText)
			if err != nil {
				return nil, nil, true, err
			}
			intType := s.g.namedType("int")
			intLLVMType, err := s.g.lowerType(intType)
			if err != nil {
				return nil, nil, true, err
			}
			return llvmBuildZExt(s.builder, cmp, intLLVMType, cStringFree("cstrlit.direct.int")), intType, true, nil
		}
	}
	cmp, ok, err := s.emitSameExtentRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	if !ok {
		cmp, ok, err = s.emitDisjointRuntimeStringCompareExpr(lexer.TOKEN_EQEQ, leftExpr, leftType, rightExpr, rightType)
		if err != nil {
			return nil, nil, true, err
		}
		if !ok {
			return nil, nil, false, nil
		}
	}
	intType := s.g.namedType("int")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return llvmBuildZExt(s.builder, cmp, intLLVMType, cStringFree("streq.direct.int")), intType, true, nil
}
func isStringViewCarrierType(t semantic.Type) bool {
	return classifyRuntimeStringCompareKind(t) == runtimeStringCompareView
}
func (s *functionState) emitGlobalCStringLiteral(text string, name string) llvmIRValueRef {
	return s.emitGlobalStringBytes(text, name)
}

// emitGlobalStringBytes emits a private string constant holding EVERY byte of text plus a
// terminating NUL, and returns its address. LLVMBuildGlobalStringPtr takes a C string, so a literal
// with an embedded NUL (`"ab\0cd"`) was cut off at it: the global held `ab` while every length
// derived from the literal — an sview's len, a folded `.len` — still said 5, and reading the tail
// read past the end of the global. Otherwise the IR is exactly LLVMBuildGlobalStringPtr's: a
// private unnamed_addr constant of align 1, whose address is the global itself.
func (s *functionState) emitGlobalStringBytes(text string, name string) llvmIRValueRef {
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	data := llvmCBytes(append([]byte(text), 0))
	defer llvmFree(data)
	initializer := llvmConstStringInContext(s.g.context, (*llvmIRChar)(data), llvmIRUnsigned(len(text)), 0)
	global := llvmAddGlobal(s.g.module, llvmTypeOf(initializer), nameC)
	llvmSetLinkage(global, llvmPrivateLinkage)
	llvmSetGlobalConstant(global, 1)
	llvmSetUnnamedAddress(global, llvmGlobalUnnamedAddr)
	llvmSetInitializer(global, initializer)
	llvmSetAlignment(global, 1)
	return global
}
func (s *functionState) emitInternSmallStringCall(data llvmIRValueRef, lenValue llvmIRValueRef, name string) (llvmIRValueRef, error) {
	u8Type := s.g.namedType("u8")
	usizeType := s.g.namedType("usize")
	srcType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	retType := &semantic.RefType{Elem: u8Type, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	helperType := &semantic.FuncType{Name: "intern_small_string", Params: []semantic.Type{srcType, usizeType}, Return: retType}
	callee, err := s.g.ensureFunctionDeclared("intern_small_string", helperType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, err
	}
	return s.buildCall(llvmFnType, callee, []llvmIRValueRef{data, lenValue}, name), nil
}
func (s *functionState) emitDirectStringViewCopyLarge(viewData llvmIRValueRef, viewLen llvmIRValueRef) (llvmIRValueRef, error) {
	i64Type := s.g.namedType("i64")
	usizeType := s.g.namedType("usize")
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{i64Type}, Return: heapVoidRefType}
	allocCallee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	oneValue := llvmConstInt(llvmInt64TypeInContext(s.g.context), 1, 0)
	allocSize := llvmBuildAdd(s.builder, viewLen, oneValue, cStringFree("svcopy.alloc.size"))
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []llvmIRValueRef{allocSize}, "svcopy.alloc")

	lenUsize, err := s.coerceValue(viewLen, i64Type, usizeType)
	if err != nil {
		return nil, err
	}
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []llvmIRValueRef{allocPtr, viewData, lenUsize}, "svcopy.memcpy")

	i8LLVMType, err := s.g.lowerBuiltin("u8")
	if err != nil {
		return nil, err
	}
	bytePtr := llvmBuildGEP2(s.builder, i8LLVMType, allocPtr, llvmValueSlicePtr([]llvmIRValueRef{lenUsize}), 1, cStringFree("svcopy.term.ptr"))
	zeroByte := llvmConstInt(i8LLVMType, 0, 0)
	llvmBuildStore(s.builder, zeroByte, bytePtr)

	return allocPtr, nil
}
func (s *functionState) emitSpecializedStringSliceCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "ctx_string_slice" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	resultType := s.exprType(expr)
	inputExpr := expr.Args[0]
	inputType := s.exprType(inputExpr)
	if _, ok := inputType.(*semantic.CStrType); !ok {
		return nil, nil, false, nil
	}
	if s.g.semanticReadView().ExprsHaveSameExtent(expr, inputExpr) {
		value, _, err := s.emitExpr(inputExpr, inputType)
		return value, resultType, true, err
	}
	sliceFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(expr)
	if !ok || !sliceFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	exactLen, ok := constOptimizationExtentSize(sliceFacts.Extent)
	if !ok {
		return nil, nil, false, nil
	}
	begin, ok := parseOptimizationExtentConstInt(sliceFacts.Extent.Begin)
	if !ok || begin < 0 {
		return nil, nil, false, nil
	}
	inputValue, _, err := s.emitExpr(inputExpr, inputType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	beginValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(begin), 0)
	sliceData := inputValue
	if begin != 0 {
		i8LLVMType, err := s.g.lowerBuiltin("u8")
		if err != nil {
			return nil, nil, true, err
		}
		indices := []llvmIRValueRef{beginValue}
		sliceData = llvmBuildGEP2(s.builder, i8LLVMType, inputValue, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("strslice.data"))
	}
	lenValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(exactLen), 0)
	if exactLen == 0 {
		emptyPtr := s.emitGlobalCStringLiteral("", "strslice.empty")
		value, err := s.emitInternSmallStringCall(emptyPtr, lenValue, "strslice.zero.small")
		return value, resultType, true, err
	}
	if exactLen <= 8 {
		value, err := s.emitInternSmallStringCall(sliceData, lenValue, "strslice.small")
		return value, resultType, true, err
	}
	largeLen := llvmConstInt(llvmInt64TypeInContext(s.g.context), llvmIRUnsignedLongLong(exactLen), 0)
	value, err := s.emitDirectStringViewCopyLarge(sliceData, largeLen)
	return value, resultType, true, err
}
