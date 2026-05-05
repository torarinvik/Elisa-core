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

func (s *functionState) emitTreeFoldHelperBody(expr *ast.FoldExpr, helper *treeFoldHelperInfo, nodeParam C.LLVMValueRef, envParam C.LLVMValueRef) error {
	savedTreeOwner := s.treeAllocOwner
	defer func() {
		s.treeAllocOwner = savedTreeOwner
	}()
	if helper.hasEnvParam && helper.envStruct != nil {
		for i, capture := range helper.captures {
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, helper.envStruct, envParam, C.unsigned(i), cStringFree(helper.name+".env.field"))
			fieldValue := C.LLVMBuildLoad2(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), fieldPtr, cStringFree(helper.name+".env.value"))
			s.defineBinding(capture.name, valueBinding{ptr: fieldValue, typ: capture.binding.typ, mutable: capture.binding.mutable})
		}
	}
	if helper.rewrite {
		s.treeAllocOwner = treeAllocOwnerBinding{isPerm: helper.rewriteOwnerPerm}
		if binding, ok := s.lookupBinding(treeRewriteOwnerStoreCaptureName); ok {
			storeType, _ := binding.typ.(*semantic.TreeStoreType)
			if storeType != nil {
				storeValue, err := s.loadValue(binding.ptr, binding.typ, helper.name+".rewrite.owner.store")
				if err != nil {
					return err
				}
				s.treeAllocOwner = treeAllocOwnerBinding{storeValue: storeValue, storeType: storeType}
			}
		} else if binding, ok := s.lookupBinding(treeRewriteOwnerArenaCaptureName); ok {
			arenaRef, err := s.loadValue(binding.ptr, binding.typ, helper.name+".rewrite.owner.arena")
			if err != nil {
				return err
			}
			s.treeAllocOwner = treeAllocOwnerBinding{arenaRef: arenaRef}
		}
	}

	var (
		resultValue C.LLVMValueRef
		terminated  bool
		err         error
	)
	switch helper.root.kind {
	case treeFoldRootFamily:
		var exhaustive bool
		if visitArmsHaveGuard(expr.Arms) {
			exhaustive = true
			members := semantic.TreeFamilyExactMembersInTagOrder(helper.root.family)
			for _, member := range members {
				if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), expr.Arms) {
					exhaustive = false
					break
				}
			}
			resultValue, terminated, err = s.emitTreeFoldGuardedSwitchDispatch(helper, envParam, nodeParam, members, expr.Arms, exhaustive, helper.name+".node")
		} else {
			var relevant []treeVisitExactArm
			relevant, exhaustive, err = s.treeVisitRelevantExactArms(helper.root.family, expr.Arms)
			if err == nil {
				resultValue, terminated, err = s.emitTreeFoldSwitchDispatch(helper, envParam, nodeParam, relevant, exhaustive, helper.name+".node")
			}
		}
	case treeFoldRootCategory:
		var exhaustive bool
		if visitArmsHaveGuard(expr.Arms) {
			exhaustive = true
			members := treeCategoryMembersInTagOrder(helper.root.category)
			for _, member := range members {
				if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), expr.Arms) {
					exhaustive = false
					break
				}
			}
			resultValue, terminated, err = s.emitTreeFoldGuardedSwitchDispatch(helper, envParam, nodeParam, members, expr.Arms, exhaustive, helper.name+".category")
		} else {
			var relevant []treeVisitExactArm
			relevant, exhaustive, err = s.treeVisitRelevantCategoryExactArms(helper.root.category, expr.Arms)
			if err == nil {
				resultValue, terminated, err = s.emitTreeFoldSwitchDispatch(helper, envParam, nodeParam, relevant, exhaustive, helper.name+".category")
			}
		}
	case treeFoldRootExact:
		resultValue, terminated, err = s.emitTreeFoldExactDispatch(helper, envParam, nodeParam, helper.root.exact, expr.Arms)
	default:
		err = fmt.Errorf("unsupported fold root kind")
	}
	if err != nil {
		return err
	}
	if terminated || s.currentBlockTerminated() {
		return nil
	}
	return s.emitFunctionReturn(resultValue, helper.resultType)
}
func (s *functionState) defineTreeFoldHelper(expr *ast.FoldExpr, helper *treeFoldHelperInfo) error {
	builder := C.LLVMCreateBuilderInContext(s.g.context)
	defer C.LLVMDisposeBuilder(builder)
	entry := C.LLVMAppendBasicBlockInContext(s.g.context, helper.fnValue, cStringFree(helper.name+".entry"))
	C.LLVMPositionBuilderAtEnd(builder, entry)
	state := &functionState{
		g:                    s.g,
		decl:                 s.decl,
		fnValue:              helper.fnValue,
		fnType:               helper.funcType,
		builder:              builder,
		typeMap:              s.typeMap,
		specializedFuncTypes: s.specializedFuncTypes,
	}
	paramOffset := 0
	if _, ok := nonVoidErrorUnion(helper.resultType); ok {
		state.resultSlot = C.LLVMGetParam(helper.fnValue, 0)
		paramOffset = 1
	}
	nodeParam := C.LLVMGetParam(helper.fnValue, C.unsigned(paramOffset))
	var envParam C.LLVMValueRef
	if helper.hasEnvParam {
		envParam = C.LLVMGetParam(helper.fnValue, C.unsigned(paramOffset+1))
	}
	return state.emitTreeFoldHelperBody(expr, helper, nodeParam, envParam)
}
func (s *functionState) emitFoldExpr(expr *ast.FoldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil {
		return nil, nil, fmt.Errorf("missing fold expression")
	}
	if expr.Keyword == "rewrite" {
		if _, ok := sequenceRewriteTargetTypeExprBackend(expr.Root); ok {
			return s.emitSequenceRewriteExpr(expr)
		}
	}
	actualType := s.exprType(expr.Value)
	resultType := s.exprType(expr)
	root, err := s.resolveTreeFoldRootInfo(actualType, expr.Root)
	if err != nil {
		return nil, nil, err
	}
	treeValue, _, err := s.emitExpr(expr.Value, root.bindType())
	if err != nil {
		return nil, nil, err
	}
	captures := s.collectTreeFoldCaptures(expr)
	envValue, envStruct, err := s.buildTreeFoldEnv(captures, "fold")
	if err != nil {
		return nil, nil, err
	}
	helperResultType := resultType
	rewrite := expr.Keyword == "rewrite"
	if rewrite {
		helperResultType = root.bindType()
	}
	helper, err := s.newTreeFoldHelper(expr, root, helperResultType, captures, envStruct, rewrite)
	if err != nil {
		return nil, nil, err
	}
	if err := s.defineTreeFoldHelper(expr, helper); err != nil {
		return nil, nil, err
	}
	value, err := s.emitTreeFoldHelperCall(helper, treeValue, envValue, "fold")
	if err != nil {
		return nil, nil, err
	}
	return value, resultType, nil
}
