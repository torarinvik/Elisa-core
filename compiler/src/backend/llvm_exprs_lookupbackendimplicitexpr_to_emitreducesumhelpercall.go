//go:build cgo

package backend

/*
#include <stdlib.h>
#include <string.h>
#include <llvm-c/Core.h>

static int elisacoreLLVMIsZeroValue(LLVMValueRef value) {
	return LLVMIsAConstant(value) != NULL && LLVMIsNull(value);
}

static LLVMMetadataRef elisa_coreAliasMDString(LLVMContextRef ctx, const char* value) {
	if (value == NULL) {
		return LLVMMDStringInContext2(ctx, "", 0);
	}
	return LLVMMDStringInContext2(ctx, value, strlen(value));
}

static LLVMMetadataRef elisa_coreAliasMDNode(LLVMContextRef ctx, LLVMMetadataRef* operands, size_t count) {
	return LLVMMDNodeInContext2(ctx, operands, count);
}

static unsigned elisa_coreMetadataKindID(LLVMContextRef ctx, const char* kindName) {
	return LLVMGetMDKindIDInContext(ctx, kindName, strlen(kindName));
}

static void elisa_coreSetMetadataList(LLVMValueRef inst, LLVMContextRef ctx, const char* kindName, LLVMMetadataRef* scopes, size_t count) {
	if (inst == NULL || ctx == NULL || kindName == NULL || count == 0) {
		return;
	}
	LLVMMetadataRef list = elisa_coreAliasMDNode(ctx, scopes, count);
	LLVMValueRef listValue = LLVMMetadataAsValue(ctx, list);
	LLVMSetMetadata(inst, elisa_coreMetadataKindID(ctx, kindName), listValue);
}

static LLVMMetadataRef elisa_coreCreateAliasScopeDomain(LLVMContextRef ctx, const char* domainName) {
	LLVMMetadataRef operands[1];
	operands[0] = elisa_coreAliasMDString(ctx, domainName);
	return elisa_coreAliasMDNode(ctx, operands, 1);
}

static LLVMMetadataRef elisa_coreCreateAliasScope(LLVMContextRef ctx, LLVMMetadataRef domain, const char* scopeName) {
	LLVMMetadataRef operands[2];
	operands[0] = elisa_coreAliasMDString(ctx, scopeName);
	operands[1] = domain;
	return elisa_coreAliasMDNode(ctx, operands, 2);
}

static void elisa_coreAttachAliasScopeMetadata(LLVMValueRef inst, LLVMContextRef ctx, const char* domainName, const char* aliasScopeName,
	const char* noAliasScope1Name, int hasNoAliasScope1, const char* noAliasScope2Name, int hasNoAliasScope2) {
	if (inst == NULL || ctx == NULL || domainName == NULL || aliasScopeName == NULL) {
		return;
	}
	LLVMMetadataRef domain = elisa_coreCreateAliasScopeDomain(ctx, domainName);
	LLVMMetadataRef aliasScope = elisa_coreCreateAliasScope(ctx, domain, aliasScopeName);
	LLVMMetadataRef aliasScopes[1];
	aliasScopes[0] = aliasScope;
	elisa_coreSetMetadataList(inst, ctx, "alias.scope", aliasScopes, 1);

	LLVMMetadataRef noAliasScopes[2];
	size_t noAliasCount = 0;
	if (hasNoAliasScope1 && noAliasScope1Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope1Name);
	}
	if (hasNoAliasScope2 && noAliasScope2Name != NULL) {
		noAliasScopes[noAliasCount++] = elisa_coreCreateAliasScope(ctx, domain, noAliasScope2Name);
	}
	if (noAliasCount != 0) {
		elisa_coreSetMetadataList(inst, ctx, "noalias", noAliasScopes, noAliasCount);
	}
}
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
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
	if s.g != nil && s.g.result != nil && s.g.result.GlobalScope != nil {
		if _, ok := s.g.result.GlobalScope.Lookup(name); ok {
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
	for _, item := range backendOrderedWithItems(expr.WithBundles, expr.WithArgs, expr.WithItemOrder) {
		if item.IsBundle {
			bundle, ok := s.lookupBackendContextBundle(item.Bundle.Name)
			if !ok || bundle == nil {
				return nil, false
			}
			explicitValues := make(map[string]ast.Expr, len(item.Bundle.Args))
			for _, arg := range item.Bundle.Args {
				explicitValues[arg.Name] = arg.Value
			}
			for _, field := range bundle.Fields {
				if value, ok := explicitValues[field.Name]; ok {
					working[field.Name] = value
					continue
				}
				value, ok := s.lookupBackendImplicitExpr(field.Name, working)
				if !ok {
					return nil, false
				}
				working[field.Name] = value
			}
			continue
		}
		working[item.Arg.Name] = item.Arg.Value
	}
	resolved := make([]ast.Expr, 0, len(funcType.ImplicitParamNames))
	explicitCount := backendExplicitParamCount(funcType, nil)
	for i, name := range funcType.ImplicitParamNames {
		value, ok := s.lookupBackendImplicitExpr(name, working)
		if !ok {
			paramIndex := explicitCount + i
			if paramIndex < len(funcType.Params) {
				if storeType, isTreeStore := funcType.Params[paramIndex].(*semantic.TreeStoreType); isTreeStore && storeType != nil {
					resolved = append(resolved, &ast.Ident{Name: semantic.TreeStoreImplicitParamName(storeType.Family)})
					continue
				}
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
func (s *functionState) emitProofCarryingViewHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	switch callIdentName(expr) {
	case "any":
		return s.emitIterableBoolAggregateHelperCall(expr, "any", true)
	case "all":
		return s.emitIterableBoolAggregateHelperCall(expr, "all", false)
	case "enumerate":
		return s.emitEnumerateHelperCall(expr)
	case "where":
		return s.emitWhereHelperCall(expr)
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
func (s *functionState) emitIterableBoolAggregateHelperCall(expr *ast.CallExpr, helperName string, stopOnTrue bool) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("%s expects 1 argument, got %d", helperName, len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("%s source is missing a semantic type", helperName)
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		resultType = s.g.result.NamedTypes["bool"]
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
	boolType := s.g.result.NamedTypes["bool"]
	boolLLVMType, err := s.g.lowerType(boolType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	defaultBool := C.LLVMConstInt(boolLLVMType, 0, 0)
	terminalBool := C.LLVMConstInt(boolLLVMType, 1, 0)
	if !stopOnTrue {
		defaultBool = C.LLVMConstInt(boolLLVMType, 1, 0)
		terminalBool = C.LLVMConstInt(boolLLVMType, 0, 0)
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".body"))
	loopContinueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".continue"))
	loopShortCircuitBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".short_circuit"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(helperName+".end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(helperName+".index"))
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{zeroIndex}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{entryBlock}), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree(helperName+".has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
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
		C.LLVMBuildCondBr(s.builder, itemBool, loopShortCircuitBB, loopContinueBB)
	} else {
		C.LLVMBuildCondBr(s.builder, itemBool, loopContinueBB, loopShortCircuitBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, loopContinueBB)
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(helperName+".index.next"))
	continueEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr([]C.LLVMValueRef{nextIndex}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{continueEnd}), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopShortCircuitBB)
	shortCircuitEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	resultValue := C.LLVMBuildPhi(s.builder, boolLLVMType, cStringFree(helperName+".result"))
	C.LLVMAddIncoming(resultValue, llvmValueSlicePtr([]C.LLVMValueRef{defaultBool, terminalBool}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{loopCondBB, shortCircuitEnd}), 2)
	return resultValue, boolType, true, nil
}
func (s *functionState) emitEnumerateHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("enumerate.source.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitWhereHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("where expects 2 arguments, got %d", len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("where source is missing a semantic type")
	}
	predicateType, ok := s.exprType(expr.Args[1]).(*semantic.FuncType)
	if !ok || predicateType == nil {
		return nil, nil, true, fmt.Errorf("where predicate is missing a function type")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, true, fmt.Errorf("where result is missing a semantic type")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	predicateValue, _, err := s.emitExpr(expr.Args[1], predicateType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("where.source.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, predicateValue, 1, cStringFree("where.predicate.insert"))
	return resultValue, resultType, true, nil
}

func (s *functionState) emitTreeTraversalHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	switch callIdentName(expr) {
	case "children":
		return s.emitChildrenHelperCall(expr)
	default:
		return nil, nil, false, nil
	}
}
func (s *functionState) emitChildrenHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("children expects 1 argument, got %d", len(expr.Args))
	}
	sourceType := s.exprType(expr.Args[0])
	if sourceType == nil {
		return nil, nil, true, fmt.Errorf("children source is missing a semantic type")
	}
	resultType := s.exprType(expr)
	if resultType == nil {
		return nil, nil, true, fmt.Errorf("children result is missing a semantic type")
	}
	sourceValue, _, err := s.emitExpr(expr.Args[0], sourceType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, sourceValue, 0, cStringFree("tree.children.node.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitReadonlyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("readonly expects 1 argument, got %d", len(expr.Args))
	}
	value, actualType, err := s.emitExpr(expr.Args[0], s.exprType(expr.Args[0]))
	if err != nil {
		return nil, nil, true, err
	}
	return value, actualType, true, nil
}
func (s *functionState) emitSplitAtHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("split_at expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("split_at expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("split.view.len"))
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
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
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, leftValue, 0, cStringFree("split.left.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, rightValue, 1, cStringFree("split.right.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitChunksExactHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("chunks_exact expects 2 arguments, got %d", len(expr.Args))
	}
	viewType, ok := s.exprType(expr.Args[0]).(*semantic.DArrayViewType)
	if !ok || viewType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact expects a dview source")
	}
	resultType := s.exprType(expr)
	viewValue, _, err := s.emitExpr(expr.Args[0], viewType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	chunkSizeValue, _, err := s.emitExpr(expr.Args[1], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("chunks.view.len"))
	if err := s.emitChunksExactValidation(chunkSizeValue, viewLen, "chunks_exact"); err != nil {
		return nil, nil, true, err
	}
	chunksLen := C.LLVMBuildUDiv(s.builder, viewLen, chunkSizeValue, cStringFree("chunks.len"))
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, viewValue, 0, cStringFree("chunks.source.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunkSizeValue, 1, cStringFree("chunks.chunk_size.insert"))
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, chunksLen, 2, cStringFree("chunks.len.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitReduceSumHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
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
	extraArgs := make([]C.LLVMValueRef, 0, len(expr.Args)-2)
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
	totalValue := C.LLVMBuildExtractValue(s.builder, srcValue, 1, cStringFree("reduce_sum.total"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	accZero, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, true, err
	}

	entryBlock := C.LLVMGetInsertBlock(s.builder)
	loopCondBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.cond"))
	loopBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.body"))
	loopEndBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("reduce_sum.end"))
	C.LLVMBuildBr(s.builder, loopCondBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopCondBB)
	indexValue := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree("reduce_sum.index"))
	accValue := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("reduce_sum.acc"))
	initValues := []C.LLVMValueRef{zeroIndex, accZero}
	initBlocks := []C.LLVMBasicBlockRef{entryBlock, entryBlock}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(initValues[:1]), llvmBlockSlicePtr(initBlocks[:1]), 1)
	C.LLVMAddIncoming(accValue, llvmValueSlicePtr(initValues[1:]), llvmBlockSlicePtr(initBlocks[1:]), 1)
	hasMore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, totalValue, cStringFree("reduce_sum.has_more"))
	C.LLVMBuildCondBr(s.builder, hasMore, loopBodyBB, loopEndBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBodyBB)
	srcElemPtr, err := s.emitDenseViewIndexedAddress(srcDataPtr, srcElemType, indexValue, "reduce_sum.src")
	if err != nil {
		return nil, nil, true, err
	}
	srcElem, err := s.loadValue(srcElemPtr, srcElemType, "reduce_sum.src.elem")
	if err != nil {
		return nil, nil, true, err
	}
	callArgs := make([]C.LLVMValueRef, 0, len(extraArgs)+1)
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
	nextIndex := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("reduce_sum.index.next"))
	bodyEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, loopCondBB)
	nextIndexValues := []C.LLVMValueRef{nextIndex}
	nextIndexBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(indexValue, llvmValueSlicePtr(nextIndexValues), llvmBlockSlicePtr(nextIndexBlocks), 1)
	nextAccValues := []C.LLVMValueRef{nextAcc}
	nextAccBlocks := []C.LLVMBasicBlockRef{bodyEnd}
	C.LLVMAddIncoming(accValue, llvmValueSlicePtr(nextAccValues), llvmBlockSlicePtr(nextAccBlocks), 1)

	C.LLVMPositionBuilderAtEnd(s.builder, loopEndBB)
	return accValue, resultType, true, nil
}
