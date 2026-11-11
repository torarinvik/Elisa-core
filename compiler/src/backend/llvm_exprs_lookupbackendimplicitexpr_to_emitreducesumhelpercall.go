//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) lookupBackendImplicitExpr(name string, working map[string]ast.Expr) (ast.Expr, bool) {
	if working != nil {
		if expr, ok := working[name]; ok && expr != nil {
			return expr, true
		}
	}
	if _, ok := s.lookupBinding(name); ok {
		return &ast.Ident{Name: name}, true
	}
	if s.g != nil && s.g.result != nil && s.g.hasGlobalScope() {
		if _, ok := s.g.lookupGlobal(name); ok {
			return &ast.Ident{Name: name}, true
		}
	}
	return nil, false
}
func (s *functionState) recoverImplicitCallArgs(expr *ast.CallExpr, funcType *semantic.FuncType) ([]ast.Expr, bool) {
	if s == nil || expr == nil || funcType == nil || len(funcType.ImplicitParamNames) == 0 {
		return nil, false
	}
	working := map[string]ast.Expr{}
	resolved := make([]ast.Expr, 0, len(funcType.ImplicitParamNames))
	for _, name := range funcType.ImplicitParamNames {
		value, ok := s.lookupBackendImplicitExpr(name, working)
		if !ok {
			// Mirror the analyzer's docs/74 fallback (resolveImplicitCallArgs): a root caller
			// with no store binding threads a synthetic `__packed_store_E` ident, which the
			// emit path resolves by getting-or-creating the region-backed store on demand.
			// This call site can only be discovered at backend time when the callee is bound
			// through generic/protocol dispatch (the analyzer saw the protocol method, not
			// the impl symbol carrying the implicit param).
			if strings.HasPrefix(name, "__packed_store_") {
				// Consumer callees (enum handles in explicit params) must reuse the active
				// binding, not mint a fresh region store — same rule as the analyzer's
				// packedStoreImplicitArgExpr (see PackedStoreImplicitUsePrefix).
				enumName := strings.TrimPrefix(name, "__packed_store_")
				if semantic.FuncTypeConsumesPackedEnumRoot(funcType, enumName) {
					resolved = append(resolved, &ast.Ident{Name: semantic.PackedStoreImplicitUsePrefix + enumName})
					continue
				}
				resolved = append(resolved, &ast.Ident{Name: name})
				continue
			}
			return nil, false
		}
		resolved = append(resolved, value)
	}
	return resolved, true
}

func backendExplicitMoveOperand(expr ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return backendExplicitMoveOperand(n.Inner)
	case *ast.MoveExpr:
		return n.Operand, true
	default:
		return nil, false
	}
}
func (s *functionState) emitProofCarryingViewHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	helperName := callIdentName(expr)
	if s == nil || s.g == nil || s.g.result == nil || !s.g.result.IsCompilerBuiltinHelperCall(expr, helperName) {
		return nil, nil, false, nil
	}
	switch helperName {
	case "any":
		return s.emitIterableBoolAggregateHelperCall(expr, "any", true)
	case "all":
		return s.emitIterableBoolAggregateHelperCall(expr, "all", false)
	case "enumerate":
		return s.emitEnumerateHelperCall(expr)
	case "readonly":
		return s.emitReadonlyHelperCall(expr)
	case "split_at":
		return s.emitSplitAtHelperCall(expr)
	case "chunks_exact":
		return s.emitChunksExactHelperCall(expr)
	case "reduce_sum":
		return s.emitReduceSumHelperCall(expr)
	case "zip_map":
		return s.emitZipMapHelperCall(expr)
	default:
		return nil, nil, false, nil
	}
}
func (s *functionState) emitIterableBoolAggregateHelperCall(expr *ast.CallExpr, helperName string, stopOnTrue bool) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("%s expects 1 argument, got %d", helperName, len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("%s source is missing a semantic type", helperName)
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.namedType("bool")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceAlloca, err := s.emitStackTempValue(sourceValue, sourceType, helperName+".source")
	if err != nil {
		return nil, nil, true, err
	}
	iterSourceAlloca, iterSourceType, transforms, err := s.peelIterViewTransforms(helperName, sourceAlloca, sourceType, ast.IterBindValue)
	if err != nil {
		return nil, nil, true, err
	}
	iterSourceExpr := iterLoopBaseSourceExpr(expr.Args[0])
	countValue, err := s.emitIterLoopCount(iterSourceExpr, iterSourceAlloca, iterSourceType, helperName)
	if err != nil {
		return nil, nil, true, err
	}
	boolType := s.g.namedType("bool")
	boolLLVMType, err := s.g.lowerType(boolType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := llvmConstInt(usizeLLVMType, 0, 0)
	one := llvmConstInt(usizeLLVMType, 1, 0)
	defaultBool := llvmConstInt(boolLLVMType, 0, 0)
	terminalBool := llvmConstInt(boolLLVMType, 1, 0)
	if !stopOnTrue {
		defaultBool = llvmConstInt(boolLLVMType, 1, 0)
		terminalBool = llvmConstInt(boolLLVMType, 0, 0)
	}

	entryBlock := llvmGetInsertBlock(s.builder)
	loopCondBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".cond"))
	loopBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".body"))
	loopContinueBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".continue"))
	loopShortCircuitBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".short_circuit"))
	loopEndBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".end"))
	llvmBuildBr(s.builder, loopCondBB)

	llvmPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := llvmBuildPhi(s.builder, usizeLLVMType, cStringFree(helperName+".index"))
	llvmAddIncoming(indexValue, llvmValueSlicePtr([]llvmIRValueRef{zeroIndex}), llvmBlockSlicePtr([]llvmIRBlockRef{entryBlock}), 1)
	hasMore := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, countValue, cStringFree(helperName+".has_more"))
	llvmBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	llvmPositionBuilderAtEnd(s.builder, loopBodyBB)
	itemValue, itemType, err := s.emitIterLoopElementValue(iterSourceExpr, iterSourceAlloca, iterSourceType, indexValue, helperName)
	if err != nil {
		return nil, nil, true, err
	}
	itemValue, itemType, err = s.applyIterViewTransforms(helperName, indexValue, loopContinueBB, itemValue, itemType, transforms)
	if err != nil {
		return nil, nil, true, err
	}
	itemBool, err := s.coerceValue(itemValue, itemType, boolType)
	if err != nil {
		return nil, nil, true, err
	}
	if stopOnTrue {
		llvmBuildCondBr(s.builder, itemBool, loopShortCircuitBB, loopContinueBB)
	} else {
		llvmBuildCondBr(s.builder, itemBool, loopContinueBB, loopShortCircuitBB)
	}

	llvmPositionBuilderAtEnd(s.builder, loopContinueBB)
	nextIndex := llvmBuildAdd(s.builder, indexValue, one, cStringFree(helperName+".index.next"))
	continueEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, loopCondBB)
	llvmAddIncoming(indexValue, llvmValueSlicePtr([]llvmIRValueRef{nextIndex}), llvmBlockSlicePtr([]llvmIRBlockRef{continueEnd}), 1)

	llvmPositionBuilderAtEnd(s.builder, loopShortCircuitBB)
	shortCircuitEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, loopEndBB)

	llvmPositionBuilderAtEnd(s.builder, loopEndBB)
	resultValue := llvmBuildPhi(s.builder, boolLLVMType, cStringFree(helperName+".result"))
	llvmAddIncoming(resultValue, llvmValueSlicePtr([]llvmIRValueRef{defaultBool, terminalBool}), llvmBlockSlicePtr([]llvmIRBlockRef{loopCondBB, shortCircuitEnd}), 2)
	return resultValue, boolType, true, nil
}
func (s *functionState) emitEnumerateHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("enumerate expects 1 argument, got %d", len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("enumerate source is missing a semantic type")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, true, fmt.Errorf("enumerate result is missing a semantic type")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := llvmGetUndef(resultLLVMType)
	resultValue = llvmBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("enumerate.source.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitReadonlyHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("readonly expects 1 argument, got %d", len(expr.Args))
	}
	value, actualType, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	return value, actualType, true, nil
}
func (s *functionState) emitSplitAtHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("split_at expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.ViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("split_at expects a view source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	indexValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree("split.view.len"))
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := llvmConstInt(usizeLLVMType, 0, 0)
	leftValue, err := s.emitArenaViewSliceValue(viewValue, viewType, zero, indexValue, "split.left")
	if err != nil {
		return nil, nil, true, err
	}
	rightValue, err := s.emitArenaViewSliceValue(viewValue, viewType, indexValue, viewLen, "split.right")
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := llvmGetUndef(resultLLVMType)
	resultValue = llvmBuildInsertValue(s.builder, resultValue, leftValue, 0, cStringFree("split.left.insert"))
	resultValue = llvmBuildInsertValue(s.builder, resultValue, rightValue, 1, cStringFree("split.right.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitChunksExactHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("chunks_exact expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.ViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact expects a view source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.namedType("usize")
	chunkSizeValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := llvmBuildExtractValue(s.builder, viewValue, 1, cStringFree("chunks.view.len"))
	if err := s.emitChunksExactValidation(chunkSizeValue, viewLen, "chunks_exact"); err != nil {
		return nil, nil, true, err
	}
	chunksLen := llvmBuildUDiv(s.builder, viewLen, chunkSizeValue, cStringFree("chunks.len"))
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := llvmGetUndef(resultLLVMType)
	resultValue = llvmBuildInsertValue(s.builder, resultValue, viewValue, 0, cStringFree("chunks.source.insert"))
	resultValue = llvmBuildInsertValue(s.builder, resultValue, chunkSizeValue, 1, cStringFree("chunks.chunk_size.insert"))
	resultValue = llvmBuildInsertValue(s.builder, resultValue, chunksLen, 2, cStringFree("chunks.len.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitReduceSumHelperCall(expr *ast.CallExpr) (llvmIRValueRef, semantic.Type, bool, error) {
	if len(expr.Args) < 2 {
		return nil, nil, true, fmt.Errorf("reduce_sum expects at least 2 arguments, got %d", len(expr.Args))
	}
	srcType, srcElemType, ok := zipMapViewInfo(s.exprType(expr.Args[0]))
	if !ok {
		return nil, nil, true, fmt.Errorf("reduce_sum source expects a dense view")
	}
	callbackType, ok := s.exprType(expr.Args[1]).(*semantic.FuncType)
	if !ok || callbackType == nil {
		return nil, nil, true, fmt.Errorf("reduce_sum callback expects a function value")
	}
	resultType := s.exprType(expr)
	srcValue, _, err := s.emitExpr(expr.Args[0], srcType)
	if err != nil {
		return nil, nil, true, err
	}
	callbackValue, _, err := s.emitExpr(expr.Args[1], callbackType)
	if err != nil {
		return nil, nil, true, err
	}
	extraArgs := make([]llvmIRValueRef, 0, len(expr.Args)-2)
	for i, arg := range expr.Args[2:] {
		var expected semantic.Type
		if i+1 < len(callbackType.Params) {
			expected = callbackType.Params[i+1]
		}
		value, _, err := s.emitExpr(arg, expected)
		if err != nil {
			return nil, nil, true, err
		}
		extraArgs = append(extraArgs, value)
	}
	srcDataPtr, err := s.emitDenseViewDataPointer(srcValue, srcElemType, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	totalValue := llvmBuildExtractValue(s.builder, srcValue, 1, cStringFree("reduce_sum.total"))
	usizeType := s.g.namedType("usize")
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := llvmConstInt(usizeLLVMType, 0, 0)
	one := llvmConstInt(usizeLLVMType, 1, 0)
	accZero, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}

	entryBlock := llvmGetInsertBlock(s.builder)
	loopCondBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.cond"))
	loopBodyBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.body"))
	loopEndBB := llvmAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.end"))
	llvmBuildBr(s.builder, loopCondBB)

	llvmPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := llvmBuildPhi(s.builder, usizeLLVMType, cStringFree("reduce_sum.index"))
	accValue := llvmBuildPhi(s.builder, resultLLVMType, cStringFree("reduce_sum.acc"))
	initValues := []llvmIRValueRef{zeroIndex, accZero}
	initBlocks := []llvmIRBlockRef{entryBlock, entryBlock}
	llvmAddIncoming(indexValue, llvmValueSlicePtr(initValues[:1]), llvmBlockSlicePtr(initBlocks[:1]), 1)
	llvmAddIncoming(accValue, llvmValueSlicePtr(initValues[1:]), llvmBlockSlicePtr(initBlocks[1:]), 1)
	hasMore := llvmBuildICmp(s.builder, llvmIRIntPredicate(llvmIntULT), indexValue, totalValue, cStringFree("reduce_sum.has_more"))
	llvmBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	llvmPositionBuilderAtEnd(s.builder, loopBodyBB)
	srcElemPtr, err := s.emitDenseViewIndexedAddress(srcDataPtr, srcElemType, indexValue, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	srcElem, err := s.loadValue(srcElemPtr, srcElemType, "reduce_sum.src.elem")
	if err != nil {
		return nil, nil, true, err
	}
	callArgs := make([]llvmIRValueRef, 0, len(extraArgs)+1)
	callArgs = append(callArgs, srcElem)
	callArgs = append(callArgs, extraArgs...)
	mappedValue, err := s.emitFunctionValueCall(callbackValue, callbackType, callArgs, "reduce_sum.call")
	if err != nil {
		return nil, nil, true, err
	}
	coercedValue, err := s.coerceValue(mappedValue, callbackType.Return, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	nextAcc, err := s.emitAugmentedValue(lexer.TOKEN_PLUSEQ, accValue, coercedValue, resultType)
	if err != nil {
		return nil, nil, true, err
	}
	nextIndex := llvmBuildAdd(s.builder, indexValue, one, cStringFree("reduce_sum.index.next"))
	bodyEnd := llvmGetInsertBlock(s.builder)
	llvmBuildBr(s.builder, loopCondBB)
	nextIndexValues := []llvmIRValueRef{nextIndex}
	nextIndexBlocks := []llvmIRBlockRef{bodyEnd}
	llvmAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)
	nextAccValues := []llvmIRValueRef{nextAcc}
	nextAccBlocks := []llvmIRBlockRef{bodyEnd}
	llvmAddIncoming(accValue, llvmValueSlicePtr(nextAccValues), llvmBlockSlicePtr(nextAccBlocks), 1)

	llvmPositionBuilderAtEnd(s.builder, loopEndBB)
	return accValue, resultType, true, nil
}
