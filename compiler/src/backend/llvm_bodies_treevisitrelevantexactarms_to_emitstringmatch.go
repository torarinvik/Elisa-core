//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

void elisa_coreSetBranchWeights(LLVMValueRef branch, LLVMContextRef ctx, unsigned trueWeight, unsigned falseWeight);
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) treeVisitRelevantExactArms(treeType *semantic.TreeType, arms []ast.VisitArm) ([]treeVisitExactArm, bool, error) {
	if treeType == nil {
		return nil, false, fmt.Errorf("missing tree family for visit lowering")
	}
	relevant := make([]treeVisitExactArm, 0, len(semantic.TreeFamilyExactMembersInTagOrder(treeType)))
	exhaustive := false
	for _, member := range semantic.TreeFamilyExactMembersInTagOrder(treeType) {
		memberName := treeExactMemberSurfaceName(member)
		arm, ok, wildcard := exactTreeVisitArm(memberName, arms)
		if !ok {
			continue
		}
		relevant = append(relevant, treeVisitExactArm{arm: arm, member: member, wildcard: wildcard})
		if wildcard && arm.Guard == nil {
			exhaustive = true
		}
	}
	if !exhaustive {
		exhaustive = true
		for _, member := range semantic.TreeFamilyExactMembersInTagOrder(treeType) {
			if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), arms) {
				exhaustive = false
				break
			}
		}
	}
	return relevant, exhaustive, nil
}
func (s *functionState) emitExactVisitArmSequence(value C.LLVMValueRef, bindType semantic.Type, arms []ast.VisitArm, resultType semantic.Type, name string) (C.LLVMValueRef, bool, error) {
	if len(arms) == 0 {
		if semantic.IsNeverType(resultType) {
			C.LLVMBuildUnreachable(s.builder)
			return nil, true, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, false, err
		}
		return C.LLVMGetUndef(llvmType), false, nil
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(arms)+1)
	for i, arm := range arms {
		bodyEntryBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm.entry"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
		}
		C.LLVMBuildBr(s.builder, bodyEntryBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyEntryBB)
		s.pushScope()
		if arm.BindName != "" && arm.BindName != "_" {
			if err := s.emitMoveBindLocal(arm.BindName, bindType, value); err != nil {
				s.popScope()
				return nil, false, err
			}
		}
		if arm.Guard != nil {
			guardBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".guard.body"))
			guardValue, _, err := s.emitExpr(arm.Guard, s.g.result.NamedTypes["bool"])
			if err != nil {
				s.popScope()
				return nil, false, err
			}
			C.LLVMBuildCondBr(s.builder, guardValue, guardBodyBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, guardBodyBB)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, false, err
		}
		if reachable && !s.currentBlockTerminated() {
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, inBlock)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
		C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	}
	if semantic.IsNeverType(resultType) {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, false, err
		}
		inBlock := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		incomingBlocks = append(incomingBlocks, inBlock)
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, true, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], false, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, false, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, false, nil
}
func (s *functionState) emitVisitExpr(expr *ast.VisitExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing visit expression")
	}
	actualType := s.exprType(expr.Value)
	resultType := s.exprType(expr)
	switch tt := semantic.StripAggregateStateType(actualType).(type) {
	case *semantic.TreeNodeType:
		return s.emitFamilyTreeVisitExpr(expr, tt, resultType)
	case *semantic.TreeBlockType:
		return s.emitExactTreeVisitExpr(expr, tt.Name, tt, resultType)
	case *semantic.TreeStructType:
		return s.emitExactTreeVisitExpr(expr, tt.Name, tt, resultType)
	}
	categoryType, _, ok := resolveMatchableTreeCategoryTypeBackend(actualType)
	if !ok || categoryType == nil {
		return nil, nil, fmt.Errorf("visit expression lowering currently requires a tree category, tree block, or tree struct source, got %s", actualType.String())
	}
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	relevantArms, exhaustive, err := s.treeVisitRelevantArms(categoryType, expr.Arms)
	if err != nil {
		return nil, nil, err
	}
	if len(relevantArms) == 0 {
		return nil, nil, fmt.Errorf("visit expression over %s has no relevant arms", categoryType.Name)
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevantArms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevantArms)+1)

	for i, armInfo := range relevantArms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(relevantArms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.next"))
		}
		if armInfo.wildcard {
			C.LLVMBuildBr(s.builder, bodyBB)
		} else {
			matchPattern := &ast.MatchVariantPattern{Position: armInfo.arm.Position, EnumName: categoryType.Name, Variant: armInfo.variant.Name}
			if _, _, err := s.emitMatchPatternTest(matchPattern, treeValue, nil, actualType, nil, expr.Value, nil, bodyBB, nextBB); err != nil {
				return nil, nil, err
			}
		}
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if armInfo.arm.BindName != "" && armInfo.arm.BindName != "_" && armInfo.variant != nil {
			viewType := categoryType.VariantViewType(armInfo.variant)
			if err := s.emitMoveBindLocal(armInfo.arm.BindName, viewType, treeValue); err != nil {
				s.popScope()
				return nil, nil, err
			}
		}
		if armInfo.arm.Guard != nil {
			guardBodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.expr.guard.body"))
			guardValue, _, err := s.emitExpr(armInfo.arm.Guard, s.g.result.NamedTypes["bool"])
			if err != nil {
				s.popScope()
				return nil, nil, err
			}
			C.LLVMBuildCondBr(s.builder, guardValue, guardBodyBB, nextBB)
			C.LLVMPositionBuilderAtEnd(s.builder, guardBodyBB)
		}
		armValue, reachable, err := s.emitMatchExprArmBody(armInfo.arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			armEnd := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, armEnd)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
		if nextBB != mergeBB && !armInfo.wildcard {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		failEnd := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, failEnd)
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.expr.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitFamilyTreeVisitExpr(expr *ast.VisitExpr, rootType *semantic.TreeNodeType, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	if rootType == nil || rootType.Family == nil {
		return nil, nil, fmt.Errorf("missing family-root visit metadata")
	}
	treeValue, _, err := s.emitExpr(expr.Value, nil)
	if err != nil {
		return nil, nil, err
	}
	if visitArmsHaveGuard(expr.Arms) {
		mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.end"))
		failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.fail"))
		tagValue, err := s.emitTreeHandleTagValue(treeValue, "visit.node")
		if err != nil {
			return nil, nil, err
		}
		members := semantic.TreeFamilyExactMembersInTagOrder(rootType.Family)
		switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(members)))
		incomingValues := make([]C.LLVMValueRef, 0, len(members)+1)
		incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(members)+1)
		for _, member := range members {
			memberName := treeExactMemberSurfaceName(member)
			memberArms := exactTreeVisitArms(memberName, expr.Arms)
			if len(memberArms) == 0 {
				continue
			}
			bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.arm"))
			tag, ok := treeExactMemberTag(member)
			if !ok {
				return nil, nil, fmt.Errorf("missing exact tag for %s", memberName)
			}
			tagConst, err := s.errorCodeConstant(tag)
			if err != nil {
				return nil, nil, err
			}
			C.LLVMAddCase(switchInst, tagConst, bodyBB)
			C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
			armValue, terminated, err := s.emitExactVisitArmSequence(treeValue, member, memberArms, resultType, "visit.node.exact")
			if err != nil {
				return nil, nil, err
			}
			if !terminated && !s.currentBlockTerminated() {
				inBlock := C.LLVMGetInsertBlock(s.builder)
				incomingValues = append(incomingValues, armValue)
				incomingBlocks = append(incomingBlocks, inBlock)
				C.LLVMBuildBr(s.builder, mergeBB)
			}
		}
		C.LLVMPositionBuilderAtEnd(s.builder, failBB)
		if semantic.IsNeverType(resultType) {
			C.LLVMBuildUnreachable(s.builder)
		} else {
			llvmType, err := s.g.lowerType(resultType)
			if err != nil {
				return nil, nil, err
			}
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
			incomingBlocks = append(incomingBlocks, inBlock)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
		if len(incomingValues) == 0 {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
			return incomingValues[0], resultType, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.node.phi"))
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
		return phi, resultType, nil
	}
	relevantArms, exhaustive, err := s.treeVisitRelevantExactArms(rootType.Family, expr.Arms)
	if err != nil {
		return nil, nil, err
	}
	if len(relevantArms) == 0 {
		return nil, nil, fmt.Errorf("visit expression over %s has no relevant arms", rootType.String())
	}
	tagValue, err := s.emitTreeHandleTagValue(treeValue, "visit.node")
	if err != nil {
		return nil, nil, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(relevantArms)))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevantArms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevantArms)+1)
	for _, armInfo := range relevantArms {
		if armInfo.member == nil {
			continue
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.node.arm"))
		tag, ok := treeExactMemberTag(armInfo.member)
		if !ok {
			return nil, nil, fmt.Errorf("missing exact tag for %s", treeExactMemberSurfaceName(armInfo.member))
		}
		tagConst, err := s.errorCodeConstant(tag)
		if err != nil {
			return nil, nil, err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if armInfo.arm.BindName != "" && armInfo.arm.BindName != "_" {
			if err := s.emitMoveBindLocal(armInfo.arm.BindName, armInfo.member, treeValue); err != nil {
				s.popScope()
				return nil, nil, err
			}
		}
		armValue, reachable, err := s.emitMatchExprArmBody(armInfo.arm.Body, resultType)
		if err != nil {
			s.popScope()
			return nil, nil, err
		}
		if reachable && !s.currentBlockTerminated() {
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		undefValue := C.LLVMGetUndef(llvmType)
		incomingValues = append(incomingValues, undefValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(resultType) {
		return incomingValues[0], resultType, nil
	}
	llvmType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree("visit.node.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, resultType, nil
}
func (s *functionState) emitExactTreeVisitExpr(expr *ast.VisitExpr, memberName string, bindType semantic.Type, resultType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	value, _, err := s.emitExpr(expr.Value, bindType)
	if err != nil {
		return nil, nil, err
	}
	if visitArmsHaveGuard(expr.Arms) {
		armValue, _, err := s.emitExactVisitArmSequence(value, bindType, exactTreeVisitArms(memberName, expr.Arms), resultType, "visit.exact")
		return armValue, resultType, err
	}
	arm, ok, _ := exactTreeVisitArm(memberName, expr.Arms)
	if !ok {
		if semantic.IsNeverType(resultType) {
			C.LLVMBuildUnreachable(s.builder)
			return nil, resultType, nil
		}
		llvmType, err := s.g.lowerType(resultType)
		if err != nil {
			return nil, nil, err
		}
		return C.LLVMGetUndef(llvmType), resultType, nil
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.exact.end"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("visit.exact.arm"))
	C.LLVMBuildBr(s.builder, bodyBB)
	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	s.pushScope()
	if arm.BindName != "" && arm.BindName != "_" {
		if err := s.emitMoveBindLocal(arm.BindName, bindType, value); err != nil {
			s.popScope()
			return nil, nil, err
		}
	}
	armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, resultType)
	if err != nil {
		s.popScope()
		return nil, nil, err
	}
	var incomingValue C.LLVMValueRef
	hasIncoming := false
	if reachable && !s.currentBlockTerminated() {
		incomingValue = armValue
		hasIncoming = true
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	s.popScope()
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if !hasIncoming {
		C.LLVMBuildUnreachable(s.builder)
		return nil, resultType, nil
	}
	return incomingValue, resultType, nil
}
func matchHasWildcard(arms []ast.MatchArm) bool {
	for _, arm := range arms {
		if _, ok := arm.Pattern.(*ast.MatchWildcardPattern); ok {
			return true
		}
	}
	return false
}
func (s *functionState) emitStringMatch(stmt *ast.MatchStmt) error {
	actualType := s.exprType(stmt.Value)
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.fail"))
	allTerminated := true
	for i, arm := range stmt.Arms {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.arm"))
		var nextBB C.LLVMBasicBlockRef
		if i == len(stmt.Arms)-1 {
			nextBB = failBB
		} else {
			nextBB = C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("match.next"))
		}
		if err := s.emitStringMatchPatternTest(arm.Pattern, stmt.Value, actualType, bodyBB, nextBB); err != nil {
			return err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		s.pushScope()
		if err := s.emitBlockInCurrentScope(arm.Body); err != nil {
			s.popScope()
			return err
		}
		s.popScope()
		if !s.currentBlockTerminated() {
			allTerminated = false
			C.LLVMBuildBr(s.builder, mergeBB)
		}

		if nextBB != mergeBB {
			C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
		}
	}

	hasWildcard := matchHasWildcard(stmt.Arms)
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		C.LLVMBuildBr(s.builder, mergeBB)
	}

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	if allTerminated && hasWildcard {
		C.LLVMBuildUnreachable(s.builder)
	}
	return nil
}
