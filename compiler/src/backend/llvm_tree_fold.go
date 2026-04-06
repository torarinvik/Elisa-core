//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type treeFoldRootKind int

const (
	treeFoldRootCategory treeFoldRootKind = iota
	treeFoldRootExact
	treeFoldRootFamily
)

type treeFoldRootInfo struct {
	kind     treeFoldRootKind
	family   *semantic.TreeType
	category *semantic.TreeCategoryType
	exact    semantic.Type
}

func (info treeFoldRootInfo) bindType() semantic.Type {
	switch info.kind {
	case treeFoldRootCategory:
		return info.category
	case treeFoldRootExact:
		return info.exact
	case treeFoldRootFamily:
		if info.family != nil {
			return info.family.NodeType
		}
	}
	return nil
}

type treeFoldCapture struct {
	name    string
	binding valueBinding
}

type treeFoldHelperInfo struct {
	name        string
	root        treeFoldRootInfo
	resultType  semantic.Type
	funcType    *semantic.FuncType
	fnValue     C.LLVMValueRef
	llvmFnType  C.LLVMTypeRef
	envStruct   C.LLVMTypeRef
	captures    []treeFoldCapture
	hasEnvParam bool
}

func resolveTreeVisitSourceTypeBackend(actual semantic.Type) (semantic.Type, *semantic.TreeType, bool) {
	switch tt := semantic.StripAggregateStateType(actual).(type) {
	case *semantic.TreeNodeType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeCategoryType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, nil, false
		}
		return tt, tt.Category.Family, tt.Category.Family != nil
	case *semantic.TreeBlockType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	case *semantic.TreeStructType:
		return tt, tt.Family, tt != nil && tt.Family != nil
	default:
		return nil, nil, false
	}
}

func (s *functionState) resolveTreeFoldRootInfo(actualType semantic.Type, rootExpr ast.TypeExpr) (treeFoldRootInfo, error) {
	sourceMember, sourceFamily, ok := resolveTreeVisitSourceTypeBackend(actualType)
	if !ok || sourceFamily == nil {
		return treeFoldRootInfo{}, fmt.Errorf("fold expression lowering expects a tree node source, got %s", actualType.String())
	}
	if rootExpr == nil {
		if categoryType, _, ok := resolveMatchableTreeCategoryTypeBackend(actualType); ok && categoryType != nil {
			return treeFoldRootInfo{kind: treeFoldRootCategory, family: categoryType.Family, category: categoryType}, nil
		}
		switch tt := semantic.StripAggregateStateType(sourceMember).(type) {
		case *semantic.TreeBlockType:
			return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
		case *semantic.TreeStructType:
			return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
		default:
			return treeFoldRootInfo{}, fmt.Errorf("fold lowering requires an explicit Family.Node root for %s", actualType.String())
		}
	}
	rootType, err := s.resolveTypeExpr(rootExpr)
	if err != nil {
		return treeFoldRootInfo{}, err
	}
	switch tt := semantic.StripAggregateStateType(rootType).(type) {
	case *semantic.TreeNodeType:
		if tt == nil || tt.Family == nil {
			break
		}
		if sourceFamily != tt.Family {
			return treeFoldRootInfo{}, fmt.Errorf("fold root %s does not match source family %s", tt.String(), sourceFamily.Name)
		}
		return treeFoldRootInfo{kind: treeFoldRootFamily, family: tt.Family}, nil
	case *semantic.TreeCategoryType:
		categoryType, _, ok := resolveMatchableTreeCategoryTypeBackend(actualType)
		if !ok || categoryType != tt {
			return treeFoldRootInfo{}, fmt.Errorf("fold root %s requires a %s source, got %s", tt.String(), tt.String(), actualType.String())
		}
		return treeFoldRootInfo{kind: treeFoldRootCategory, family: tt.Family, category: tt}, nil
	case *semantic.TreeBlockType:
		if !semantic.SameType(sourceMember, tt) {
			return treeFoldRootInfo{}, fmt.Errorf("fold root %s requires a %s source, got %s", tt.String(), tt.String(), actualType.String())
		}
		return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
	case *semantic.TreeStructType:
		if !semantic.SameType(sourceMember, tt) {
			return treeFoldRootInfo{}, fmt.Errorf("fold root %s requires a %s source, got %s", tt.String(), tt.String(), actualType.String())
		}
		return treeFoldRootInfo{kind: treeFoldRootExact, family: tt.Family, exact: tt}, nil
	}
	return treeFoldRootInfo{}, fmt.Errorf("fold root expects a tree category, tree member, or Family.Node type, got %s", rootType.String())
}

func (s *functionState) collectTreeFoldCaptures(expr *ast.FoldExpr) []treeFoldCapture {
	if s == nil || s.g == nil || s.g.result == nil || expr == nil {
		return nil
	}
	info := s.g.result.Fold[expr]
	if info == nil || len(info.Captures) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]treeFoldCapture, 0, len(info.Captures))
	for _, name := range info.Captures {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		binding, ok := s.lookupBinding(name)
		if !ok || binding.ptr == nil || binding.typ == nil {
			continue
		}
		out = append(out, treeFoldCapture{name: name, binding: binding})
	}
	return out
}

func (s *functionState) buildTreeFoldEnv(captures []treeFoldCapture, name string) (C.LLVMValueRef, C.LLVMTypeRef, error) {
	if len(captures) == 0 {
		return nil, nil, nil
	}
	ptrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	fields := make([]C.LLVMTypeRef, len(captures))
	for i := range captures {
		fields[i] = ptrType
	}
	envType := C.LLVMStructTypeInContext(s.g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	envAlloca := C.LLVMBuildAlloca(s.builder, envType, cStringFree(name+".env"))
	for i, capture := range captures {
		fieldPtr := C.LLVMBuildStructGEP2(s.builder, envType, envAlloca, C.unsigned(i), cStringFree(name+".env.field"))
		C.LLVMBuildStore(s.builder, capture.binding.ptr, fieldPtr)
	}
	return envAlloca, envType, nil
}

func (s *functionState) newTreeFoldHelper(expr *ast.FoldExpr, root treeFoldRootInfo, resultType semantic.Type, captures []treeFoldCapture, envStruct C.LLVMTypeRef) (*treeFoldHelperInfo, error) {
	rootBindType := root.bindType()
	if rootBindType == nil {
		return nil, fmt.Errorf("missing fold root bind type")
	}
	name := s.g.nextSyntheticName("tree_fold_")
	params := []semantic.Type{rootBindType}
	hasEnv := len(captures) != 0
	if hasEnv {
		params = append(params, &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true})
	}
	funcType := &semantic.FuncType{Name: name, Params: params, Return: resultType}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	fnValue := C.LLVMAddFunction(s.g.module, nameC, llvmFnType)
	C.LLVMSetLinkage(fnValue, C.LLVMPrivateLinkage)
	return &treeFoldHelperInfo{
		name:        name,
		root:        root,
		resultType:  resultType,
		funcType:    funcType,
		fnValue:     fnValue,
		llvmFnType:  llvmFnType,
		envStruct:   envStruct,
		captures:    captures,
		hasEnvParam: hasEnv,
	}, nil
}

func (s *functionState) emitTreeFoldHelperCall(helper *treeFoldHelperInfo, nodeValue C.LLVMValueRef, envValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	if helper == nil {
		return nil, fmt.Errorf("missing fold helper metadata")
	}
	args := make([]C.LLVMValueRef, 0, len(helper.funcType.Params))
	args = append(args, nodeValue)
	if helper.hasEnvParam {
		args = append(args, envValue)
	}
	if retUnion, ok := nonVoidErrorUnion(helper.resultType); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, name+".result")
		if err != nil {
			return nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		call := s.buildCall(helper.llvmFnType, helper.fnValue, callArgs, name+".call")
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
	return s.buildCall(helper.llvmFnType, helper.fnValue, args, callName), nil
}

func (s *functionState) treeVisitRelevantCategoryExactArms(categoryType *semantic.TreeCategoryType, arms []ast.VisitArm) ([]treeVisitExactArm, bool, error) {
	if categoryType == nil {
		return nil, false, fmt.Errorf("missing category for fold lowering")
	}
	relevant := make([]treeVisitExactArm, 0, len(categoryType.Variants))
	exhaustive := false
	for _, member := range treeCategoryMembersInTagOrder(categoryType) {
		memberName := treeExactMemberSurfaceName(member)
		arm, ok, wildcard := exactTreeVisitArm(memberName, arms)
		if !ok {
			continue
		}
		relevant = append(relevant, treeVisitExactArm{arm: arm, member: member, wildcard: wildcard})
		if wildcard {
			exhaustive = true
		}
	}
	if !exhaustive {
		exhaustive = len(relevant) == len(categoryType.Variants)
	}
	return relevant, exhaustive, nil
}

func (s *functionState) emitTreeFoldExactStructuralChildNodeValue(nodeValue C.LLVMValueRef, memberType semantic.Type, indexValue C.LLVMValueRef, rootType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("fold child source %s is missing tree family metadata", treeExactMemberSurfaceName(memberType))
	}
	stateValue := s.emitTreeHandleStateValue(nodeValue, name+".state")
	rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, name+".index")
	if err != nil {
		return nil, err
	}
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, name)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	rootLLVMType, err := s.g.lowerType(rootType)
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
		fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, fieldDecl.Name, rowIndex, name)
		if err != nil {
			return nil, err
		}
		matchBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".match"))
		continueBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".next"))
		var condValue C.LLVMValueRef
		edgeCount := one
		matchValue := fieldValue
		resolvedType := field.Type
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
		if relation == ast.EnumPayloadRelationChildren {
			var valueType semantic.Type
			matchValue, valueType, err = s.emitTreeStructuralSequenceItemValue(fieldValue, field.Type, remaining, name)
			if err != nil {
				return nil, err
			}
			resolvedType = valueType
		}
		if resolvedType == nil || !semantic.AssignableTo(rootType, resolvedType) {
			return nil, fmt.Errorf("fold child %s is not assignable to root %s", resolvedType.String(), rootType.String())
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
	phi := C.LLVMBuildPhi(s.builder, rootLLVMType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, nil
}

func (s *functionState) emitTreeFoldChildResultsView(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	countValue, err := s.emitTreeExactStructuralChildCount(nodeValue, memberType, name+".count")
	if err != nil {
		return nil, err
	}
	resultLLVMType, err := s.g.lowerType(helper.resultType)
	if err != nil {
		return nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	elemSize, err := s.sizeOfType(helper.resultType)
	if err != nil {
		return nil, err
	}
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	one := C.LLVMConstInt(usizeLLVMType, 1, 0)
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zero, cStringFree(name+".zero"))
	bufferCount := C.LLVMBuildSelect(s.builder, isZero, one, countValue, cStringFree(name+".buffer.count"))
	bufferPtr := C.LLVMBuildArrayAlloca(s.builder, resultLLVMType, bufferCount, cStringFree(name+".buffer"))
	indexAlloca, err := s.createEntryAlloca(name+".index", usizeType)
	if err != nil {
		return nil, err
	}
	C.LLVMBuildStore(s.builder, zero, indexAlloca)
	loopBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".loop"))
	bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".body"))
	endBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	C.LLVMBuildBr(s.builder, loopBB)

	C.LLVMPositionBuilderAtEnd(s.builder, loopBB)
	indexValue, err := s.loadValue(indexAlloca, usizeType, name+".index.load")
	if err != nil {
		return nil, err
	}
	condValue := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), indexValue, countValue, cStringFree(name+".cond"))
	C.LLVMBuildCondBr(s.builder, condValue, bodyBB, endBB)

	C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
	childNode, err := s.emitTreeFoldExactStructuralChildNodeValue(nodeValue, memberType, indexValue, helper.root.bindType(), name+".child")
	if err != nil {
		return nil, err
	}
	childResult, err := s.emitTreeFoldHelperCall(helper, childNode, envValue, name+".child.result")
	if err != nil {
		return nil, err
	}
	elemPtr := C.LLVMBuildGEP2(s.builder, resultLLVMType, bufferPtr, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".elem.ptr"))
	C.LLVMBuildStore(s.builder, childResult, elemPtr)
	nextValue := C.LLVMBuildAdd(s.builder, indexValue, one, cStringFree(name+".next"))
	C.LLVMBuildStore(s.builder, nextValue, indexAlloca)
	C.LLVMBuildBr(s.builder, loopBB)

	C.LLVMPositionBuilderAtEnd(s.builder, endBB)
	viewType := &semantic.DArrayViewType{Elem: helper.resultType, SurfaceName: "dview"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, bufferPtr, 0, cStringFree(name+".view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree(name+".view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree(name+".view.elem_size"))
	return viewValue, nil
}

func (s *functionState) emitTreeFoldChildResultAtIndex(childViewValue C.LLVMValueRef, resultType semantic.Type, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, error) {
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, childViewValue, 0, cStringFree(name+".data"))
	elemPtr := C.LLVMBuildGEP2(s.builder, resultLLVMType, viewData, llvmValueSlicePtr([]C.LLVMValueRef{indexValue}), 1, cStringFree(name+".ptr"))
	return C.LLVMBuildLoad2(s.builder, resultLLVMType, elemPtr, cStringFree(name+".value")), nil
}

func (s *functionState) emitTreeFoldChildResultsSubview(childViewValue C.LLVMValueRef, resultType semantic.Type, offsetValue C.LLVMValueRef, countValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	viewType := &semantic.DArrayViewType{Elem: resultType, SurfaceName: "dview"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, err
	}
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, childViewValue, 0, cStringFree(name+".data"))
	viewElemSize := C.LLVMBuildExtractValue(s.builder, childViewValue, 2, cStringFree(name+".elem_size"))
	subData := C.LLVMBuildGEP2(s.builder, resultLLVMType, viewData, llvmValueSlicePtr([]C.LLVMValueRef{offsetValue}), 1, cStringFree(name+".sub.data"))
	subView := C.LLVMGetUndef(viewLLVMType)
	subView = C.LLVMBuildInsertValue(s.builder, subView, subData, 0, cStringFree(name+".sub.view.data"))
	subView = C.LLVMBuildInsertValue(s.builder, subView, countValue, 1, cStringFree(name+".sub.view.len"))
	subView = C.LLVMBuildInsertValue(s.builder, subView, viewElemSize, 2, cStringFree(name+".sub.view.elem_size"))
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
	stateValue := s.emitTreeHandleStateValue(nodeValue, name+".state")
	rowIndex, err := s.emitTreeHandleIndexValue(nodeValue, name+".index")
	if err != nil {
		return err
	}
	tablePtr, err := s.emitTreeStateTablePtr(stateValue, family, memberType, name)
	if err != nil {
		return err
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
			fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, childBinding.Name, rowIndex, name+"."+childBinding.Name)
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
					boundType := &semantic.OptionalType{Value: helper.resultType}
					boundLLVMType, err := s.g.lowerType(boundType)
					if err != nil {
						return err
					}
					presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".some"))
					absentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".none"))
					contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+"."+childBinding.Name+".cont"))
					C.LLVMBuildCondBr(s.builder, presentValue, presentBB, absentBB)

					C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
					childResult, err := s.emitTreeFoldChildResultAtIndex(childViewValue, helper.resultType, offsetValue, name+"."+childBinding.Name)
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
					childResult, err := s.emitTreeFoldChildResultAtIndex(childViewValue, helper.resultType, offsetValue, name+"."+childBinding.Name)
					if err != nil {
						return err
					}
					if err := s.emitMoveBindLocal(bindName, helper.resultType, childResult); err != nil {
						return err
					}
				}
				boundFields[childBinding.Name] = true
			}
			offsetValue = C.LLVMBuildAdd(s.builder, offsetValue, childCount, cStringFree(name+"."+childBinding.Name+".offset.next"))
		case ast.EnumPayloadRelationChildren:
			fieldValue, _, err := s.emitTreeExactFieldValueAtIndex(tablePtr, memberType, childBinding.Name, rowIndex, name+"."+childBinding.Name)
			if err != nil {
				return err
			}
			countValue, err := s.emitTreeStructuralSequenceCount(fieldValue, childBinding.Type, name+"."+childBinding.Name+".count")
			if err != nil {
				return err
			}
			if wanted {
				subViewValue, subViewType, err := s.emitTreeFoldChildResultsSubview(childViewValue, helper.resultType, offsetValue, countValue, name+"."+childBinding.Name)
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
	childViewValue, err := s.emitTreeFoldChildResultsView(helper, envValue, nodeValue, memberType, "fold.arm")
	if err != nil {
		return nil, false, err
	}
	s.pushScope()
	if arm.BindName != "" && arm.BindName != "_" {
		if err := s.emitMoveBindLocal(arm.BindName, memberType, nodeValue); err != nil {
			s.popScope()
			return nil, false, err
		}
	}
	if arm.ChildResultsName != "" && arm.ChildResultsName != "_" {
		childViewType := &semantic.DArrayViewType{Elem: helper.resultType, SurfaceName: "dview"}
		if err := s.emitMoveBindLocal(arm.ChildResultsName, childViewType, childViewValue); err != nil {
			s.popScope()
			return nil, false, err
		}
	}
	if err := s.emitTreeFoldNamedChildBindingLocals(helper, nodeValue, memberType, childViewValue, arm, "fold.arm.named"); err != nil {
		s.popScope()
		return nil, false, err
	}
	armValue, reachable, err := s.emitMatchExprArmBody(arm.Body, helper.resultType)
	s.popScope()
	return armValue, reachable, err
}

func (s *functionState) emitTreeFoldExactDispatch(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, arms []ast.VisitArm) (C.LLVMValueRef, bool, error) {
	arm, ok, _ := exactTreeVisitArm(treeExactMemberSurfaceName(memberType), arms)
	if !ok {
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
	if len(relevant) == 0 {
		return nil, false, fmt.Errorf("fold over %s has no relevant arms", helper.root.bindType().String())
	}
	tagValue, err := s.emitTreeHandleTagValue(nodeValue, name+".tag")
	if err != nil {
		return nil, false, err
	}
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".end"))
	failBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, failBB, C.unsigned(len(relevant)))
	incomingValues := make([]C.LLVMValueRef, 0, len(relevant)+1)
	incomingBlocks := make([]C.LLVMBasicBlockRef, 0, len(relevant)+1)
	for _, armInfo := range relevant {
		if armInfo.member == nil {
			continue
		}
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".arm"))
		tag, ok := treeExactMemberTag(armInfo.member)
		if !ok {
			return nil, false, fmt.Errorf("missing exact tag for %s", treeExactMemberSurfaceName(armInfo.member))
		}
		tagConst, err := s.enumTagConstant(tag)
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
	if semantic.IsNeverType(helper.resultType) || exhaustive {
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
	if len(incomingValues) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil, true, nil
	}
	if len(incomingValues) == 1 || semantic.IsNeverType(helper.resultType) {
		return incomingValues[0], false, nil
	}
	llvmType, err := s.g.lowerType(helper.resultType)
	if err != nil {
		return nil, false, err
	}
	phi := C.LLVMBuildPhi(s.builder, llvmType, cStringFree(name+".phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(incomingValues), llvmBlockSlicePtr(incomingBlocks), C.unsigned(len(incomingValues)))
	return phi, false, nil
}

func (s *functionState) emitTreeFoldHelperBody(expr *ast.FoldExpr, helper *treeFoldHelperInfo, nodeParam C.LLVMValueRef, envParam C.LLVMValueRef) error {
	if helper.hasEnvParam && helper.envStruct != nil {
		for i, capture := range helper.captures {
			fieldPtr := C.LLVMBuildStructGEP2(s.builder, helper.envStruct, envParam, C.unsigned(i), cStringFree(helper.name+".env.field"))
			fieldValue := C.LLVMBuildLoad2(s.builder, C.LLVMPointerTypeInContext(s.g.context, 0), fieldPtr, cStringFree(helper.name+".env.value"))
			s.defineBinding(capture.name, valueBinding{ptr: fieldValue, typ: capture.binding.typ, mutable: capture.binding.mutable})
		}
	}

	var (
		resultValue C.LLVMValueRef
		terminated  bool
		err         error
	)
	switch helper.root.kind {
	case treeFoldRootFamily:
		var relevant []treeVisitExactArm
		var exhaustive bool
		relevant, exhaustive, err = s.treeVisitRelevantExactArms(helper.root.family, expr.Arms)
		if err == nil {
			resultValue, terminated, err = s.emitTreeFoldSwitchDispatch(helper, envParam, nodeParam, relevant, exhaustive, helper.name+".node")
		}
	case treeFoldRootCategory:
		var relevant []treeVisitExactArm
		var exhaustive bool
		relevant, exhaustive, err = s.treeVisitRelevantCategoryExactArms(helper.root.category, expr.Arms)
		if err == nil {
			resultValue, terminated, err = s.emitTreeFoldSwitchDispatch(helper, envParam, nodeParam, relevant, exhaustive, helper.name+".category")
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
	helper, err := s.newTreeFoldHelper(expr, root, resultType, captures, envStruct)
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
