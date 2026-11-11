//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (s *functionState) emitSpecializedStringViewCopyCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || (ident.Name != "string_view_copy" && ident.Name != "ctx_string_from_view") {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	viewExpr := expr.Args[0]
	viewType := s.exprType(viewExpr)
	resultType := s.exprType(expr)
	if !isStringViewCarrierType(viewType) {
		return nil, nil, false, nil
	}
	viewFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(viewExpr)
	if !ok || !viewFacts.HasExactExtent() {
		return nil, nil, false, nil
	}
	viewValue, _, err := s.emitExpr(viewExpr, viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewData := llvmBuildExtractValue(s.builder, viewValue, 0, cStringFree("svcopy.data"))
	viewLen := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree("svcopy.len"))
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	if exactLen, ok := constOptimizationExtentSize(viewFacts.Extent); ok {
		lenValue := llvmConstInt(usizeLLVMType, llvmIRUnsignedLongLong(exactLen), 0)
		if exactLen <= 8 {
			dataValue := viewData
			if exactLen == 0 {
				dataValue = s.emitGlobalCStringLiteral("", "svcopy.empty")
			}
			value, err := s.emitInternSmallStringCall(dataValue, lenValue, "svcopy.small")
			return value, resultType, true, err
		}
		largeLen := llvmConstInt(llvmInt64TypeInContext(s.g.context), llvmIRUnsignedLongLong(exactLen), 0)
		value, err := s.emitDirectStringViewCopyLarge(viewData, largeLen)
		return value, resultType, true, err
	}

	i64LLVMType := llvmInt64TypeInContext(s.g.context)
	zeroLen := llvmConstInt(i64LLVMType, 0, 0)
	eightLen := llvmConstInt(i64LLVMType, 8, 0)
	zeroCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntSLE), viewLen, zeroLen, cStringFree("svcopy.len.zero"))
	positiveBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.positive"))
	zeroBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.zero"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.merge"))
	entryBlock := llvmGetInsertBlock(s.builder)
	llvmBuildCondBr(s.builder, zeroCond, zeroBB, positiveBB)

	llvmPositionBuilderAtEnd(s.builder, zeroBB)
	emptyPtr := s.emitGlobalCStringLiteral("", "svcopy.empty")
	zeroSmall, err := s.emitInternSmallStringCall(emptyPtr, llvmConstInt(usizeLLVMType, 0, 0), "svcopy.zero.small")
	if err != nil {
		return nil, nil, true, err
	}
	zeroEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, positiveBB)
	smallCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntSLE), viewLen, eightLen, cStringFree("svcopy.len.small"))
	smallBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.small"))
	largeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("svcopy.large"))
	llvmBuildCondBr(s.builder, smallCond, smallBB, largeBB)

	llvmPositionBuilderAtEnd(s.builder, smallBB)
	viewLenUsize, err := s.coerceValue(viewLen, s.g.namedType("i64"), usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	smallValue, err := s.emitInternSmallStringCall(viewData, viewLenUsize, "svcopy.small")
	if err != nil {
		return nil, nil, true, err
	}
	smallEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, largeBB)
	largeValue, err := s.emitDirectStringViewCopyLarge(viewData, viewLen)
	if err != nil {
		return nil, nil, true, err
	}
	largeEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	phi := llvmBuildPhi(s.builder, llvmResultType, cStringFree("svcopy.result"))
	values := []llvmIRValueRef{zeroSmall, smallValue, largeValue}
	blocks := []llvmIRBlockRef{zeroEnd, smallEnd, largeEnd}
	_ = entryBlock
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitSpecializedStringViewLiteralCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, false, nil
	}
	viewArgIndex, literalArgIndex, returnsInt, ok := s.specializedStringViewLiteralCallShape(ident.Name)
	if !ok {
		return nil, nil, false, nil
	}
	literalText, ok := s.staticCStringLiteral(expr.Args[literalArgIndex])
	if !ok {
		return nil, nil, false, nil
	}
	viewType := s.exprType(expr.Args[viewArgIndex])
	if classifyRuntimeStringCompareKind(viewType) != runtimeStringCompareView {
		return nil, nil, false, nil
	}
	cmp, err := s.emitStringViewStaticLiteralEqual(expr.Args[viewArgIndex], viewType, expr.Args[literalArgIndex], literalText)
	if err != nil {
		return nil, nil, true, err
	}
	if !returnsInt {
		return cmp, s.exprType(expr), true, nil
	}
	intType := s.g.namedType("int")
	intLLVMType, err := s.g.lowerType(intType)
	if err != nil {
		return nil, nil, true, err
	}
	return llvmBuildZExt(s.builder, cmp, intLLVMType, cStringFree("svlit.eq.int")), intType, true, nil
}
func (s *functionState) specializedStringViewLiteralCallShape(funcName string) (int, int, bool, bool) {
	if funcName == "string_view_eq" || funcName == "ctx_string_view_eq" {
		return 0, 1, true, true
	}
	sym, ok := s.g.lookupGlobalForInputRead(funcName, "literal-wrapper-resolution")
	if !ok {
		return 0, 0, false, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || len(decl.Params) != 2 {
		return 0, 0, false, false
	}
	body := s.g.functionBodyForInputRead(decl, "literal-wrapper-stability")
	if len(body) != 1 {
		return 0, 0, false, false
	}
	ret, ok := body[0].(*ast.ReturnStmt)
	if !ok || ret.Value == nil {
		return 0, 0, false, false
	}
	callExpr, ok := s.boolStringViewLiteralWrapperCall(ret.Value, decl.Params[0].Name, decl.Params[1].Name)
	if !ok || callExpr == nil {
		return 0, 0, false, false
	}
	callee, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		return 0, 0, false, false
	}
	if callee.Name != "string_view_eq" && callee.Name != "ctx_string_view_eq" {
		return 0, 0, false, false
	}
	return 0, 1, false, true
}
