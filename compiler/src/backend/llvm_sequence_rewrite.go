//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type sequenceRewriteCodegenContext struct {
	outPtr  C.LLVMValueRef
	outType *semantic.DArrayType
}

type sequenceRewriteArmInfoBackend struct {
	bindType      semantic.Type
	bindName      string
	alwaysMatches bool
	memberType    semantic.Type
}

func sequenceRewriteTargetTypeExprBackend(root ast.TypeExpr) (ast.TypeExpr, bool) {
	generic, ok := root.(*ast.GenericType)
	if !ok || generic == nil || generic.Name != "sequence" || len(generic.Args) != 1 {
		return nil, false
	}
	return generic.Args[0], true
}

func sequenceRewriteCarrierElemTypeBackend(t semantic.Type) (semantic.Type, bool) {
	switch tt := semantic.StripAggregateStateType(t).(type) {
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return nil, false
		}
		return tt.Elem, true
	default:
		return nil, false
	}
}

func sequenceRewriteArmBindNameBackend(arm ast.VisitArm) (string, bool) {
	if arm.Wildcard {
		return "", true
	}
	if arm.TargetName != "" && !strings.Contains(arm.TargetName, ".") && arm.BindName == "" && arm.ChildResultsName == "" && len(arm.ChildBindings) == 0 {
		return arm.TargetName, true
	}
	return "", false
}

func sequenceRewriteRootExactMembersBackend(root treeFoldRootInfo) []semantic.Type {
	switch root.kind {
	case treeFoldRootCategory:
		return treeCategoryMembersInTagOrder(root.category)
	case treeFoldRootExact:
		if root.exact == nil {
			return nil
		}
		return []semantic.Type{root.exact}
	case treeFoldRootFamily:
		return semantic.TreeFamilyExactMembersInTagOrder(root.family)
	default:
		return nil
	}
}

func (s *functionState) resolveSequenceRewriteArmInfoBackend(elemType semantic.Type, arm ast.VisitArm) (sequenceRewriteArmInfoBackend, error) {
	if arm.Wildcard {
		return sequenceRewriteArmInfoBackend{alwaysMatches: true}, nil
	}
	if bindName, ok := sequenceRewriteArmBindNameBackend(arm); ok {
		return sequenceRewriteArmInfoBackend{bindType: elemType, bindName: bindName, alwaysMatches: true}, nil
	}
	if arm.ChildResultsName != "" || len(arm.ChildBindings) != 0 {
		return sequenceRewriteArmInfoBackend{}, fmt.Errorf("sequence rewrite tree-target arms do not support child result bindings")
	}
	root, err := s.resolveTreeFoldRootInfo(elemType, nil)
	if err != nil {
		return sequenceRewriteArmInfoBackend{}, fmt.Errorf("sequence rewrite arms currently support only `_`, a bare element binding name, or an exact tree target with an optional bind name")
	}
	for _, memberType := range sequenceRewriteRootExactMembersBackend(root) {
		if treeExactMemberSurfaceName(memberType) == arm.TargetName {
			return sequenceRewriteArmInfoBackend{bindType: memberType, bindName: arm.BindName, alwaysMatches: root.kind == treeFoldRootExact, memberType: memberType}, nil
		}
	}
	return sequenceRewriteArmInfoBackend{}, fmt.Errorf("sequence rewrite arms currently support only `_`, a bare element binding name, or an exact tree target with an optional bind name")
}

func (s *functionState) emitSequenceRewriteTreeArmMatch(elemValue C.LLVMValueRef, memberType semantic.Type, bodyBB C.LLVMBasicBlockRef, nextBB C.LLVMBasicBlockRef, name string) error {
	tagValue, err := s.emitTreeHandleTagValue(elemValue, name+".tag")
	if err != nil {
		return err
	}
	tagConst, err := s.treeExactMemberTagConstant(memberType)
	if err != nil {
		return err
	}
	matchValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree(name+".match"))
	C.LLVMBuildCondBr(s.builder, matchValue, bodyBB, nextBB)
	return nil
}

func (s *functionState) emitSequenceRewriteEmitExpr(expr *ast.EmitExpr) (C.LLVMValueRef, semantic.Type, error) {
	if s.currentSequenceRewrite == nil || s.currentSequenceRewrite.outType == nil {
		return nil, nil, fmt.Errorf("emit is only allowed inside sequence rewrite arms")
	}
	voidType := s.g.result.NamedTypes["void"]
	if expr == nil || expr.Nothing || expr.Value == nil {
		return nil, voidType, nil
	}
	if expr.All {
		sequenceValue, actualType, err := s.emitExpr(expr.Value, nil)
		if err != nil {
			return nil, nil, err
		}
		sequenceElemType, ok := sequenceRewriteCarrierElemTypeBackend(actualType)
		if !ok || sequenceElemType == nil {
			return nil, nil, fmt.Errorf("emit all expects a darray or dview source, got %s", actualType.String())
		}
		sequenceViewType := &semantic.DArrayViewType{Elem: sequenceElemType, SurfaceName: "dview"}
		sequenceViewValue, err := s.coerceValue(sequenceValue, actualType, sequenceViewType)
		if err != nil {
			return nil, nil, err
		}
		if err := s.emitSequenceRewriteAppendView(s.currentSequenceRewrite.outPtr, s.currentSequenceRewrite.outType, sequenceViewValue, sequenceViewType, "sequence.emit.all"); err != nil {
			return nil, nil, err
		}
		return nil, voidType, nil
	}
	itemValue, _, err := s.emitExpr(expr.Value, s.currentSequenceRewrite.outType.Elem)
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitSequenceRewriteAppend(s.currentSequenceRewrite.outPtr, s.currentSequenceRewrite.outType, itemValue, "sequence.emit"); err != nil {
		return nil, nil, err
	}
	return nil, voidType, nil
}

func (s *functionState) emitSequenceRewriteAppend(outPtr C.LLVMValueRef, outType *semantic.DArrayType, itemValue C.LLVMValueRef, name string) error {
	if outPtr == nil || outType == nil {
		return fmt.Errorf("missing sequence rewrite output")
	}
	owner, ok := s.lookupTreeAllocOwner()
	if !ok || owner.arenaRef == nil {
		return fmt.Errorf("sequence rewrite requires an active in <arena>: scope")
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(outPtr, outType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree(name+".count"))
	neededValue := C.LLVMBuildAdd(s.builder, currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), cStringFree(name+".needed"))
	if err := s.emitBuiltinDArrayEnsureCapacity(outPtr, outType, owner.arenaRef, neededValue, name); err != nil {
		return err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(outPtr, outType)
	if err != nil {
		return err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	elemLLVMType, err := s.g.lowerType(outType.Elem)
	if err != nil {
		return err
	}
	slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree(name+".slot"))
	C.LLVMBuildStore(s.builder, itemValue, slotPtr)
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	return nil
}

func (s *functionState) emitSequenceRewriteAppendView(outPtr C.LLVMValueRef, outType *semantic.DArrayType, viewValue C.LLVMValueRef, viewType *semantic.DArrayViewType, name string) error {
	if outPtr == nil || outType == nil || viewType == nil {
		return fmt.Errorf("missing sequence rewrite output")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	elemLLVMType, err := s.g.lowerType(outType.Elem)
	if err != nil {
		return err
	}
	dataValue := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".data"))
	lenValue := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".len"))
	indexPtr := C.LLVMBuildAlloca(s.builder, usizeLLVMType, cStringFree(name+".index"))
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	C.LLVMBuildStore(s.builder, zero, indexPtr)
	loopBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".loop"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
	doneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".done"))
	C.LLVMBuildBr(s.builder, loopBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBB)
	indexValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree(name+".index.load"))
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, lenValue, cStringFree(name+".cond"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, doneBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dataValue, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".elem.ptr"))
	elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree(name+".elem"))
	if err := s.emitSequenceRewriteAppend(outPtr, outType, elemValue, name); err != nil {
		return err
	}
	nextValue := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(name+".next"))
	C.LLVMBuildStore(s.builder, nextValue, indexPtr)
	C.LLVMBuildBr(s.builder, loopBB)

	C.LLVMPositionBuilderAtEnd(s.builder, doneBB)
	return nil
}

func (s *functionState) emitSequenceRewriteArmBody(body []ast.Stmt) error {
	if len(body) == 0 {
		return nil
	}
	scope := s.scope
	for _, stmt := range body {
		if err := s.emitStmt(stmt); err != nil {
			s.discardScopeCleanups(scope)
			return err
		}
		if s.currentBlockTerminated() {
			s.discardScopeCleanups(scope)
			return nil
		}
	}
	return s.emitScopeCleanups(scope)
}

func (s *functionState) emitSequenceRewriteArms(elemValue C.LLVMValueRef, elemType semantic.Type, arms []ast.VisitArm, outPtr C.LLVMValueRef, outType *semantic.DArrayType, name string) error {
	endBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	for index, arm := range arms {
		armInfo, err := s.resolveSequenceRewriteArmInfoBackend(elemType, arm)
		if err != nil {
			return err
		}
		var nextBB C.LLVMBasicBlockRef
		if !armInfo.alwaysMatches {
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.body"))
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.next"))
			if err := s.emitSequenceRewriteTreeArmMatch(elemValue, armInfo.memberType, bodyBB, nextBB, name+".tree"); err != nil {
				return err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		}
		s.pushScope()
		if armInfo.bindName != "" && armInfo.bindType != nil {
			if err := s.emitMoveBindLocal(armInfo.bindName, armInfo.bindType, elemValue); err != nil {
				s.popScope()
				return err
			}
		}
		if arm.Guard != nil {
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.body"))
			if nextBB == nil {
				nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.next"))
			}
			guardValue, _, err := s.emitExpr(arm.Guard, s.g.result.NamedTypes["bool"])
			if err != nil {
				s.popScope()
				return err
			}
			C.LLVMBuildCondBr(s.builder, guardValue, bodyBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		}
		saved := s.currentSequenceRewrite
		s.currentSequenceRewrite = &sequenceRewriteCodegenContext{outPtr: outPtr, outType: outType}
		err = s.emitSequenceRewriteArmBody(arm.Body)
		s.currentSequenceRewrite = saved
		if err != nil {
			s.popScope()
			return err
		}
		if !s.currentBlockTerminated() {
			C.LLVMBuildBr(s.builder, endBB)
		}
		s.popScope()
		if armInfo.alwaysMatches && arm.Guard == nil {
			break
		}
		if nextBB != nil {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
			if index == len(arms)-1 {
				C.LLVMBuildBr(s.builder, endBB)
			}
		}
	}
	if !s.currentBlockTerminated() {
		C.LLVMBuildBr(s.builder, endBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, endBB)
	return nil
}

func (s *functionState) emitSequenceRewriteExpr(expr *ast.FoldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing sequence rewrite expression")
	}
	resultType, ok := s.exprType(expr).(*semantic.DArrayType)
	if !ok || resultType == nil {
		return nil, nil, fmt.Errorf("sequence rewrite requires a darray result type")
	}
	sourceType := s.exprType(expr.Value)
	sourceElemType, ok := sequenceRewriteCarrierElemTypeBackend(sourceType)
	if !ok || sourceElemType == nil {
		return nil, nil, fmt.Errorf("sequence rewrite expects a darray or dview source, got %s", sourceType.String())
	}
	sourceValue, _, err := s.emitExpr(expr.Value, sourceType)
	if err != nil {
		return nil, nil, err
	}
	sourceViewType := &semantic.DArrayViewType{Elem: sourceElemType, SurfaceName: "dview"}
	sourceViewValue, err := s.coerceValue(sourceValue, sourceType, sourceViewType)
	if err != nil {
		return nil, nil, err
	}
	zeroOut, err := s.zeroValue(resultType)
	if err != nil {
		return nil, nil, err
	}
	outPtr, err := s.createEntryAlloca("sequence.rewrite.out", resultType)
	if err != nil {
		return nil, nil, err
	}
	C.LLVMBuildStore(s.builder, zeroOut, outPtr)
	viewLLVMType, err := s.g.lowerType(sourceViewType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	elemLLVMType, err := s.g.lowerType(sourceElemType)
	if err != nil {
		return nil, nil, err
	}
	viewTemp := C.LLVMBuildAlloca(s.builder, viewLLVMType, cStringFree("sequence.rewrite.view"))
	C.LLVMBuildStore(s.builder, sourceViewValue, viewTemp)
	dataValue := C.LLVMBuildExtractValue(s.builder, sourceViewValue, 0, cStringFree("sequence.rewrite.data"))
	lenValue := C.LLVMBuildExtractValue(s.builder, sourceViewValue, 1, cStringFree("sequence.rewrite.len"))
	indexPtr := C.LLVMBuildAlloca(s.builder, usizeLLVMType, cStringFree("sequence.rewrite.index"))
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	C.LLVMBuildStore(s.builder, zero, indexPtr)
	loopBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.loop"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.body"))
	endBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("sequence.rewrite.done"))
	C.LLVMBuildBr(s.builder, loopBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBB)
	indexValue := C.LLVMBuildLoad2(s.builder, usizeLLVMType, indexPtr, cStringFree("sequence.rewrite.index.load"))
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, lenValue, cStringFree("sequence.rewrite.cond"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, endBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	elemPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, dataValue, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree("sequence.rewrite.elem.ptr"))
	elemValue := C.LLVMBuildLoad2(s.builder, elemLLVMType, elemPtr, cStringFree("sequence.rewrite.elem"))
	if err := s.emitSequenceRewriteArms(elemValue, sourceElemType, expr.Arms, outPtr, resultType, "sequence.rewrite.arm"); err != nil {
		return nil, nil, err
	}
	if !s.currentBlockTerminated() {
		nextValue := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree("sequence.rewrite.next"))
		C.LLVMBuildStore(s.builder, nextValue, indexPtr)
		C.LLVMBuildBr(s.builder, loopBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, endBB)
	resultValue := C.LLVMBuildLoad2(s.builder, s.mustLowerType(resultType), outPtr, cStringFree("sequence.rewrite.result"))
	return resultValue, resultType, nil
}

func (s *functionState) mustLowerType(t semantic.Type) C.LLVMTypeRef {
	llvmType, err := s.g.lowerType(t)
	if err != nil {
		panic(err)
	}
	return llvmType
}
