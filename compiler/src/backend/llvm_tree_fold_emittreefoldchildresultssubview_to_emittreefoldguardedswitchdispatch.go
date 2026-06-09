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

func (s *functionState) emitTreeFoldChildResultsSubview(childViewValue C.LLVMValueRef, sourceType semantic.Type, offsetValue C.LLVMValueRef, countValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	viewType := &semantic.ViewType{Elem: sourceType, SurfaceName: "view"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, err
	}
	sourceLLVMType, err := s.g.lowerType(sourceType)
	if err != nil {
		return nil, nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, childViewValue, 0, cStringFree(name+".data"))
	subData := C.LLVMBuildGEP2(s.builder, sourceLLVMType, viewData, llvmValueSlicePtr([]C.LLVMValueRef{offsetValue}), 1, cStringFree(name+".sub.data"))
	subView := C.LLVMGetUndef(viewLLVMType)
	subView = C.LLVMBuildInsertValue(s.builder, subView, subData, 0, cStringFree(name+".sub.view.data"))
	subView = C.LLVMBuildInsertValue(s.builder, subView, countValue, 1, cStringFree(name+".sub.view.len"))
	return subView, viewType, nil
}
func (s *functionState) emitTreeFoldNamedChildBindingLocals(helper *treeFoldHelperInfo, nodeValue C.LLVMValueRef, memberType semantic.Type, childViewValue C.LLVMValueRef, arm ast.VisitArm, name string) error {
	if len(arm.ChildBindings) == 0 {
		return nil
	}
	requested := make(map[string]string, len(arm.ChildBindings))
	for _, binding := range arm.ChildBindings {
		if binding.FieldName == "" || binding.BindName == "" || binding.BindName == "_" {
			continue
		}
		requested[binding.FieldName] = binding.BindName
	}
	if len(requested) == 0 {
		return nil
	}
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return fmt.Errorf("fold child binding source %s is missing tree family metadata", treeExactMemberSurfaceName(memberType))
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	offsetValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
	oneValue := C.LLVMConstInt(usizeLLVMType, 1, 0)
	boundFields := map[string]bool{}
	for _, childBinding := range semantic.TreeStructuralChildBindings(memberType) {
		bindName, wanted := requested[childBinding.Name]
		switch childBinding.Relation {
		case ast.EnumPayloadRelationChild:
			childResultType := helper.resultType
			if helper.rewrite {
				if bindingType, ok := semantic.TreeRewriteChildBindingType(childBinding.Type, childBinding.Relation); ok {
					if optionalBinding, ok := bindingType.(*semantic.OptionalType); ok {
						childResultType = optionalBinding.Value
					} else {
						childResultType = bindingType
					}
				}
			}
			fieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, childBinding.Name, name+"."+childBinding.Name)
			if err != nil {
				return err
			}
			childCount := oneValue
			presentValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
			optionalType, optionalChild := childBinding.Type.(*semantic.OptionalType)
			if optionalChild {
				presentValue, err = s.extractOptionalPresent(fieldValue, optionalType)
				if err != nil {
					return err
				}
				childCount = C.LLVMBuildSelect(s.builder, presentValue, oneValue, zeroValue, cStringFree(name+"."+childBinding.Name+".count"))
			}
			if wanted {
				if optionalChild {
					boundType := &semantic.OptionalType{Value: childResultType}
					boundLLVMType, err := s.g.lowerType(boundType)
					if err != nil {
						return err
					}
					presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".some"))
					absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".none"))
					contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".cont"))
					C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

					C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
					childResult, err := s.emitTreeFoldChildResultAtIndex(childViewValue, helper.childResultsElemType(), childResultType, offsetValue, name+"."+childBinding.Name)
					if err != nil {
						return err
					}
					presentValue, err := s.buildOptionalSome(boundType, childResult)
					if err != nil {
						return err
					}
					presentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, absentBB)
					absentValue, err := s.buildOptionalNone(boundType)
					if err != nil {
						return err
					}
					absentEnd := C.LLVMGetInsertBlock(s.builder)
					C.LLVMBuildBr(s.builder, contBB)

					C.LLVMPositionBuilderAtEnd(s.builder, contBB)
					boundPhi := C.LLVMBuildPhi(s.builder, boundLLVMType, cStringFree(name+"."+childBinding.Name+".value"))
					C.LLVMAddIncoming(boundPhi, llvmValueSlicePtr([]C.LLVMValueRef{presentValue, absentValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{presentEnd, absentEnd}), 2)
					if err := s.emitMoveBindLocal(bindName, boundType, boundPhi); err != nil {
						return err
					}
				} else {
					childResult, err := s.emitTreeFoldChildResultAtIndex(childViewValue, helper.childResultsElemType(), childResultType, offsetValue, name+"."+childBinding.Name)
					if err != nil {
						return err
					}
					if err := s.emitMoveBindLocal(bindName, childResultType, childResult); err != nil {
						return err
					}
				}
				boundFields[childBinding.Name] = true
			}
			offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, childCount, cStringFree(name+"."+childBinding.Name+".offset.next"))
		case ast.EnumPayloadRelationChildren:
			fieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, childBinding.Name, name+"."+childBinding.Name)
			if err != nil {
				return err
			}
			countValue, err := s.emitTreeStructuralSequenceCount(fieldValue, childBinding.Type, name+"."+childBinding.Name+".count")
			if err != nil {
				return err
			}
			if wanted {
				subViewValue, subViewType, err := s.emitTreeFoldChildResultsSubview(childViewValue, helper.childResultsElemType(), offsetValue, countValue, name+"."+childBinding.Name)
				if err != nil {
					return err
				}
				if optionalType, ok := childBinding.Type.(*semantic.OptionalType); ok {
					presentValue, err := s.extractOptionalPresent(fieldValue, optionalType)
					if err != nil {
						return err
					}
					boundType := &semantic.OptionalType{Value: subViewType}
					boundValue, err := s.buildOptionalValue(boundType, presentValue, subViewValue)
					if err != nil {
						return err
					}
					if err := s.emitMoveBindLocal(bindName, boundType, boundValue); err != nil {
						return err
					}
				} else {
					if err := s.emitMoveBindLocal(bindName, subViewType, subViewValue); err != nil {
						return err
					}
				}
				boundFields[childBinding.Name] = true
			}
			offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, countValue, cStringFree(name+"."+childBinding.Name+".offset.next"))
		}
	}
	for fieldName := range requested {
		if !boundFields[fieldName] {
			return fmt.Errorf("fold arm child binding %q was not resolved for %s", fieldName, treeExactMemberSurfaceName(memberType))
		}
	}
	return nil
}
func (s *functionState) emitTreeFoldArmValue(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, arm ast.VisitArm) (C.LLVMValueRef, bool, error) {
	armNodeValue := nodeValue
	if helper != nil && helper.root.kind == treeFoldRootFamily && helper.root.family != nil {
		var err error
		armNodeValue, err = s.emitTreeRootDispatchMemberValue(nodeValue, helper.root.family, memberType, "fold.arm.member")
		if err != nil {
			return nil, false, err
		}
	}
	childViewValue, err := s.emitTreeFoldChildResultsView(helper, envValue, armNodeValue, memberType, "fold.arm")
	if err != nil {
		return nil, false, err
	}
	s.pushScope()
	if arm.BindName != "" && arm.BindName != "_" {
		if err := s.emitMoveBindLocal(arm.BindName, memberType, armNodeValue); err != nil {
			s.popScope()
			return nil, false, err
		}
	}
	if arm.ChildResultsName != "" && arm.ChildResultsName != "_" {
		childViewType := &semantic.ViewType{Elem: helper.childResultsElemType(), SurfaceName: "view"}
		if err := s.emitMoveBindLocal(arm.ChildResultsName, childViewType, childViewValue); err != nil {
			s.popScope()
			return nil, false, err
		}
	}
	if err := s.emitTreeFoldNamedChildBindingLocals(helper, armNodeValue, memberType, childViewValue, arm, "fold.arm.named"); err != nil {
		s.popScope()
		return nil, false, err
	}
	savedRewriteDefault := s.treeRewriteDefault
	if helper.rewrite {
		if _, exact := semantic.TreeExactTag(memberType); exact {
			s.treeRewriteDefault = &treeRewriteDefaultContext{memberType: memberType, nodeValue: armNodeValue, childViewValue: childViewValue, childResultType: helper.childResultsElemType()}
		} else {
			s.treeRewriteDefault = nil
		}
	}
	armResultType := helper.armResultType(memberType, arm)
	armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, armResultType)
	s.treeRewriteDefault = savedRewriteDefault
	s.popScope()
	if err == nil && reachable && helper != nil && armValue != nil {
		armValue, err = s.coerceValue(armValue, armResultType, helper.resultType)
	}
	return armValue, reachable, err
}
func (s *functionState) emitTreeFoldArmSequence(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, arms []ast.VisitArm, failUnreachable bool, name string) (C.LLVMValueRef, bool, error) {
	armNodeValue := nodeValue
	if helper != nil && helper.root.kind == treeFoldRootFamily && helper.root.family != nil {
		var err error
		armNodeValue, err = s.emitTreeRootDispatchMemberValue(nodeValue, helper.root.family, memberType, name+".member")
		if err != nil {
			return nil, false, err
		}
	}
	if len(arms) == 0 {
		if helper != nil && helper.hasImplicitRewriteDefault() {
			value, err := s.emitTreeFoldImplicitRewriteDefault(helper, envValue, armNodeValue, memberType, name)
			return value, false, err
		}
		if semantic.IsNeverType(helper.resultType) || failUnreachable {
			C.LLVMBuildUnreachable(s.builder)
			return nil, true, nil
		}
		llvmType, err := s.g.lowerType(helper.resultType)
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
		childViewValue, err := s.emitTreeFoldChildResultsView(helper, envValue, armNodeValue, memberType, name+".arm")
		if err != nil {
			return nil, false, err
		}
		s.pushScope()
		if arm.BindName != "" && arm.BindName != "_" {
			if err := s.emitMoveBindLocal(arm.BindName, memberType, armNodeValue); err != nil {
				s.popScope()
				return nil, false, err
			}
		}
		if arm.ChildResultsName != "" && arm.ChildResultsName != "_" {
			childViewType := &semantic.ViewType{Elem: helper.childResultsElemType(), SurfaceName: "view"}
			if err := s.emitMoveBindLocal(arm.ChildResultsName, childViewType, childViewValue); err != nil {
				s.popScope()
				return nil, false, err
			}
		}
		if err := s.emitTreeFoldNamedChildBindingLocals(helper, armNodeValue, memberType, childViewValue, arm, name+".named"); err != nil {
			s.popScope()
			return nil, false, err
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
		savedRewriteDefault := s.treeRewriteDefault
		if helper.rewrite {
			if _, exact := semantic.TreeExactTag(memberType); exact {
				s.treeRewriteDefault = &treeRewriteDefaultContext{memberType: memberType, nodeValue: armNodeValue, childViewValue: childViewValue, childResultType: helper.childResultsElemType()}
			} else {
				s.treeRewriteDefault = nil
			}
		}
		armResultType := helper.armResultType(memberType, arm)
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, armResultType)
		s.treeRewriteDefault = savedRewriteDefault
		if err != nil {
			s.popScope()
			return nil, false, err
		}
		if reachable && !s.currentBlockTerminated() {
			if helper != nil && armValue != nil {
				armValue, err = s.coerceValue(armValue, armResultType, helper.resultType)
				if err != nil {
					s.popScope()
					return nil, false, err
				}
			}
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, inBlock)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
		s.popScope()
		C.LLVMPositionBuilderAtEnd(s.builder, nextBB)
	}
	if helper != nil && helper.hasImplicitRewriteDefault() {
		value, err := s.emitTreeFoldImplicitRewriteDefault(helper, envValue, armNodeValue, memberType, name)
		if err != nil {
			return nil, false, err
		}
		inBlock := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, inBlock)
		C.LLVMBuildBr(s.builder, mergeBB)
	} else if semantic.IsNeverType(helper.resultType) || failUnreachable {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(helper.resultType)
		if err != nil {
			return nil, false, err
		}
		inBlock := C.LLVMGetInsertBlock(s.builder)
		incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		incomingBlocks = append(incomingBlocks, inBlock)
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return s.emitTreeSwitchMergedValue(helper.resultType, incomingValues, incomingBlocks, name+".phi")
}
func (s *functionState) emitTreeFoldExactDispatch(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, arms []ast.VisitArm) (C.LLVMValueRef, bool, error) {
	if visitArmsHaveGuard(arms) {
		memberName := treeExactMemberSurfaceName(memberType)
		return s.emitTreeFoldArmSequence(helper, envValue, nodeValue, memberType, exactTreeVisitArms(memberName, arms), visitArmsCoverExactMember(memberName, arms), "fold.exact")
	}
	arm, ok, _ := exactTreeVisitArm(treeExactMemberSurfaceName(memberType), arms)
	if !ok {
		if helper != nil && helper.hasImplicitRewriteDefault() {
			value, err := s.emitTreeFoldImplicitRewriteDefault(helper, envValue, nodeValue, memberType, "fold.exact")
			return value, false, err
		}
		if semantic.IsNeverType(helper.resultType) {
			C.LLVMBuildUnreachable(s.builder)
			return nil, true, nil
		}
		llvmType, err := s.g.lowerType(helper.resultType)
		if err != nil {
			return nil, false, err
		}
		return C.LLVMGetUndef(llvmType), false, nil
	}
	value, reachable, err := s.emitTreeFoldArmValue(helper, envValue, nodeValue, memberType, arm)
	return value, !reachable, err
}
func (s *functionState) emitTreeFoldSwitchDispatch(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, relevant []treeVisitExactArm, exhaustive bool, name string) (C.LLVMValueRef, bool, error) {
	if len(relevant) == 0 && (helper == nil || !helper.hasImplicitRewriteDefault()) {
		return nil, false, fmt.Errorf("fold over %s has no relevant arms", helper.root.bindType().String())
	}
	var tagValue C.LLVMValueRef
	var err error
	if helper.root.kind == treeFoldRootCategory && helper.root.category != nil {
		tagValue, err = s.extractTreeCategoryTagValue(nodeValue, helper.root.category)
	} else if helper.root.kind == treeFoldRootFamily && helper.root.family != nil {
		tagValue, err = s.emitTreeRootTagValue(nodeValue, helper.root.family, name+".tag")
	} else {
		tagValue, err = s.emitTreeHandleTagValue(nodeValue, name+".tag")
	}
	if err != nil {
		return nil, false, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(relevant)))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevant)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevant)+1)
	coveredMembers := make(map[string]bool, len(relevant))
	for _, armInfo := range relevant {
		if armInfo.member == nil {
			continue
		}
		coveredMembers[treeExactMemberSurfaceName(armInfo.member)] = true
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm"))
		tagConst, err := s.treeExactMemberTagConstant(armInfo.member)
		if err != nil {
			return nil, false, err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		armValue, reachable, err := s.emitTreeFoldArmValue(helper, envValue, nodeValue, armInfo.member, armInfo.arm)
		if err != nil {
			return nil, false, err
		}
		if reachable && !s.currentBlockTerminated() {
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if helper != nil && helper.hasImplicitRewriteDefault() && !exhaustive {
		if err := s.emitTreeFoldImplicitRewriteDefaultSwitch(helper, envValue, nodeValue, tagValue, coveredMembers, mergeBB, &incomingValues, &incomingBlocks, name); err != nil {
			return nil, false, err
		}
	} else if semantic.IsNeverType(helper.resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(helper.resultType)
		if err != nil {
			return nil, false, err
		}
		incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return s.emitTreeSwitchMergedValue(helper.resultType, incomingValues, incomingBlocks, name+".phi")
}
func (s *functionState) emitTreeFoldGuardedSwitchDispatch(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, members []semantic.Type, arms []ast.VisitArm, exhaustive bool, name string) (C.LLVMValueRef, bool, error) {
	if len(members) == 0 {
		return nil, false, fmt.Errorf("fold over %s has no relevant arms", helper.root.bindType().String())
	}
	var tagValue C.LLVMValueRef
	var err error
	if helper.root.kind == treeFoldRootCategory && helper.root.category != nil {
		tagValue, err = s.extractTreeCategoryTagValue(nodeValue, helper.root.category)
	} else if helper.root.kind == treeFoldRootFamily && helper.root.family != nil {
		tagValue, err = s.emitTreeRootTagValue(nodeValue, helper.root.family, name+".tag")
	} else {
		tagValue, err = s.emitTreeHandleTagValue(nodeValue, name+".tag")
	}
	if err != nil {
		return nil, false, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(members)))
	incomingValues := make([]C.LLVMValueRef, 0, len(members)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(members)+1)
	coveredMembers := make(map[string]bool, len(members))
	for _, member := range members {
		memberName := treeExactMemberSurfaceName(member)
		memberArms := exactTreeVisitArms(memberName, arms)
		if len(memberArms) == 0 {
			continue
		}
		coveredMembers[memberName] = true
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm"))
		tagConst, err := s.treeExactMemberTagConstant(member)
		if err != nil {
			return nil, false, err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		armValue, terminated, err := s.emitTreeFoldArmSequence(helper, envValue, nodeValue, member, memberArms, visitArmsCoverExactMember(memberName, arms), name+".exact")
		if err != nil {
			return nil, false, err
		}
		if !terminated && !s.currentBlockTerminated() {
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if helper != nil && helper.hasImplicitRewriteDefault() && !exhaustive {
		if err := s.emitTreeFoldImplicitRewriteDefaultSwitch(helper, envValue, nodeValue, tagValue, coveredMembers, mergeBB, &incomingValues, &incomingBlocks, name); err != nil {
			return nil, false, err
		}
	} else if semantic.IsNeverType(helper.resultType) || exhaustive {
		C.LLVMBuildUnreachable(s.builder)
	} else {
		llvmType, err := s.g.lowerType(helper.resultType)
		if err != nil {
			return nil, false, err
		}
		incomingValues = append(incomingValues, C.LLVMGetUndef(llvmType))
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return s.emitTreeSwitchMergedValue(helper.resultType, incomingValues, incomingBlocks, name+".phi")
}
