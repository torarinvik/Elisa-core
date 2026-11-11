//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) boolStringViewLiteralWrapperCall(expr ast.Expr, viewParam string, literalParam string) (*ast.CallExpr, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_EQEQ && binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	callExpr, intLit, ok := unwrapCallComparedToZero(binary.Left, binary.Right)
	if !ok {
		callExpr, intLit, ok = unwrapCallComparedToZero(binary.Right, binary.Left)
	}
	if !ok || intLit == nil || intLit.Value != "0" {
		return nil, false
	}
	if binary.Op != lexer.TOKEN_BANGEQ {
		return nil, false
	}
	if !matchesStringViewLiteralWrapperArgs(callExpr, viewParam, literalParam) {
		return nil, false
	}
	return callExpr, true
}
func unwrapCallComparedToZero(left ast.Expr, right ast.Expr) (*ast.CallExpr, *ast.IntLit, bool) {
	callExpr, ok := left.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	intLit, ok := right.(*ast.IntLit)
	if !ok {
		return nil, nil, false
	}
	return callExpr, intLit, true
}
func matchesStringViewLiteralWrapperArgs(callExpr *ast.CallExpr, viewParam string, literalParam string) bool {
	if callExpr == nil || len(callExpr.Args) != 2 {
		return false
	}
	viewIdent, ok := callExpr.Args[0].(*ast.Ident)
	if !ok || viewIdent.Name != viewParam {
		return false
	}
	return exprIsParamOrCastOfParam(callExpr.Args[1], literalParam)
}
func exprIsParamOrCastOfParam(expr ast.Expr, paramName string) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name == paramName
	case *ast.ParenExpr:
		return exprIsParamOrCastOfParam(n.Inner, paramName)
	case *ast.CastExpr:
		return exprIsParamOrCastOfParam(n.Operand, paramName)
	default:
		return false
	}
}
func stripMemcpyOperandExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
	return nil
}
func isMemcpyViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType, *semantic.SViewType:
		return true
	case *semantic.StructType:
		return tt != nil && (tt.Name == "DynArrayView" || tt.Name == "StringView")
	default:
		return false
	}
}
func isDynArrayViewCarrierType(t semantic.Type) bool {
	switch tt := t.(type) {
	case *semantic.ViewType:
		return true
	case *semantic.StructType:
		return tt != nil && tt.Name == "DynArrayView"
	default:
		return false
	}
}
func isDynArrayCarrierType(t semantic.Type) bool {
	switch t.(type) {
	case *semantic.DArrayType:
		return true
	default:
		return false
	}
}
func (s *functionState) memcpyDisjointCarrierExpr(expr ast.Expr) ast.Expr {
	stripped := stripMemcpyOperandExpr(expr)
	fieldExpr, ok := stripped.(*ast.FieldExpr)
	if !ok || fieldExpr.Field != "data" {
		return nil
	}
	if !isMemcpyViewCarrierType(s.exprType(fieldExpr.Object)) {
		return nil
	}
	return fieldExpr.Object
}
func (s *functionState) memcpyOperandsAreDisjoint(destExpr ast.Expr, srcExpr ast.Expr) bool {
	if s == nil || s.g == nil || s.g.result == nil {
		return false
	}
	if s.g.semanticReadView().ExprsAreDisjoint(destExpr, srcExpr) {
		return true
	}
	destCarrier := s.memcpyDisjointCarrierExpr(destExpr)
	srcCarrier := s.memcpyDisjointCarrierExpr(srcExpr)
	if destCarrier == nil || srcCarrier == nil {
		return false
	}
	return s.g.semanticReadView().ExprsAreDisjoint(destCarrier, srcCarrier)
}
func (s *functionState) emitSpecializedMemcpyCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return nil, nil, false, nil
	}
	if ident.Name != "memcpy" && ident.Name != "arena_memcpy" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, false, nil
	}
	callee, funcType, _, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("copy helper target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	args := make([]llvmIRValueRef, 0, len(expr.Args))
	for i, arg := range expr.Args {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		args = append(args, value)
	}
	callName := "calltmp"
	if isVoidType(funcType.Return) {
		callName = ""
	}
	call := s.buildCall(llvmFnType, callee, args, callName)
	if s.memcpyOperandsAreDisjoint(expr.Args[0], expr.Args[1]) {
		s.addCallSiteEnumAttribute(call, llvmIRUnsigned(1), "noalias")
		s.addCallSiteEnumAttribute(call, llvmIRUnsigned(2), "noalias")
	}
	return call, funcType.Return, true, nil
}
func (s *functionState) emitSpecializedArenaViewCopyCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_copy_exact" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	dstExpr := expr.Args[0]
	srcExpr := expr.Args[1]
	dstType := s.exprType(dstExpr)
	srcType := s.exprType(srcExpr)
	if !isDynArrayViewCarrierType(dstType) || !isDynArrayViewCarrierType(srcType) {
		return nil, nil, false, nil
	}
	callee, funcType, _, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if funcType == nil {
		return nil, nil, true, fmt.Errorf("arena_da_copy_exact target does not have a function type")
	}
	exactCopyCount := uint64(0)
	hasSmallExactCopyCount := false
	if dstFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(dstExpr); ok {
		if dstCount, ok := constOptimizationExtentSize(dstFacts.Extent); ok && dstCount <= smallExactArenaCopyUnrollLimit {
			if srcFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(srcExpr); ok {
				if srcCount, ok := constOptimizationExtentSize(srcFacts.Extent); ok && srcCount == dstCount {
					exactCopyCount = dstCount
					hasSmallExactCopyCount = true
				}
			}
		}
	}
	disjoint := s.g.semanticReadView().ExprsAreDisjoint(dstExpr, srcExpr)
	if !hasSmallExactCopyCount && !disjoint {
		return nil, nil, false, nil
	}
	dstValue, _, err := s.emitExpr(dstExpr, dstType)
	if err != nil {
		return nil, nil, true, err
	}
	srcValue, _, err := s.emitExpr(srcExpr, srcType)
	if err != nil {
		return nil, nil, true, err
	}
	if hasSmallExactCopyCount {
		if exactCopyCount == 0 {
			return nil, funcType.Return, true, nil
		}
		var elemType semantic.Type
		switch viewType := funcType.Params[0].(type) {
		case *semantic.ViewType:
			elemType = viewType.Elem
		default:
			return nil, nil, true, fmt.Errorf("arena_da_copy_exact specialization expected view parameter, got %T", funcType.Params[0])
		}
		elemLLVMType, err := s.g.lowerType(elemType)
		if err != nil {
			return nil, nil, true, err
		}
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		dstData := llvmBuildExtractValue(s.builder, dstValue, 0, cStringFree("view.copy.dst.data"))
		srcData := llvmBuildExtractValue(s.builder, srcValue, 0, cStringFree("view.copy.src.data"))
		domainName := ""
		dstScopeName := ""
		srcScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("elisa_core.view.copy.%p.domain", expr)
			dstScopeName = domainName + ".dst"
			srcScopeName = domainName + ".src"
		}
		for i := uint64(0); i < exactCopyCount; i++ {
			indexValue := llvmConstInt(usizeType, llvmIRUnsignedLongLong(i), 0)
			indices := []llvmIRValueRef{indexValue}
			srcPtr := llvmBuildGEP2(s.builder, elemLLVMType, srcData, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("view.copy.src.elem.ptr"))
			elemValue := llvmBuildLoad2(s.builder, elemLLVMType, srcPtr, cStringFree("view.copy.elem"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(elemValue, domainName, srcScopeName, []string{dstScopeName})
			}
			dstPtr := llvmBuildGEP2(s.builder, elemLLVMType, dstData, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("view.copy.dst.elem.ptr"))
			store := llvmBuildStore(s.builder, elemValue, dstPtr)
			if disjoint {
				s.attachAliasScopeMetadataWithNames(store, domainName, dstScopeName, []string{srcScopeName})
			}
		}
		return nil, funcType.Return, true, nil
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, true, err
	}
	dstData := llvmBuildExtractValue(s.builder, dstValue, 0, cStringFree("view.copy.dst.data"))
	dstLen := llvmBuildExtractValue(s.builder, dstValue, 1, cStringFree("view.copy.dst.len"))
	srcData := llvmBuildExtractValue(s.builder, srcValue, 0, cStringFree("view.copy.src.data"))
	srcLen := llvmBuildExtractValue(s.builder, srcValue, 1, cStringFree("view.copy.src.len"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	var copyElemType semantic.Type
	if vt, ok := funcType.Params[0].(*semantic.ViewType); ok {
		copyElemType = vt.Elem
	} else {
		return nil, nil, true, fmt.Errorf("arena_da_copy_exact specialization expected view parameter, got %T", funcType.Params[0])
	}
	copyElemSize, err := s.sizeOfType(copyElemType)
	if err != nil {
		return nil, nil, true, err
	}
	copyElemSizeConst := llvmConstInt(usizeType, llvmIRUnsignedLongLong(copyElemSize), 0)
	dstBytes := llvmBuildMul(s.builder, dstLen, copyElemSizeConst, cStringFree("view.copy.dst.bytes"))
	srcBytes := llvmBuildMul(s.builder, srcLen, copyElemSizeConst, cStringFree("view.copy.src.bytes"))
	zeroBytes := llvmConstInt(usizeType, 0, 0)
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.namedType("usize")}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	buildMemcpyNoAlias := func(byteCount llvmIRValueRef) {
		memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []llvmIRValueRef{dstData, srcData, byteCount}, "view.copy.memcpy")
		s.addCallSiteEnumAttribute(memcpyCall, llvmIRUnsigned(1), "noalias")
		s.addCallSiteEnumAttribute(memcpyCall, llvmIRUnsigned(2), "noalias")
	}

	if s.g.semanticReadView().ExprsHaveEqualExtentSize(dstExpr, srcExpr) {
		zeroCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), dstBytes, zeroBytes, cStringFree("view.copy.bytes.zero"))
		copyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.exact.fast"))
		mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.exact.merge"))
		llvmBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

		llvmPositionBuilderAtEnd(s.builder, copyBB)
		buildMemcpyNoAlias(dstBytes)
		llvmBuildBr(s.builder, mergeBB)

		llvmPositionBuilderAtEnd(s.builder, mergeBB)
		return nil, funcType.Return, true, nil
	}

	lenEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), dstBytes, srcBytes, cStringFree("view.copy.bytes.eq"))
	copyCheckBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.fast.check"))
	fallbackBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.fallback"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.merge"))
	llvmBuildCondBr(s.builder, lenEqual, copyCheckBB, fallbackBB)

	llvmPositionBuilderAtEnd(s.builder, copyCheckBB)
	zeroCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), dstBytes, zeroBytes, cStringFree("view.copy.bytes.zero"))
	copyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.copy.fast"))
	llvmBuildCondBr(s.builder, zeroCond, mergeBB, copyBB)

	llvmPositionBuilderAtEnd(s.builder, copyBB)
	buildMemcpyNoAlias(dstBytes)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, fallbackBB)
	fallbackCall := s.buildCall(llvmFnType, callee, []llvmIRValueRef{dstValue, srcValue}, "")
	_ = fallbackCall
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	return nil, funcType.Return, true, nil
}
func (s *functionState) emitSpecializedArenaViewEqCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	ident, ok := expr.Func.(*ast.Ident)
	if !ok || ident.Name != "arena_da_eq_exact" {
		return nil, nil, false, nil
	}
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, ident.Name) {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 || s == nil || s.g == nil || s.g.result == nil {
		return nil, nil, false, nil
	}
	leftExpr := expr.Args[0]
	rightExpr := expr.Args[1]
	leftType := s.exprType(leftExpr)
	rightType := s.exprType(rightExpr)
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.namedType("bool")
	}
	if !isDynArrayViewCarrierType(leftType) || !isDynArrayViewCarrierType(rightType) {
		return nil, nil, false, nil
	}
	if !s.g.semanticReadView().ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		return nil, nil, false, nil
	}
	disjoint := s.g.semanticReadView().ExprsAreDisjoint(leftExpr, rightExpr)
	exactEqByteCount := uint64(0)
	hasSmallExactEqByteCount := false
	if elemType, ok := runtimeIndexedElemType(leftType); ok {
		if elemSize, err := s.sizeOfType(elemType); err == nil && elemSize != 0 {
			if leftFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(leftExpr); ok {
				if leftCount, ok := constOptimizationExtentSize(leftFacts.Extent); ok {
					if rightFacts, ok := s.g.semanticReadView().ExprOptimizationFacts(rightExpr); ok {
						if rightCount, ok := constOptimizationExtentSize(rightFacts.Extent); ok && rightCount == leftCount {
							totalBytes := leftCount * elemSize
							if totalBytes <= smallExactArenaEqUnrollByteLimit {
								exactEqByteCount = totalBytes
								hasSmallExactEqByteCount = true
							}
						}
					}
				}
			}
		}
	}

	leftValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, _, err := s.emitExpr(rightExpr, rightType)
	if err != nil {
		return nil, nil, true, err
	}
	leftData := llvmBuildExtractValue(s.builder, leftValue, 0, cStringFree("view.eq.left.data"))
	leftLen := llvmBuildExtractValue(s.builder, leftValue, 1, cStringFree("view.eq.left.len"))
	rightData := llvmBuildExtractValue(s.builder, rightValue, 0, cStringFree("view.eq.right.data"))
	if hasSmallExactEqByteCount {
		if exactEqByteCount == 0 {
			return llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0), resultType, true, nil
		}
		byteType := llvmInt8TypeInContext(s.g.context)
		usizeType, err := s.g.lowerBuiltin("usize")
		if err != nil {
			return nil, nil, true, err
		}
		cmpResult := llvmConstInt(llvmInt1TypeInContext(s.g.context), 1, 0)
		domainName := ""
		leftScopeName := ""
		rightScopeName := ""
		if disjoint {
			domainName = fmt.Sprintf("elisa_core.view.eq.%p.domain", expr)
			leftScopeName = domainName + ".left"
			rightScopeName = domainName + ".right"
		}
		for i := uint64(0); i < exactEqByteCount; i++ {
			indexValue := llvmConstInt(usizeType, llvmIRUnsignedLongLong(i), 0)
			indices := []llvmIRValueRef{indexValue}
			leftBytePtr := llvmBuildGEP2(s.builder, byteType, leftData, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("view.eq.left.byte.ptr"))
			leftByte := llvmBuildLoad2(s.builder, byteType, leftBytePtr, cStringFree("view.eq.left.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(leftByte, domainName, leftScopeName, []string{rightScopeName})
			}
			rightBytePtr := llvmBuildGEP2(s.builder, byteType, rightData, llvmValueSlicePtr(indices), llvmIRUnsigned(len(indices)), cStringFree("view.eq.right.byte.ptr"))
			rightByte := llvmBuildLoad2(s.builder, byteType, rightBytePtr, cStringFree("view.eq.right.byte"))
			if disjoint {
				s.attachAliasScopeMetadataWithNames(rightByte, domainName, rightScopeName, []string{leftScopeName})
			}
			bytesEqual := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), leftByte, rightByte, cStringFree("view.eq.byte.eq"))
			cmpResult = llvmBuildAnd(s.builder, cmpResult, bytesEqual, cStringFree("view.eq.byte.and"))
		}
		return cmpResult, resultType, true, nil
	}
	_ = llvmBuildExtractValue(s.builder, rightValue, 1, cStringFree("view.eq.right.len"))
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, true, err
	}
	eqElemType, ok := runtimeIndexedElemType(leftType)
	if !ok {
		return nil, nil, true, fmt.Errorf("arena_da_eq_exact specialization expected a view element type, got %s", leftType)
	}
	eqElemSize, err := s.sizeOfType(eqElemType)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount := llvmBuildMul(s.builder, leftLen, llvmConstInt(usizeType, llvmIRUnsignedLongLong(eqElemSize), 0), cStringFree("view.eq.bytes"))
	zeroBytes := llvmConstInt(usizeType, 0, 0)
	zeroCond := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntEQ), byteCount, zeroBytes, cStringFree("view.eq.bytes.zero"))
	entryBlock := llvmGetInsertBlock(s.builder)
	memcmpBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.eq.memcmp"))
	mergeBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("view.eq.merge"))
	llvmBuildCondBr(s.builder, zeroCond, mergeBB, memcmpBB)

	llvmPositionBuilderAtEnd(s.builder, memcmpBB)
	cmp, err := s.emitMemcmpEqualValue(leftData, rightData, byteCount, "view.eq.memcmp", disjoint)
	if err != nil {
		return nil, nil, true, err
	}
	memcmpEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, mergeBB)

	llvmPositionBuilderAtEnd(s.builder, mergeBB)
	boolType := llvmInt1TypeInContext(s.g.context)
	phi := llvmBuildPhi(s.builder, boolType, cStringFree("view.eq.result"))
	trueValue := llvmConstInt(boolType, 1, 0)
	values := []llvmIRValueRef{trueValue, cmp}
	blocks := []llvmIRBlockRef{entryBlock, memcmpEnd}
	llvmAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), llvmIRUnsigned(len(values)))
	return phi, resultType, true, nil
}
