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
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) materializeTreeOwnerDArrayFromView(viewValue C.LLVMValueRef, viewType *semantic.DArrayViewType, resultType *semantic.DArrayType, owner treeAllocOwnerBinding, name string) (C.LLVMValueRef, error) {
	if viewType == nil || resultType == nil {
		return nil, fmt.Errorf("missing dview materialization metadata")
	}
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, err
	}
	zeroResult, err := s.zeroValue(resultType)
	if err != nil {
		return nil, err
	}
	viewData := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree(name+".src.data"))
	viewLen := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree(name+".src.len"))
	viewElemSize := C.LLVMBuildExtractValue(s.builder, viewValue, 2, cStringFree(name+".src.elem_size"))
	byteCount := C.LLVMBuildMul(s.builder, viewLen, viewElemSize, cStringFree(name+".bytes"))
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, err
	}
	zeroBytes := C.LLVMConstInt(usizeLLVMType, 0, 0)
	zeroCond := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), byteCount, zeroBytes, cStringFree(name+".bytes.zero"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".merge"))
	C.LLVMBuildCondBr(s.builder, zeroCond, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocPtr, err := s.emitTreeOwnerAllocBytes(owner, byteCount, name)
	if err != nil {
		return nil, err
	}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, err
	}
	memcpyCall := s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{allocPtr, viewData, byteCount}, name+".memcpy")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(1), "noalias")
	s.addCallSiteEnumAttribute(memcpyCall, C.uint(2), "noalias")
	materialized := C.LLVMGetUndef(llvmResultType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, allocPtr, 0, cStringFree(name+".items"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 1, cStringFree(name+".count"))
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewLen, 2, cStringFree(name+".capacity"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, llvmResultType, cStringFree(name+".result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, nil
}
func (s *functionState) emitTupleExpr(expr *ast.TupleExpr) (C.LLVMValueRef, semantic.Type, error) {
	tupleType, ok := semantic.StripAggregateStateType(s.exprType(expr)).(*semantic.TupleType)
	if !ok || tupleType == nil {
		return nil, nil, fmt.Errorf("tuple expression requires a tuple type, got %s", s.exprType(expr))
	}
	llvmType, err := s.g.lowerType(tupleType)
	if err != nil {
		return nil, nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	for i, elem := range expr.Elems {
		if i >= len(tupleType.Fields) {
			break
		}
		elemValue, _, err := s.emitExpr(elem, tupleType.Fields[i].Type)
		if err != nil {
			return nil, nil, err
		}
		value = C.LLVMBuildInsertValue(s.builder, value, elemValue, C.unsigned(i), cStringFree("tuple.ins"))
	}
	return value, tupleType, nil
}
func (s *functionState) enumConstructorInfo(expr *ast.CallExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.enumConstructorInfoFromField(fieldExpr)
}
func (s *functionState) enumConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.EnumType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}
func (s *functionState) treeConstructorInfo(expr *ast.CallExpr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	return s.treeConstructorInfoFromField(fieldExpr)
}
func (s *functionState) treeExactMemberConstructorInfoFromField(expr *ast.FieldExpr) (semantic.Type, bool) {
	if expr == nil {
		return nil, false
	}
	base, ok := s.treeTypeForExpr(expr.Object)
	if !ok || base == nil {
		return nil, false
	}
	memberType, ok := base.Member(expr.Field)
	if !ok {
		return nil, false
	}
	switch semantic.StripAggregateStateType(memberType).(type) {
	case *semantic.TreeBlockType, *semantic.TreeStructType:
		return memberType, true
	default:
		return nil, false
	}
}
func (s *functionState) treeExactMemberConstructorCall(expr *ast.CallExpr) (semantic.Type, bool) {
	if expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	return s.treeExactMemberConstructorInfoFromField(fieldExpr)
}
func (s *functionState) treeConstructorInfoFromField(expr *ast.FieldExpr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	ownerName, variantName, ok := qualifiedFieldOwnerAndLeaf(expr)
	if !ok {
		return nil, nil, false
	}
	base, ok := s.g.result.NamedTypes[ownerName]
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*semantic.TreeCategoryType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok {
		return treeType, nil, true
	}
	return treeType, variant, true
}
func (s *functionState) treeAllocConstructorInfo(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		treeType, variant, ok := s.treeConstructorInfoFromField(n)
		if !ok {
			return nil, nil, nil, false
		}
		if variant != nil && len(variant.Payload) != 0 {
			return nil, nil, nil, false
		}
		return treeType, variant, nil, true
	case *ast.CallExpr:
		treeType, variant, ok := s.treeConstructorInfo(n)
		return treeType, variant, n, ok
	default:
		return nil, nil, nil, false
	}
}
func treeAllocArgs(callExpr *ast.CallExpr) []ast.Expr {
	if callExpr != nil {
		return callExpr.Args
	}
	return nil
}
func treeAllocArgNames(callExpr *ast.CallExpr) []string {
	if callExpr != nil {
		return callExpr.ArgNames
	}
	return nil
}
func qualifiedFieldOwnerAndLeaf(expr *ast.FieldExpr) (string, string, bool) {
	parts, ok := qualifiedFieldParts(expr)
	if !ok || len(parts) < 2 {
		return "", "", false
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], true
}
func qualifiedFieldParts(expr ast.Expr) ([]string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return []string{n.Name}, true
	case *ast.FieldExpr:
		parts, ok := qualifiedFieldParts(n.Object)
		if !ok || n.Field == "" {
			return nil, false
		}
		return append(parts, n.Field), true
	case *ast.ParenExpr:
		return qualifiedFieldParts(n.Inner)
	default:
		return nil, false
	}
}
func resolveMatchableTreeCategoryTypeBackend(actual semantic.Type) (*semantic.TreeCategoryType, *semantic.TreeVariantViewType, bool) {
	actual = semantic.StripAggregateStateType(actual)
	switch tt := actual.(type) {
	case *semantic.TreeCategoryType:
		return tt, nil, true
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, nil, false
		}
		return tt.Category, tt, true
	default:
		return nil, nil, false
	}
}
func (s *functionState) treeIsTargetPattern(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, *ast.MatchVariantPattern, bool) {
	if paren, ok := expr.(*ast.ParenExpr); ok && paren != nil {
		return s.treeIsTargetPattern(paren.Inner)
	}
	if alias, ok := expr.(*ast.IsAliasExpr); ok && alias != nil {
		return s.treeIsTargetPattern(alias.Target)
	}
	if testExpr, ok := expr.(*ast.VariantTestExpr); ok {
		if testExpr == nil || testExpr.Pattern == nil {
			return nil, nil, nil, false
		}
		base, ok := s.g.result.NamedTypes[testExpr.Pattern.EnumName]
		if !ok {
			return nil, nil, nil, false
		}
		treeType, ok := base.(*semantic.TreeCategoryType)
		if !ok || treeType == nil {
			return nil, nil, nil, false
		}
		variant, ok := treeType.Variant(testExpr.Pattern.Variant)
		if !ok || variant == nil {
			return nil, nil, nil, false
		}
		return treeType, variant, testExpr.Pattern, true
	}
	treeType, variant, ok := s.treeIsTarget(expr)
	if !ok || treeType == nil || variant == nil {
		return nil, nil, nil, false
	}
	pattern := &ast.MatchVariantPattern{Position: expr.Pos(), EnumName: treeType.Name, Variant: variant.Name}
	return treeType, variant, pattern, true
}
func (s *functionState) treeIsTarget(expr ast.Expr) (*semantic.TreeCategoryType, *semantic.EnumVariant, bool) {
	if fieldExpr, ok := expr.(*ast.FieldExpr); ok {
		return s.treeConstructorInfoFromField(fieldExpr)
	}
	typedExpr, ok := expr.(*ast.TypeExprExpr)
	if !ok || typedExpr == nil || typedExpr.Type == nil {
		return nil, nil, false
	}
	named, ok := typedExpr.Type.(*ast.NamedType)
	if !ok || named == nil {
		return nil, nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, nil, false
	}
	categoryName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, ok := s.g.result.NamedTypes[categoryName]
	if !ok {
		return nil, nil, false
	}
	treeType, ok := base.(*semantic.TreeCategoryType)
	if !ok || treeType == nil {
		return nil, nil, false
	}
	variant, ok := treeType.Variant(variantName)
	if !ok {
		return treeType, nil, false
	}
	return treeType, variant, true
}
func (s *functionState) emitTreeIsTest(leftExpr ast.Expr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, pattern *ast.MatchVariantPattern) (C.LLVMValueRef, semantic.Type, error) {
	leftType := s.exprType(leftExpr)
	treeValue, _, err := s.emitExpr(leftExpr, leftType)
	if err != nil {
		return nil, nil, err
	}
	if pattern != nil && len(pattern.Args) != 0 {
		successBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.ok"))
		failureBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.fail"))
		contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("is.tree.variant.cont"))
		if _, _, err := s.emitMatchPatternTest(pattern, treeValue, nil, leftType, nil, leftExpr, nil, successBB, failureBB); err != nil {
			return nil, nil, err
		}

		C.LLVMPositionBuilderAtEnd(s.builder, successBB)
		successValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 1, 0)
		C.LLVMBuildBr(s.builder, contBB)
		successEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, failureBB)
		failureValue := C.LLVMConstInt(C.LLVMInt1TypeInContext(s.g.context), 0, 0)
		C.LLVMBuildBr(s.builder, contBB)
		failureEnd := C.LLVMGetInsertBlock(s.builder)

		C.LLVMPositionBuilderAtEnd(s.builder, contBB)
		phi := C.LLVMBuildPhi(s.builder, C.LLVMInt1TypeInContext(s.g.context), cStringFree("is.tree.variant.result"))
		values := []C.LLVMValueRef{successValue, failureValue}
		blocks := []C.LLVMBasicBlockRef{successEnd, failureEnd}
		C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
		return phi, s.g.result.NamedTypes["bool"], nil
	}
	tagValue, err := s.extractTreeCategoryTagValue(treeValue, treeType)
	if err != nil {
		return nil, nil, err
	}
	tagConst, err := s.enumTagConstant(variant.Tag)
	if err != nil {
		return nil, nil, err
	}
	cmp := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), tagValue, tagConst, cStringFree("istree.tag"))
	return cmp, s.g.result.NamedTypes["bool"], nil
}
func (s *functionState) emitTreeCategoryAlloc(treeType *semantic.TreeCategoryType, owner treeAllocOwnerBinding) (C.LLVMValueRef, error) {
	if treeType == nil {
		return nil, fmt.Errorf("missing tree constructor metadata")
	}
	storageType, err := s.g.ensureTreeCategoryBody(treeType)
	if err != nil {
		return nil, err
	}
	storageBytes, err := s.g.abiSizeOfLLVMType(storageType)
	if err != nil {
		return nil, err
	}
	if !owner.isPerm {
		if owner.storeValue != nil && owner.storeType != nil {
			arenaRef, err := s.emitTreeStoreArenaValue(owner.storeValue, owner.storeType)
			if err != nil {
				return nil, err
			}
			owner.arenaRef = arenaRef
		}
		if owner.arenaRef == nil {
			return nil, fmt.Errorf("missing Arena owner for tree constructor")
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, err
		}
		sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(storageBytes), 0)
		arenaType := s.g.result.NamedTypes["Arena"]
		arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		voidType := s.g.result.NamedTypes["void"]
		voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
			return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
		})
		callee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
		if err != nil {
			return nil, err
		}
		llvmFnType, err := s.g.lowerFunctionType(allocType)
		if err != nil {
			return nil, err
		}
		return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{owner.arenaRef, sizeValue}, "tree.region.alloc"), nil
	}
	voidType := s.g.result.NamedTypes["void"]
	heapVoidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageHeap, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("alloc_perm", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "alloc_perm", Params: []semantic.Type{s.g.result.NamedTypes["i64"]}, Return: heapVoidRefType}
	})
	callee, err := s.g.ensureFunctionDeclared("alloc_perm", allocType)
	if err != nil {
		return nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, err
	}
	sizeValue := C.LLVMConstInt(C.LLVMInt64TypeInContext(s.g.context), C.ulonglong(storageBytes), 0)
	return s.buildCall(llvmFnType, callee, []C.LLVMValueRef{sizeValue}, "tree.alloc"), nil
}
func (s *functionState) emitTreeConstructorValue(callExpr *ast.CallExpr, treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, args []ast.Expr, argNames []string, owner *treeAllocOwnerBinding) (C.LLVMValueRef, semantic.Type, error) {
	if treeType == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing tree constructor metadata")
	}
	resolvedOwner := treeAllocOwnerBinding{}
	if owner != nil {
		resolvedOwner = *owner
	} else {
		activeOwner, ok := s.lookupTreeAllocOwnerForFamily(treeType.Family)
		if !ok {
			return nil, nil, fmt.Errorf("tree constructor %s.%s requires an active in <owner>: scope or explicit new[owner]", treeType.Name, variant.Name)
		}
		resolvedOwner = activeOwner
	}
	orderedArgs, commonArgs, err := s.resolveTreeConstructorArgs(callExpr, treeType, variant, args, argNames)
	if err != nil {
		return nil, nil, err
	}
	if len(orderedArgs) != len(variant.Payload) {
		return nil, nil, fmt.Errorf("tree constructor %s.%s expects %d arguments, got %d", treeType.Name, variant.Name, len(variant.Payload), len(args))
	}
	storeValue, _, err := s.ensureTreeOwnerStoreValue(resolvedOwner, treeType.Family)
	if err != nil {
		return nil, nil, err
	}
	arenaValue := s.emitTreeStoreArenaValueNamed(storeValue, "tree.store.arena")
	stateValue := s.emitTreeStoreStateValueNamed(storeValue, "tree.store.state")
	if treeCategoryLayoutPlan(treeType).isCategoryUnion() {
		return s.emitTreeCategoryUnionConstructorValue(treeType, variant, orderedArgs, commonArgs, arenaValue, stateValue)
	}
	memberType := treeType.VariantViewType(variant)
	slot, err := s.emitTreeExactAppendSlot(arenaValue, stateValue, treeType.Family, memberType, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	fieldValues := make([]C.LLVMValueRef, 0, len(treeExactFieldDecls(memberType)))
	for _, fieldDecl := range treeCommonFieldDecls(treeType) {
		field, ok := treeExactFieldInfo(memberType, fieldDecl.Name)
		if !ok {
			return nil, nil, fmt.Errorf("missing tree common field %s.%s", treeType.Name, fieldDecl.Name)
		}
		fieldValue, _, err := s.emitExpr(commonArgs[fieldDecl.Name], field.Type)
		if err != nil {
			return nil, nil, err
		}
		fieldValues = append(fieldValues, fieldValue)
	}
	for i, payloadType := range variant.Payload {
		fieldValue, _, err := s.emitExpr(orderedArgs[i], payloadType)
		if err != nil {
			return nil, nil, err
		}
		fieldValues = append(fieldValues, fieldValue)
	}
	if err := s.emitTreeStoreExactRowValueAtIndex(slot.tablePtr, memberType, slot.rowIndex, fieldValues, "tree.ctor"); err != nil {
		return nil, nil, err
	}
	if err := s.emitTreeTableSetCount(slot.tablePtr, memberType, slot.neededCount, "tree.ctor"); err != nil {
		return nil, nil, err
	}
	keyValue, err := s.buildTreeHandleKey(variant.Tag, slot.rowIndex, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	handleValue, err := s.buildTreeHandleValue(treeType.Family, stateValue, keyValue, "tree.ctor")
	if err != nil {
		return nil, nil, err
	}
	return handleValue, treeType, nil
}

func (s *functionState) emitTreeCategoryUnionConstructorValue(treeType *semantic.TreeCategoryType, variant *semantic.EnumVariant, orderedArgs []ast.Expr, commonArgs map[string]ast.Expr, arenaValue C.LLVMValueRef, stateValue C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	if treeType == nil || treeType.Family == nil || variant == nil {
		return nil, nil, fmt.Errorf("missing category-union tree constructor metadata")
	}
	slot, err := s.emitTreeCategoryUnionAppendSlot(arenaValue, stateValue, treeType.Family, treeType, "tree.category")
	if err != nil {
		return nil, nil, err
	}
	if err := s.emitTreeCategoryUnionKindAtIndex(slot.tablePtr, treeType, slot.rowIndex, variant.Tag, "tree.category"); err != nil {
		return nil, nil, err
	}
	payloadType, err := s.g.lowerTreeCategoryUnionVariantPayloadType(treeType, variant)
	if err != nil {
		return nil, nil, err
	}
	if C.LLVMGetTypeKind(payloadType) != C.LLVMVoidTypeKind {
		payloadValue := C.LLVMGetUndef(payloadType)
		fieldIndex := 0
		for _, fieldDecl := range treeCommonFieldDecls(treeType) {
			field, ok := treeExactFieldInfo(treeType.VariantViewType(variant), fieldDecl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("missing tree common field %s.%s", treeType.Name, fieldDecl.Name)
			}
			fieldValue, _, err := s.emitExpr(commonArgs[fieldDecl.Name], field.Type)
			if err != nil {
				return nil, nil, err
			}
			payloadValue = C.LLVMBuildInsertValue(s.builder, payloadValue, fieldValue, C.unsigned(fieldIndex), cStringFree("tree.category.payload.field"))
			fieldIndex++
		}
		for i, payloadType := range variant.Payload {
			fieldValue, _, err := s.emitExpr(orderedArgs[i], payloadType)
			if err != nil {
				return nil, nil, err
			}
			payloadValue = C.LLVMBuildInsertValue(s.builder, payloadValue, fieldValue, C.unsigned(fieldIndex), cStringFree("tree.category.payload.field"))
			fieldIndex++
		}
		if err := s.emitTreeCategoryUnionPayloadAtIndex(slot.tablePtr, treeType, variant, slot.rowIndex, payloadType, payloadValue, "tree.category"); err != nil {
			return nil, nil, err
		}
	}
	if err := s.emitTreeCategoryUnionTableSetCount(slot.tablePtr, treeType, slot.neededCount, "tree.category"); err != nil {
		return nil, nil, err
	}
	handleValue, err := s.coerceValue(slot.rowIndex, s.g.result.NamedTypes["usize"], s.g.result.NamedTypes["u32"])
	if err != nil {
		return nil, nil, err
	}
	return handleValue, treeType, nil
}
