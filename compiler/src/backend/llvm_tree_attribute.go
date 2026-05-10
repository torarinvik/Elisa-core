//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"sort"
	"unsafe"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

type treeAttributeHelperInfo struct {
	attr       *semantic.TreeAttribute
	root       treeFoldRootInfo
	name       string
	resultType semantic.Type
	funcType   *semantic.FuncType
	fnValue    C.LLVMValueRef
	llvmFnType C.LLVMTypeRef
	storeParam bool
	storeType  *semantic.TreeStoreType
	storeTypes []*semantic.TreeStoreType
	defined    bool
}

func treeAttributeRootInfo(receiver semantic.Type) (treeFoldRootInfo, error) {
	switch tt := semantic.StripAggregateStateType(receiver).(type) {
	case *semantic.TreeNodeType:
		if tt == nil || tt.Family == nil {
			return treeFoldRootInfo{}, fmt.Errorf("missing tree family node metadata")
		}
		return treeFoldRootInfo{kind: treeFoldRootFamily, family: tt.Family}, nil
	case *semantic.TreeCategoryType:
		if tt == nil || tt.Family == nil {
			return treeFoldRootInfo{}, fmt.Errorf("missing tree category metadata")
		}
		return treeFoldRootInfo{kind: treeFoldRootCategory, family: tt.Family, category: tt}, nil
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Category.Family == nil {
			return treeFoldRootInfo{}, fmt.Errorf("missing tree variant view metadata")
		}
		return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Category.Family, exact: tt}, nil
	case *semantic.TreeBlockType:
		if tt == nil || tt.Family == nil {
			return treeFoldRootInfo{}, fmt.Errorf("missing tree block metadata")
		}
		return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
	case *semantic.TreeStructType:
		if tt == nil || tt.Family == nil {
			return treeFoldRootInfo{}, fmt.Errorf("missing tree struct metadata")
		}
		return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
	default:
		return treeFoldRootInfo{}, fmt.Errorf("attribute helper requires a tree receiver, got %s", receiver.String())
	}
}

func (s *functionState) emitTreeAttributeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if s == nil || s.g == nil || s.g.result == nil || expr == nil {
		return nil, nil, false, nil
	}
	ref := s.g.result.AttributeFieldRefs[expr]
	if ref == nil || ref.Attribute == nil {
		return nil, nil, false, nil
	}
	if projectedType, ok := semantic.TreeAttributeSequenceInstance(s.exprType(expr)); ok {
		sourceValue, _, err := s.emitExpr(expr.Object, nil)
		if err != nil {
			return nil, nil, true, err
		}
		llvmType, err := s.g.lowerType(projectedType)
		if err != nil {
			return nil, nil, true, err
		}
		value := C.LLVMGetUndef(llvmType)
		value = C.LLVMBuildInsertValue(s.builder, value, sourceValue, 0, cStringFree("tree.attr.project.source"))
		return value, projectedType, true, nil
	}
	objValue, _, err := s.emitExpr(expr.Object, ref.Attribute.Receiver)
	if err != nil {
		return nil, nil, true, err
	}
	helper, err := s.ensureTreeAttributeHelper(ref.Attribute)
	if err != nil {
		return nil, nil, true, err
	}
	value, err := s.emitTreeAttributeHelperCall(helper, objValue, "tree.attr")
	if err != nil {
		return nil, nil, true, err
	}
	return value, ref.Attribute.ReturnType, true, nil
}

func treeAttributeFieldRefForExpr(result *semantic.Result, expr ast.Expr) *semantic.AttributeFieldRef {
	if result == nil || expr == nil {
		return nil
	}
	switch node := expr.(type) {
	case *ast.FieldExpr:
		return result.AttributeFieldRefs[node]
	case *ast.ParenExpr:
		return treeAttributeFieldRefForExpr(result, node.Inner)
	case *ast.CastExpr:
		return treeAttributeFieldRefForExpr(result, node.Operand)
	default:
		return nil
	}
}

func (s *functionState) ensureTreeAttributeHelper(attr *semantic.TreeAttribute) (*treeAttributeHelperInfo, error) {
	if s == nil || s.g == nil || attr == nil {
		return nil, fmt.Errorf("missing tree attribute helper metadata")
	}
	if helper := s.g.treeAttributeHelpers[attr]; helper != nil {
		return helper, nil
	}
	helper, err := s.newTreeAttributeHelper(attr)
	if err != nil {
		return nil, err
	}
	s.g.treeAttributeHelpers[attr] = helper
	if err := s.defineTreeAttributeHelper(helper); err != nil {
		return nil, err
	}
	helper.defined = true
	return helper, nil
}

func (s *functionState) newTreeAttributeHelper(attr *semantic.TreeAttribute) (*treeAttributeHelperInfo, error) {
	if s == nil || s.g == nil || attr == nil || attr.Receiver == nil || attr.ReturnType == nil {
		return nil, fmt.Errorf("missing tree attribute helper metadata")
	}
	root, err := treeAttributeRootInfo(attr.Receiver)
	if err != nil {
		return nil, err
	}
	name := s.g.nextSyntheticName("tree_attr_")
	params := []semantic.Type{attr.Receiver}
	storeTypes := s.g.categoryUnionTreeStoreTypesForAttribute(attr)
	storeParam := len(storeTypes) != 0
	var storeType *semantic.TreeStoreType
	if storeParam {
		storeType = storeTypes[0]
		for _, candidate := range storeTypes {
			params = append(params, candidate)
			if root.family != nil && candidate.Family == root.family {
				storeType = candidate
			}
		}
	}
	funcType := &semantic.FuncType{Name: name, Params: params, Return: attr.ReturnType}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	fnValue := C.LLVMAddFunction(s.g.module, nameC, llvmFnType)
	C.LLVMSetLinkage(fnValue, C.LLVMPrivateLinkage)
	return &treeAttributeHelperInfo{
		attr:       attr,
		root:       root,
		name:       name,
		resultType: attr.ReturnType,
		funcType:   funcType,
		fnValue:    fnValue,
		llvmFnType: llvmFnType,
		storeParam: storeParam,
		storeType:  storeType,
		storeTypes: storeTypes,
	}, nil
}

func (g *llvmGenerator) categoryUnionTreeStoreTypesForAttribute(attr *semantic.TreeAttribute) []*semantic.TreeStoreType {
	if g == nil || g.result == nil || attr == nil {
		return nil
	}
	storesByFamily := map[string]*semantic.TreeStoreType{}
	addFamily := func(family *semantic.TreeType) {
		if family == nil || family.Layout != semantic.TreeLayoutCategoryUnion || family.StoreType == nil {
			return
		}
		storesByFamily[family.Name] = family.StoreType
	}
	if root, err := treeAttributeRootInfo(attr.Receiver); err == nil {
		addFamily(root.family)
	}
	for _, attrs := range g.result.TreeAttributes {
		for _, candidate := range attrs {
			if candidate == nil || candidate.Name != attr.Name {
				continue
			}
			root, err := treeAttributeRootInfo(candidate.Receiver)
			if err != nil {
				continue
			}
			addFamily(root.family)
		}
	}
	names := make([]string, 0, len(storesByFamily))
	for name := range storesByFamily {
		names = append(names, name)
	}
	sort.Strings(names)
	stores := make([]*semantic.TreeStoreType, 0, len(names))
	for _, name := range names {
		stores = append(stores, storesByFamily[name])
	}
	return stores
}

func (s *functionState) defineTreeAttributeHelper(helper *treeAttributeHelperInfo) error {
	if s == nil || s.g == nil || helper == nil {
		return fmt.Errorf("missing tree attribute helper")
	}
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
	if helper.storeParam {
		for i, storeType := range helper.storeTypes {
			storeParam := C.LLVMGetParam(helper.fnValue, C.unsigned(paramOffset+1+i))
			state.bindImplicitTreeStoreValue(storeType, storeParam)
			if storeType == helper.storeType {
				state.treeAllocOwner = treeAllocOwnerBinding{storeValue: storeParam, storeType: storeType}
			}
		}
	}
	return state.emitTreeAttributeHelperBody(helper, nodeParam)
}

func (s *functionState) emitTreeAttributeHelperCall(helper *treeAttributeHelperInfo, nodeValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if helper == nil {
		return nil, fmt.Errorf("missing tree attribute helper metadata")
	}
	callArgs := []C.LLVMValueRef{nodeValue}
	if helper.storeParam {
		for _, storeType := range helper.storeTypes {
			if storeType == nil || storeType.Family == nil {
				return nil, fmt.Errorf("missing tree attribute store metadata")
			}
			owner, ok := s.lookupTreeAllocOwnerForFamily(storeType.Family)
			if !ok {
				caller := "<helper>"
				if s.decl != nil {
					caller = s.decl.Name
				}
				return nil, fmt.Errorf("category_union tree attribute %s for %s requires an explicit tree store context in %s", helper.attr.Name, storeType.Family.Name, caller)
			}
			storeValue, _, err := s.ensureTreeOwnerStoreValue(owner, storeType.Family)
			if err != nil {
				return nil, err
			}
			callArgs = append(callArgs, storeValue)
		}
	}
	if retUnion, ok := nonVoidErrorUnion(helper.resultType); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, name+".result")
		if err != nil {
			return nil, err
		}
		errorCallArgs := append([]C.LLVMValueRef{resultSlot}, callArgs...)
		call := s.buildCall(helper.llvmFnType, helper.fnValue, errorCallArgs, name+".call")
		payload, err := s.loadValue(resultSlot, retUnion.Value, name+".payload")
		if err != nil {
			return nil, err
		}
		return s.buildErrorUnionValue(retUnion, call, payload)
	}
	callName := name + ".call"
	if isVoidType(helper.resultType) {
		callName = ""
	}
	return s.buildCall(helper.llvmFnType, helper.fnValue, callArgs, callName), nil
}

func (s *functionState) emitTreeAttributeHelperBody(helper *treeAttributeHelperInfo, nodeParam C.LLVMValueRef) error {
	if s == nil || helper == nil || helper.attr == nil || helper.attr.Decl == nil {
		return fmt.Errorf("missing tree attribute helper body metadata")
	}
	var (
		resultValue C.LLVMValueRef
		terminated  bool
		err         error
	)
	switch helper.root.kind {
	case treeFoldRootFamily:
		resultValue, terminated, err = s.emitTreeAttributeSwitchDispatch(helper, nodeParam, semantic.TreeFamilyExactMembersInTagOrder(helper.root.family), helper.name+".family")
	case treeFoldRootCategory:
		resultValue, terminated, err = s.emitTreeAttributeSwitchDispatch(helper, nodeParam, treeCategoryMembersInTagOrder(helper.root.category), helper.name+".category")
	case treeFoldRootExact:
		resultValue, terminated, err = s.emitTreeAttributeExactDispatch(helper, nodeParam, helper.root.exact)
	default:
		err = fmt.Errorf("unsupported tree attribute root kind")
	}
	if err != nil {
		return err
	}
	if terminated || s.currentBlockTerminated() {
		return nil
	}
	return s.emitFunctionReturn(resultValue, helper.resultType)
}

func (s *functionState) emitTreeAttributeExactDispatch(helper *treeAttributeHelperInfo, nodeValue C.LLVMValueRef, memberType semantic.Type) (C.LLVMValueRef, bool, error) {
	if helper == nil {
		return nil, false, fmt.Errorf("missing tree attribute helper metadata")
	}
	memberName := treeExactMemberSurfaceName(memberType)
	memberArms := exactTreeVisitArms(memberName, helper.attr.Decl.Arms)
	if len(memberArms) == 0 {
		return nil, false, fmt.Errorf("attribute %s has no relevant arms for %s", helper.attr.Name, memberName)
	}
	return s.emitTreeAttributeArmSequence(helper, nodeValue, memberType, memberArms, visitArmsCoverExactMember(memberName, helper.attr.Decl.Arms), helper.name+".exact")
}

func (s *functionState) emitTreeAttributeSwitchDispatch(helper *treeAttributeHelperInfo, nodeValue C.LLVMValueRef, members []semantic.Type, name string) (C.LLVMValueRef, bool, error) {
	if helper == nil {
		return nil, false, fmt.Errorf("missing tree attribute helper metadata")
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
	exhaustive := true
	for _, member := range members {
		if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), helper.attr.Decl.Arms) {
			exhaustive = false
		}
	}
	for _, member := range members {
		memberName := treeExactMemberSurfaceName(member)
		memberArms := exactTreeVisitArms(memberName, helper.attr.Decl.Arms)
		if len(memberArms) == 0 {
			continue
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm"))
		tagConst, err := s.treeExactMemberTagConstant(member)
		if err != nil {
			return nil, false, err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		armValue, terminated, err := s.emitTreeAttributeArmSequence(helper, nodeValue, member, memberArms, visitArmsCoverExactMember(memberName, helper.attr.Decl.Arms), name+".exact")
		if err != nil {
			return nil, false, err
		}
		if !terminated && !s.currentBlockTerminated() {
			inBlock := C.LLVMGetInsertBlock(s.builder)
			incomingValues = append(incomingValues, armValue)
			incomingBlocks = append(incomingBlocks, inBlock)
			C.LLVMBuildBr(s.builder, mergeBB)
		}
	}
	C.LLVMPositionBuilderAtEnd(s.builder, failBB)
	if semantic.IsNeverType(helper.resultType) || exhaustive {
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

func (s *functionState) emitTreeAttributeArmSequence(helper *treeAttributeHelperInfo, nodeValue C.LLVMValueRef, memberType semantic.Type, arms []ast.VisitArm, exhaustive bool, name string) (C.LLVMValueRef, bool, error) {
	if helper == nil {
		return nil, false, fmt.Errorf("missing tree attribute helper metadata")
	}
	if len(arms) == 0 {
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
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	incomingValues := make([]C.LLVMValueRef, 0, len(arms)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(arms)+1)
	armNodeValue := nodeValue
	if helper.root.kind == treeFoldRootFamily && helper.root.family != nil {
		var err error
		armNodeValue, err = s.emitTreeRootDispatchMemberValue(nodeValue, helper.root.family, memberType, name+".member")
		if err != nil {
			return nil, false, err
		}
	}
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
			if err := s.emitMoveBindLocal(arm.BindName, memberType, armNodeValue); err != nil {
				s.popScope()
				return nil, false, err
			}
		}
		if err := s.emitTreeAttributeImplicitChildrenLocal(helper.attr.Receiver, armNodeValue, memberType, name+".children"); err != nil {
			s.popScope()
			return nil, false, err
		}
		if err := s.emitTreeAttributeNamedChildBindingLocals(armNodeValue, memberType, arm, name+".named"); err != nil {
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
		armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, helper.resultType)
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
	if semantic.IsNeverType(helper.resultType) || exhaustive {
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

func (s *functionState) emitTreeAttributeNamedChildBindingLocals(nodeValue C.LLVMValueRef, memberType semantic.Type, arm ast.VisitArm, name string) error {
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
		return fmt.Errorf("attribute child binding source %s is missing tree family metadata", treeExactMemberSurfaceName(memberType))
	}
	bound := map[string]bool{}
	for _, childBinding := range semantic.TreeStructuralChildBindings(memberType) {
		bindName, wanted := requested[childBinding.Name]
		if !wanted {
			continue
		}
		surfaceValue, surfaceType, err := s.emitTreeMemberSurfaceFieldValueAtHandle(nodeValue, family, memberType, childBinding.Name, name+"."+childBinding.Name+".surface")
		if err != nil {
			return err
		}
		if err := s.emitMoveBindLocal(bindName, surfaceType, surfaceValue); err != nil {
			return err
		}
		bound[childBinding.Name] = true
	}
	for fieldName := range requested {
		if !bound[fieldName] {
			return fmt.Errorf("attribute arm child binding %q was not resolved for %s", fieldName, treeExactMemberSurfaceName(memberType))
		}
	}
	return nil
}

func (s *functionState) emitTreeAttributeImplicitChildrenLocal(receiver semantic.Type, nodeValue C.LLVMValueRef, memberType semantic.Type, name string) error {
	if s == nil || s.g == nil || receiver == nil || memberType == nil {
		return nil
	}
	bindings := semantic.TreeStructuralChildBindings(memberType)
	if len(bindings) == 0 {
		return nil
	}
	for _, binding := range bindings {
		if binding.Type == nil || !semantic.AssignableTo(receiver, binding.Type) {
			return nil
		}
	}
	base, ok := s.g.result.NamedTypes["TreeChildren"].(*semantic.StructType)
	if !ok || base == nil {
		return fmt.Errorf("missing runtime struct TreeChildren")
	}
	childrenType := &semantic.GenericInstanceType{Name: "TreeChildren", Base: base, Args: []semantic.Type{memberType, receiver}}
	childrenLLVMType, err := s.g.lowerType(childrenType)
	if err != nil {
		return err
	}
	childrenValue := C.LLVMGetUndef(childrenLLVMType)
	childrenValue = C.LLVMBuildInsertValue(s.builder, childrenValue, nodeValue, 0, cStringFree(name+".node.insert"))
	return s.emitMoveBindLocal("children", childrenType, childrenValue)
}
