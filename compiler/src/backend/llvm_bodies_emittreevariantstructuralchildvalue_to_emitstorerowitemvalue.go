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

func (s *functionState) emitTreeVariantStructuralChildValue(nodeValue C.LLVMValueRef, categoryType *semantic.TreeCategoryType, variant *semantic.EnumVariant, indexValue C.LLVMValueRef, itemType semantic.Type, name string) (C.LLVMValueRef, error) {
	if variant == nil {
		return nil, fmt.Errorf("tree category %s is missing variant metadata", categoryType.Name)
	}
	if !variant.HasStructuralPayloads() {
		return nil, fmt.Errorf("tree category %s variant %s has no structural children", categoryType.Name, variant.Name)
	}
	payloadValues, err := s.extractTreeVariantPayloadValues(nodeValue, categoryType, variant)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(variant.Payload))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(variant.Payload))
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	remaining := indexValue
	for payloadIndex, payloadType := range variant.Payload {
		relation := variant.PayloadRelation(payloadIndex)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		payloadValue := payloadValues[payloadIndex]
		matchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.match"))
		continueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.next"))
		var condValue C.LLVMValueRef
		edgeCount := one
		resolvedType := payloadType
		matchValue := payloadValue
		if relation == ast.EnumPayloadRelationChild {
			if optionalType, ok := payloadType.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(payloadValue, optionalType)
				if err != nil {
					return nil, err
				}
				matchValue, err = s.extractOptionalPayload(payloadValue, optionalType)
				if err != nil {
					return nil, err
				}
				resolvedType = optionalType.Value
				edgeCount = C.LLVMBuildSelect(s.builder, presentValue, one, zero, cStringFree(name+".tree.children.edge.count"))
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".tree.children.lt"))
		} else {
			var err error
			edgeCount, err = s.emitTreeStructuralSequenceCount(payloadValue, payloadType, name)
			if err != nil {
				return nil, err
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".tree.children.lt"))
		}
		C.LLVMBuildCondBr(s.builder, condValue, matchBB, continueBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchBB)
		if relation == ast.EnumPayloadRelationChild {
			// matchValue and resolvedType are already set above.
		} else {
			var value C.LLVMValueRef
			value, resolvedType, err = s.emitTreeStructuralSequenceItemValue(payloadValue, payloadType, remaining, name)
			if err != nil {
				return nil, err
			}
			matchValue = value
		}
		matchValue, err = s.coerceTreeChildrenItemValue(matchValue, resolvedType, itemType)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, matchValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)

		C.LLVMPositionBuilderAtEnd(s.builder, continueBB)
		remaining = C.LLVMBuildSub(s.builder, remaining, edgeCount, cStringFree(name+".tree.children.rem"))
	}
	C.LLVMBuildBr(s.builder, failBB)
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".tree.children.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}
func (s *functionState) emitTreeExactStructuralChildCount(nodeValue C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("children(...) exact member is missing tree family metadata")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	total := C.LLVMConstInt(usizeLLVMType, 0, 0)
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		switch relation {
		case ast.EnumPayloadRelationChild:
			fieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, fieldDecl.Name, name)
			if err != nil {
				return nil, err
			}
			childCount := C.LLVMConstInt(usizeLLVMType, 1, 0)
			if optionalType, ok := field.Type.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				zeroValue := C.LLVMConstInt(usizeLLVMType, 0, 0)
				childCount = C.LLVMBuildSelect(s.builder, presentValue, childCount, zeroValue, cStringFree(name+".count"))
			}
			total = C.LLVMBuildAdd(s.builder, total, childCount, cStringFree(name+".count"))
		case ast.EnumPayloadRelationChildren:
			fieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, fieldDecl.Name, name)
			if err != nil {
				return nil, err
			}
			seqCount, err := s.emitTreeStructuralSequenceCount(fieldValue, field.Type, name)
			if err != nil {
				return nil, err
			}
			total = C.LLVMBuildAdd(s.builder, total, seqCount, cStringFree(name+".count"))
		}
	}
	return total, nil
}
func (s *functionState) emitTreeExactStructuralChildValue(nodeValue C.LLVMValueRef, memberType semantic.Type, indexValue C.LLVMValueRef, itemType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("children(...) exact member is missing tree family metadata")
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	var incomingValues []C.LLVMValueRef
	var incomingBlocks []C.LLVMBasicBlockRef
	remaining := indexValue
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		fieldValue, _, err := s.emitTreeMemberFieldValueAtHandle(nodeValue, family, memberType, fieldDecl.Name, name)
		if err != nil {
			return nil, err
		}
		matchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".match"))
		continueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
		var condValue C.LLVMValueRef
		edgeCount := one
		resolvedType := field.Type
		matchValue := fieldValue
		if relation == ast.EnumPayloadRelationChild {
			if optionalType, ok := field.Type.(*semantic.OptionalType); ok {
				presentValue, err := s.extractOptionalPresent(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				matchValue, err = s.extractOptionalPayload(fieldValue, optionalType)
				if err != nil {
					return nil, err
				}
				resolvedType = optionalType.Value
				edgeCount = C.LLVMBuildSelect(s.builder, presentValue, one, zero, cStringFree(name+".edge.count"))
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".lt"))
		} else {
			edgeCount, err = s.emitTreeStructuralSequenceCount(fieldValue, field.Type, name)
			if err != nil {
				return nil, err
			}
			condValue = C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), remaining, edgeCount, cStringFree(name+".lt"))
		}
		C.LLVMBuildCondBr(s.builder, condValue, matchBB, continueBB)

		C.LLVMPositionBuilderAtEnd(s.builder, matchBB)
		if relation == ast.EnumPayloadRelationChild {
			// matchValue and resolvedType are already set above.
		} else {
			var value C.LLVMValueRef
			value, resolvedType, err = s.emitTreeStructuralSequenceItemValue(fieldValue, field.Type, remaining, name)
			if err != nil {
				return nil, err
			}
			matchValue = value
		}
		matchValue, err = s.coerceTreeChildrenItemValue(matchValue, resolvedType, itemType)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, matchValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)

		C.LLVMPositionBuilderAtEnd(s.builder, continueBB)
		remaining = C.LLVMBuildSub(s.builder, remaining, edgeCount, cStringFree(name+".rem"))
	}
	C.LLVMBuildBr(s.builder, failBB)
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}
func (s *functionState) emitTreeChildrenCount(sourceType semantic.Type, nodeValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if exactType, family, ok := treeChildrenExactSourceInfo(sourceType); ok {
		switch exact := exactType.(type) {
		case *semantic.TreeBlockType, *semantic.TreeStructType:
			return s.emitTreeExactStructuralChildCount(nodeValue, exact, name)
		case *semantic.TreeNodeType:
			tagValue, err := s.emitTreeHandleTagValue(nodeValue, name+".node")
			if err != nil {
				return nil, err
			}
			usizeType := s.g.result.NamedTypes["usize"]
			usizeLLVMType, err := s.g.lowerType(usizeType)
			if err != nil {
				return nil, err
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(semantic.TreeFamilyExactMembersInTagOrder(family))))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, member := range semantic.TreeFamilyExactMembersInTagOrder(family) {
				tag, ok := treeExactMemberTag(member)
				if !ok {
					continue
				}
				tagConst, err := s.enumTagConstant(tag)
				if err != nil {
					return nil, err
				}
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.case"))
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				countValue, err := s.emitTreeExactStructuralChildCount(nodeValue, member, name)
				if err != nil {
					return nil, err
				}
				incomingValues = append(incomingValues, countValue)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			phi := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(name+".node.count.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, nil
		}
	}
	categoryType, fixedVariant, ok := treeChildrenSourceInfo(sourceType)
	if !ok || categoryType == nil {
		return nil, fmt.Errorf("children(...) expects a tree node source, got %s", sourceType.String())
	}
	if fixedVariant != nil {
		return s.emitTreeVariantStructuralChildCount(nodeValue, categoryType, fixedVariant, name)
	}
	matchTagValue, err := s.extractTreeCategoryTagValue(nodeValue, categoryType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, failBB, C.unsigned(len(categoryType.Variants)))
	incomingValues := make([]C.LLVMValueRef, 0, len(categoryType.Variants))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(categoryType.Variants))
	for _, variant := range categoryType.Variants {
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, err
		}
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.count.case"))
		C.LLVMAddCase(switchInst, tagConst, caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		countValue, err := s.emitTreeVariantStructuralChildCount(nodeValue, categoryType, variant, name)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, countValue)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)
	}
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, usizeLLVMType, cStringFree(name+".tree.children.count.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}
func (s *functionState) emitTreeChildrenValue(sourceType semantic.Type, nodeValue C.LLVMValueRef, itemType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if exactType, family, ok := treeChildrenExactSourceInfo(sourceType); ok {
		switch exact := exactType.(type) {
		case *semantic.TreeBlockType, *semantic.TreeStructType:
			return s.emitTreeExactStructuralChildValue(nodeValue, exact, indexValue, itemType, name)
		case *semantic.TreeNodeType:
			tagValue, err := s.emitTreeHandleTagValue(nodeValue, name+".node")
			if err != nil {
				return nil, err
			}
			itemLLVMType, err := s.g.lowerType(itemType)
			if err != nil {
				return nil, err
			}
			resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.result"))
			failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.fail"))
			switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(semantic.TreeFamilyExactMembersInTagOrder(family))))
			var incomingValues []C.LLVMValueRef
			var incomingBlocks []C.LLVMBasicBlockRef
			for _, member := range semantic.TreeFamilyExactMembersInTagOrder(family) {
				tag, ok := treeExactMemberTag(member)
				if !ok {
					continue
				}
				tagConst, err := s.enumTagConstant(tag)
				if err != nil {
					return nil, err
				}
				caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".node.value.case"))
				C.LLVMAddCase(switchInst, tagConst, caseBB)
				C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
				value, err := s.emitTreeExactStructuralChildValue(nodeValue, member, indexValue, itemType, name)
				if err != nil {
					return nil, err
				}
				incomingValues = append(incomingValues, value)
				incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
				C.LLVMBuildBr(s.builder, resultBB)
			}
			if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
				return nil, err
			}
			C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
			phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".node.value.phi"))
			C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
			return phi, nil
		}
	}
	categoryType, fixedVariant, ok := treeChildrenSourceInfo(sourceType)
	if !ok || categoryType == nil {
		return nil, fmt.Errorf("children(...) expects a tree node source, got %s", sourceType.String())
	}
	if fixedVariant != nil {
		return s.emitTreeVariantStructuralChildValue(nodeValue, categoryType, fixedVariant, indexValue, itemType, name)
	}
	matchTagValue, err := s.extractTreeCategoryTagValue(nodeValue, categoryType)
	if err != nil {
		return nil, err
	}
	itemLLVMType, err := s.g.lowerType(itemType)
	if err != nil {
		return nil, err
	}
	resultBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.result"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, matchTagValue, failBB, C.unsigned(len(categoryType.Variants)))
	incomingValues := make([]C.LLVMValueRef, 0, len(categoryType.Variants))
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(categoryType.Variants))
	for _, variant := range categoryType.Variants {
		tagConst, err := s.enumTagConstant(variant.Tag)
		if err != nil {
			return nil, err
		}
		caseBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".tree.children.value.case"))
		C.LLVMAddCase(switchInst, tagConst, caseBB)
		C.LLVMPositionBuilderAtEnd(s.builder, caseBB)
		if !variant.HasStructuralPayloads() {
			C.LLVMBuildBr(s.builder, failBB)
			continue
		}
		value, err := s.emitTreeVariantStructuralChildValue(nodeValue, categoryType, variant, indexValue, itemType, name)
		if err != nil {
			return nil, err
		}
		incomingValues = append(incomingValues, value)
		incomingBlocks = append(incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, resultBB)
	}
	if err := s.emitTreeChildrenTrapBlock(failBB); err != nil {
		return nil, err
	}
	C.LLVMPositionBuilderAtEnd(s.builder, resultBB)
	phi := C.LLVMBuildPhi(s.builder, itemLLVMType, cStringFree(name+".tree.children.value.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}
func iterLoopItemTypeBackend(t semantic.Type) (semantic.Type, bool) {
	switch tt := t.(type) {
	case *semantic.ArrayType:
		if tt.SurfaceName == "str" || tt.SurfaceName == "string" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.DArrayType:
		return tt.Elem, true
	case *semantic.ViewType:
		return tt.Elem, true
	case *semantic.DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return nil, false
		}
		return tt.Elem, true
	case *semantic.StoreRowsViewType:
		return &semantic.StoreRowViewType{Store: tt.Store}, true
	case *semantic.GenericInstanceType:
		if itemType, ok := semantic.TreeChildrenItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.TreeAttributeSequenceItemType(tt); ok {
			return itemType, true
		}
		if itemType, ok := semantic.ChunksExactViewItemType(tt); ok {
			return itemType, true
		}
		return nil, false
	case *semantic.RefType:
		if tt.State != semantic.RefStateNonNull {
			return nil, false
		}
		return iterLoopItemTypeBackend(tt.Elem)
	default:
		return nil, false
	}
}
func isIterLoopRuntimeStringViewType(t semantic.Type) bool {
	st, ok := t.(*semantic.StructType)
	return ok && st != nil && st.Name == "StringView"
}
func (s *functionState) emitStoreRowsCarrierStorePtr(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (*semantic.StoreRowsViewType, C.LLVMValueRef, error) {
	switch tt := sourceType.(type) {
	case *semantic.StoreRowsViewType:
		carrierValue, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows")
		if err != nil {
			return nil, nil, err
		}
		storePtr := C.LLVMBuildExtractValue(s.builder, carrierValue, 0, cStringFree(sourceName+".rows.store"))
		return tt, storePtr, nil
	case *semantic.RefType:
		rowsType, ok := tt.Elem.(*semantic.StoreRowsViewType)
		if !ok || rowsType == nil {
			return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
		}
		if tt.State != semantic.RefStateNonNull {
			return nil, nil, fmt.Errorf("iterable loop requires a non-null reference source, got %s", sourceType.String())
		}
		carrierPtr, err := s.loadValue(sourceAlloca, sourceType, sourceName+".rows.ref")
		if err != nil {
			return nil, nil, err
		}
		rowsLLVMType, err := s.g.lowerType(rowsType)
		if err != nil {
			return nil, nil, err
		}
		storeFieldPtr := C.LLVMBuildStructGEP2(s.builder, rowsLLVMType, carrierPtr, 0, cStringFree(sourceName+".rows.store.ptr"))
		storePtr, err := s.loadValue(storeFieldPtr, &semantic.RefType{Elem: rowsType.Store, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}, sourceName+".rows.store")
		if err != nil {
			return nil, nil, err
		}
		return rowsType, storePtr, nil
	default:
		return nil, nil, fmt.Errorf("unsupported store rows carrier %s", sourceType.String())
	}
}
func (s *functionState) emitStoreRowsCount(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, sourceName string) (C.LLVMValueRef, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, err
	}
	if rowsType.Store == nil || len(rowsType.Store.StoreFieldOrder) == 0 {
		usizeType, err := s.g.lowerType(s.g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		return C.LLVMConstInt(usizeType, 0, 0), nil
	}
	firstField := rowsType.Store.StoreFieldOrder[0]
	columnPtr, darrayType, err := s.emitBuiltinStoreFieldDArrayPtr(storePtr, rowsType.Store, firstField)
	if err != nil {
		return nil, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(columnPtr, darrayType)
	if err != nil {
		return nil, err
	}
	return s.loadValue(countPtr, usizeType, sourceName+".rows.count")
}
func (s *functionState) emitStoreRowItemValue(sourceAlloca C.LLVMValueRef, sourceType semantic.Type, indexValue C.LLVMValueRef, sourceName string) (C.LLVMValueRef, semantic.Type, error) {
	rowsType, storePtr, err := s.emitStoreRowsCarrierStorePtr(sourceAlloca, sourceType, sourceName)
	if err != nil {
		return nil, nil, err
	}
	rowType := &semantic.StoreRowViewType{Store: rowsType.Store}
	rowLLVMType, err := s.g.lowerType(rowType)
	if err != nil {
		return nil, nil, err
	}
	rowValue := C.LLVMGetUndef(rowLLVMType)
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, storePtr, 0, cStringFree(sourceName+".row.store"))
	rowValue = C.LLVMBuildInsertValue(s.builder, rowValue, indexValue, 1, cStringFree(sourceName+".row.index"))
	return rowValue, rowType, nil
}
