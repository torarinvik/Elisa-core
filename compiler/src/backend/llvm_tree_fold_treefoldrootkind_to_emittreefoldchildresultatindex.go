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
	"unsafe"
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

const (
	treeRewriteOwnerArenaCaptureName = "__elisa_core_rewrite_owner_arena"
	treeRewriteOwnerStoreCaptureName = "__elisa_core_rewrite_owner_store"
)

type treeFoldHelperInfo struct {
	name             string
	root             treeFoldRootInfo
	resultType       semantic.Type
	funcType         *semantic.FuncType
	fnValue          C.LLVMValueRef
	llvmFnType       C.LLVMTypeRef
	envStruct        C.LLVMTypeRef
	captures         []treeFoldCapture
	hasEnvParam      bool
	rewrite          bool
	implicitDefault  bool
	rewriteOwnerPerm bool
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
	seen := map[string]bool{}
	out := make([]treeFoldCapture, 0, 4)
	if info := s.g.result.Fold[expr]; info != nil {
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
	}
	if expr.Keyword == "rewrite" {
		if s.treeAllocOwner.storeValue != nil && s.treeAllocOwner.storeType != nil && !seen[treeRewriteOwnerStoreCaptureName] {
			capture, err := s.spillTreeFoldCaptureValue(treeRewriteOwnerStoreCaptureName, s.treeAllocOwner.storeValue, s.treeAllocOwner.storeType)
			if err == nil {
				out = append(out, treeFoldCapture{name: treeRewriteOwnerStoreCaptureName, binding: capture})
				seen[treeRewriteOwnerStoreCaptureName] = true
			}
		} else if s.treeAllocOwner.arenaRef != nil && !seen[treeRewriteOwnerArenaCaptureName] {
			arenaRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["Arena"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
			capture, err := s.spillTreeFoldCaptureValue(treeRewriteOwnerArenaCaptureName, s.treeAllocOwner.arenaRef, arenaRefType)
			if err == nil {
				out = append(out, treeFoldCapture{name: treeRewriteOwnerArenaCaptureName, binding: capture})
				seen[treeRewriteOwnerArenaCaptureName] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
func (s *functionState) spillTreeFoldCaptureValue(name string, value C.LLVMValueRef, typ semantic.Type) (valueBinding, error) {
	if s == nil || value == nil || typ == nil {
		return valueBinding{}, fmt.Errorf("missing fold capture value")
	}
	llvmType, err := s.g.lowerType(typ)
	if err != nil {
		return valueBinding{}, err
	}
	alloca := C.LLVMBuildAlloca(s.builder, llvmType, cStringFree(name))
	s.g.applyTypeAlignment(alloca, typ)
	C.LLVMBuildStore(s.builder, value, alloca)
	return valueBinding{ptr: alloca, typ: typ, mutable: false}, nil
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
func (s *functionState) newTreeFoldHelper(expr *ast.FoldExpr, root treeFoldRootInfo, resultType semantic.Type, captures []treeFoldCapture, envStruct C.LLVMTypeRef, rewrite bool) (*treeFoldHelperInfo, error) {
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
		name:             name,
		root:             root,
		resultType:       resultType,
		funcType:         funcType,
		fnValue:          fnValue,
		llvmFnType:       llvmFnType,
		envStruct:        envStruct,
		captures:         captures,
		hasEnvParam:      hasEnv,
		rewrite:          rewrite,
		implicitDefault:  rewrite && expr != nil && expr.RewriteDefault,
		rewriteOwnerPerm: rewrite && s.treeAllocOwner.isPerm,
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
func (helper *treeFoldHelperInfo) childResultsElemType() semantic.Type {
	if helper == nil {
		return nil
	}
	if helper.rewrite {
		return helper.root.bindType()
	}
	return helper.resultType
}
func (helper *treeFoldHelperInfo) armResultType(memberType semantic.Type, arm ast.VisitArm) semantic.Type {
	if helper == nil {
		return nil
	}
	if !helper.rewrite {
		return helper.resultType
	}
	if arm.Wildcard {
		return helper.root.bindType()
	}
	if resultType := semantic.TreeRewriteResultTypeForValue(memberType); resultType != nil {
		return resultType
	}
	return helper.resultType
}
func (helper *treeFoldHelperInfo) hasImplicitRewriteDefault() bool {
	return helper != nil && helper.rewrite && helper.implicitDefault
}
func (helper *treeFoldHelperInfo) exactMembersInTagOrder() []semantic.Type {
	if helper == nil {
		return nil
	}
	switch helper.root.kind {
	case treeFoldRootFamily:
		return semantic.TreeFamilyExactMembersInTagOrder(helper.root.family)
	case treeFoldRootCategory:
		return treeCategoryMembersInTagOrder(helper.root.category)
	case treeFoldRootExact:
		if helper.root.exact != nil {
			return []semantic.Type{helper.root.exact}
		}
	}
	return nil
}
func (s *functionState) emitTreeFoldImplicitRewriteDefault(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, memberType semantic.Type, name string) (C.LLVMValueRef, error) {
	if helper == nil || !helper.hasImplicitRewriteDefault() {
		return nil, fmt.Errorf("missing implicit rewrite default")
	}
	childViewValue, err := s.emitTreeFoldChildResultsView(helper, envValue, nodeValue, memberType, name+".children")
	if err != nil {
		return nil, err
	}
	value, _, err := s.emitTreeRewriteDefaultValue(&treeRewriteDefaultContext{memberType: memberType, nodeValue: nodeValue, childViewValue: childViewValue}, semantic.TreeRewriteResultTypeForValue(memberType))
	return value, err
}
func (s *functionState) emitTreeFoldImplicitRewriteDefaultSwitch(helper *treeFoldHelperInfo, envValue C.LLVMValueRef, nodeValue C.LLVMValueRef, tagValue C.LLVMValueRef, covered map[string]bool, mergeBB C.LLVMBasicBlockRef, incomingValues *[]C.LLVMValueRef, incomingBlocks *[]C.LLVMBasicBlockRef, name string) error {
	if helper == nil || !helper.hasImplicitRewriteDefault() {
		return fmt.Errorf("missing implicit rewrite default switch")
	}
	missing := make([]semantic.Type, 0)
	for _, member := range helper.exactMembersInTagOrder() {
		memberName := treeExactMemberSurfaceName(member)
		if covered[memberName] {
			continue
		}
		missing = append(missing, member)
	}
	if len(missing) == 0 {
		C.LLVMBuildUnreachable(s.builder)
		return nil
	}
	missingFailBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".fail.unmatched"))
	switchInst := C.LLVMBuildSwitch(s.builder, tagValue, missingFailBB, C.unsigned(len(missing)))
	for _, member := range missing {
		bodyBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".default.arm"))
		tag, ok := treeExactMemberTag(member)
		if !ok {
			return fmt.Errorf("missing exact tag for %s", treeExactMemberSurfaceName(member))
		}
		tagConst, err := s.enumTagConstant(tag)
		if err != nil {
			return err
		}
		C.LLVMAddCase(switchInst, tagConst, bodyBB)
		C.LLVMPositionBuilderAtEnd(s.builder, bodyBB)
		value, err := s.emitTreeFoldImplicitRewriteDefault(helper, envValue, nodeValue, member, name+".default")
		if err != nil {
			return err
		}
		*incomingValues = append(*incomingValues, value)
		*incomingBlocks = append(*incomingBlocks, C.LLVMGetInsertBlock(s.builder))
		C.LLVMBuildBr(s.builder, mergeBB)
	}
	C.LLVMPositionBuilderAtEnd(s.builder, missingFailBB)
	C.LLVMBuildUnreachable(s.builder)
	return nil
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
		if wildcard && arm.Guard == nil {
			exhaustive = true
		}
	}
	if !exhaustive {
		exhaustive = true
		for _, member := range treeCategoryMembersInTagOrder(categoryType) {
			if !visitArmsCoverExactMember(treeExactMemberSurfaceName(member), arms) {
				exhaustive = false
				break
			}
		}
	}
	return relevant, exhaustive, nil
}
func (s *functionState) emitTreeFoldExactStructuralChildNodeValue(nodeValue C.LLVMValueRef, memberType semantic.Type, indexValue C.LLVMValueRef, rootType semantic.Type, name string) (C.LLVMValueRef, error) {
	family := treeExactMemberFamily(memberType)
	if family == nil {
		return nil, fmt.Errorf("fold child source %s is missing tree family metadata", treeExactMemberSurfaceName(memberType))
	}
	access, err := s.emitTreeExactTableAccessFromHandle(nodeValue, family, memberType, name)
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
	rowValue, _, err := s.emitTreeExactRowValueAtIndex(access.tablePtr, memberType, access.rowIndex, name)
	if err != nil {
		return nil, err
	}
	for _, fieldDecl := range treeExactFieldDecls(memberType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, fmt.Errorf("missing exact tree field %s.%s", treeExactMemberSurfaceName(memberType), fieldDecl.Name)
		}
		relation := semantic.TreeFieldStructuralRelation(family, field.Type)
		if relation != ast.EnumPayloadRelationChild && relation != ast.EnumPayloadRelationChildren {
			continue
		}
		fieldValue, _, err := s.emitTreeExactFieldValueFromRow(memberType, rowValue, fieldDecl.Name, name)
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
