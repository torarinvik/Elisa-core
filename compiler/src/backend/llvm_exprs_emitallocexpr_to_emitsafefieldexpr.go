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
)

func (s *functionState) emitAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr.Owner == nil {
		return s.emitScopedPackedAllocExpr(expr)
	}
	if updateExpr, ok := expr.Value.(*ast.RecordUpdateExpr); ok && updateExpr != nil {
		memberType := semantic.StripAggregateStateType(s.exprType(updateExpr.Base))
		if _, exact := semantic.TreeExactTag(memberType); exact {
			owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
			if err != nil {
				return nil, nil, err
			}
			if !ownerOK {
				return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
			}
			return s.emitTreeExactMemberUpdateExpr(updateExpr, memberType, &owner)
		}
	}
	if callExpr, ok := expr.Value.(*ast.CallExpr); ok {
		if memberType, ok := s.treeExactMemberConstructorCall(callExpr); ok {
			owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
			if err != nil {
				return nil, nil, err
			}
			if !ownerOK {
				return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
			}
			return s.emitTreeExactMemberConstructorValue(callExpr, memberType, &owner)
		}
	}
	if treeType, variant, callExpr, ok := s.treeAllocConstructorInfo(expr.Value); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor")
		}
		owner, ownerOK, err := s.classifyTreeAllocOwnerExpr(expr.Owner)
		if err != nil {
			return nil, nil, err
		}
		if !ownerOK {
			return nil, nil, fmt.Errorf("tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference")
		}
		return s.emitTreeConstructorValue(callExpr, treeType, variant, treeAllocArgs(callExpr), treeAllocArgNames(callExpr), &owner)
	}
	if isTreeAllocPermExpr(expr.Owner) {
		return nil, nil, fmt.Errorf("new[perm] expects a tree constructor")
	}
	if _, ok := s.exprType(expr.Owner).(*semantic.PackedEnumStoreType); ok {
		return s.emitPackedAllocExpr(expr)
	}
	ownerIdent, ok := expr.Owner.(*ast.Ident)
	if !ok {
		return nil, nil, fmt.Errorf("only region-backed new[...] is lowered so far")
	}
	binding, ok := s.lookupBinding(ownerIdent.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown region %q during LLVM lowering", ownerIdent.Name)
	}
	valueType := s.exprType(expr.Value)
	if valueType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for region allocation value in %q", ownerIdent.Name)
	}
	value, _, err := s.emitExpr(expr.Value, valueType)
	if err != nil {
		return nil, nil, err
	}
	sizeBytes, err := s.sizeOfType(valueType)
	if err != nil {
		return nil, nil, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	arenaRefType := &semantic.RefType{Elem: binding.typ, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{binding.ptr, sizeValue}, "region.alloc")
	C.LLVMBuildStore(s.builder, value, allocPtr)
	return allocPtr, s.exprType(expr), nil
}
func (s *functionState) emitScopedPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	switch n := expr.Value.(type) {
	case *ast.FieldExpr:
		enumType, variant, ok := s.enumConstructorInfoFromField(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(nil, store.value, enumType, variant, nil, nil)
	case *ast.CallExpr:
		enumType, variant, ok := s.enumConstructorInfo(n)
		if !ok || enumType == nil || variant == nil || !enumType.Packed {
			return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
		}
		store, ok := s.lookupPackedStore(enumType)
		if !ok {
			return nil, nil, fmt.Errorf("missing active packed enum store for %s", enumType.Name)
		}
		return s.emitPackedEnumConstructorAlloc(n, store.value, enumType, variant, n.Args, n.ArgNames)
	default:
		return nil, nil, fmt.Errorf("new without [...] expects a packed enum constructor inside an in-store block")
	}
}
func (s *functionState) emitPackedAllocExpr(expr *ast.AllocExpr) (C.LLVMValueRef, semantic.Type, error) {
	storeValue, _, err := s.emitExpr(expr.Owner, nil)
	if err != nil {
		return nil, nil, err
	}
	if fieldExpr, ok := expr.Value.(*ast.FieldExpr); ok {
		enumType, variant, ok := s.enumConstructorInfoFromField(fieldExpr)
		if ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
			return s.emitPackedEnumConstructorAlloc(nil, storeValue, enumType, variant, nil, nil)
		}
	}
	callExpr, ok := expr.Value.(*ast.CallExpr)
	if !ok {
		return nil, nil, fmt.Errorf("packed enum allocation expects a constructor call")
	}
	enumType, variant, ok := s.enumConstructorInfo(callExpr)
	if !ok || enumType == nil || variant == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("packed enum allocation expects a packed enum constructor call")
	}
	return s.emitPackedEnumConstructorAlloc(callExpr, storeValue, enumType, variant, callExpr.Args, callExpr.ArgNames)
}
func (s *functionState) nodeTableFillTypeArgs(expr *ast.CallExpr) (*semantic.EnumType, semantic.Type, error) {
	if expr == nil || callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, fmt.Errorf("node_table_fill expects explicit specialization")
	}
	_, specialize, ok := callSpecializedIdent(expr.Func)
	if !ok || specialize == nil || len(specialize.TypeArgs) != 2 {
		return nil, nil, fmt.Errorf("node_table_fill expects exactly 2 type arguments")
	}
	enumArg, err := s.resolveTypeExpr(specialize.TypeArgs[0])
	if err != nil {
		return nil, nil, err
	}
	enumType, ok := semantic.StripAggregateStateType(enumArg).(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, nil, fmt.Errorf("node_table_fill expects a packed enum type argument")
	}
	elemType, err := s.resolveTypeExpr(specialize.TypeArgs[1])
	if err != nil {
		return nil, nil, err
	}
	return enumType, elemType, nil
}
func (s *functionState) emitNodeKeyIndexValue(expr ast.Expr) (C.LLVMValueRef, *semantic.EnumType, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	keyType := s.exprType(expr)
	enumType, ok := semantic.NodeKeyEnumType(keyType)
	if !ok || enumType == nil {
		return nil, nil, false, nil
	}
	value, actualType, err := s.emitExpr(expr, keyType)
	if err != nil {
		return nil, nil, true, err
	}
	if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
		loaded, loadErr := s.loadValue(value, refType.Elem, "nodekey.load")
		if loadErr != nil {
			return nil, nil, true, loadErr
		}
		value = loaded
	}
	return C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("nodekey.index")), enumType, true, nil
}
func (s *functionState) emitDenseKeyHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callIdentName(expr) != "dense_key" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 2 {
		return nil, nil, true, fmt.Errorf("dense_key expects 2 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("dense_key requires frozen packed-store metadata")
	}
	var handleValue C.LLVMValueRef
	actualNodeType := s.exprType(expr.Args[0])
	sourceEnum, ok := denseKeySourceEnumType(actualNodeType)
	if !ok || sourceEnum == nil {
		return nil, nil, true, fmt.Errorf("dense_key expects a packed enum value or packedview")
	}
	if viewType, ok := actualNodeType.(*semantic.PackedVariantViewType); ok && viewType != nil {
		viewValue, _, err := s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		handleValue = C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("nodekey.view.handle"))
	} else {
		var actualType semantic.Type
		handleValue, actualType, err = s.emitExpr(expr.Args[0], actualNodeType)
		if err != nil {
			return nil, nil, true, err
		}
		if refType, ok := actualType.(*semantic.RefType); ok && refType != nil && refType.State == semantic.RefStateNonNull {
			handleValue, err = s.loadValue(handleValue, refType.Elem, "nodekey.handle")
			if err != nil {
				return nil, nil, true, err
			}
		}
	}
	var indexValue C.LLVMValueRef
	switch s.g.packedModeForEnum(sourceEnum) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		indexValue, err = s.coerceValue(handleValue, sourceEnum, s.g.result.NamedTypes["u32"])
		if err != nil {
			return nil, nil, true, err
		}
	default:
		return nil, nil, true, fmt.Errorf("unsupported packed enum ABI mode %d", s.g.packedModeForEnum(sourceEnum))
	}
	resultValue := C.LLVMGetUndef(resultLLVMType)
	resultValue = C.LLVMBuildInsertValue(s.builder, resultValue, indexValue, 0, cStringFree("nodekey.index.insert"))
	return resultValue, resultType, true, nil
}
func (s *functionState) emitNodeTableFillHelperCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if callSpecializedIdentName(expr) != "node_table_fill" {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 3 {
		return nil, nil, true, fmt.Errorf("node_table_fill expects 3 arguments, got %d", len(expr.Args))
	}
	resultType := s.exprType(expr)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, true, err
	}
	_, elemType, err := s.nodeTableFillTypeArgs(expr)
	if err != nil {
		return nil, nil, true, err
	}
	storeValue, storeType, err := s.emitPackedStoreValueFromExpr(expr.Args[1])
	if err != nil {
		return nil, nil, true, err
	}
	if storeType == nil || storeType.Enum == nil {
		return nil, nil, true, fmt.Errorf("node_table_fill requires frozen packed-store metadata")
	}
	countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "node.table.count")
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.alloc"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("node.table.merge"))
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), countValue, zeroCount, cStringFree("node.table.count.zero"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, allocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	arenaPtr, _, err := s.emitAddressOrTemp(expr.Args[0])
	if err != nil {
		return nil, nil, true, err
	}
	elemSize, err := s.sizeOfType(elemType)
	if err != nil {
		return nil, nil, true, err
	}
	elemSizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(elemSize), 0)
	byteCount := C.LLVMBuildMul(s.builder, countValue, elemSizeValue, cStringFree("node.table.bytes"))
	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidType := s.g.result.NamedTypes["void"]
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return nil, nil, true, err
	}
	allocPtr := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaPtr, byteCount}, "node.table.alloc.ptr")
	viewType := &semantic.DArrayViewType{Elem: elemType, SurfaceName: "dview"}
	viewLLVMType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, nil, true, err
	}
	viewValue := C.LLVMGetUndef(viewLLVMType)
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, allocPtr, 0, cStringFree("node.table.view.data"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, countValue, 1, cStringFree("node.table.view.len"))
	viewValue = C.LLVMBuildInsertValue(s.builder, viewValue, elemSizeValue, 2, cStringFree("node.table.view.elem_size"))
	initValue, actualInitType, err := s.emitExpr(expr.Args[2], elemType)
	if err != nil {
		return nil, nil, true, err
	}
	initValue, err = s.coerceValue(initValue, actualInitType, elemType)
	if err != nil {
		return nil, nil, true, err
	}
	fillType := s.g.cachedRuntimeHelperType("arena_da_fill", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_da_fill", Params: []semantic.Type{viewType, elemType}, Return: s.g.result.NamedTypes["void"]}
	})
	fillCallee, err := s.g.ensureFunctionDeclared("arena_da_fill", fillType)
	if err != nil {
		return nil, nil, true, err
	}
	fillLLVMType, err := s.g.lowerFunctionType(fillType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(fillLLVMType, fillCallee, []C.LLVMValueRef{viewValue, initValue}, "")
	materialized := C.LLVMGetUndef(resultLLVMType)
	materialized = C.LLVMBuildInsertValue(s.builder, materialized, viewValue, 0, cStringFree("node.table.values.insert"))
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	zeroResult := C.LLVMConstNull(resultLLVMType)
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("node.table.result"))
	values := []C.LLVMValueRef{zeroResult, materialized}
	blocks := []C.LLVMBasicBlockRef{entryBlock, allocEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, true, nil
}
func (s *functionState) emitResolvedCall(callee C.LLVMValueRef, funcType *semantic.FuncType, direct bool, args []C.LLVMValueRef) (C.LLVMValueRef, semantic.Type, error) {
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	llvmFnType, err := s.g.lowerFunctionType(funcType)
	if err != nil {
		return nil, nil, err
	}
	if retUnion, ok := nonVoidErrorUnion(funcType.Return); ok {
		resultSlot, err := s.emitStackTempZeroed(retUnion.Value, "call.result")
		if err != nil {
			return nil, nil, err
		}
		callArgs := make([]C.LLVMValueRef, 0, len(args)+1)
		callArgs = append(callArgs, resultSlot)
		callArgs = append(callArgs, args...)
		var call C.LLVMValueRef
		if direct {
			call = s.buildTypedCall(llvmFnType, callee, callArgs, "calltmp", funcType)
		} else {
			call, err = s.emitFunctionValueCall(callee, funcType, callArgs, "calltmp")
			if err != nil {
				return nil, nil, err
			}
		}
		payload, err := s.loadValue(resultSlot, retUnion.Value, "call.payload")
		if err != nil {
			return nil, nil, err
		}
		unionValue, err := s.buildErrorUnionValue(retUnion, call, payload)
		if err != nil {
			return nil, nil, err
		}
		return unionValue, funcType.Return, nil
	}
	callName := ""
	if !isVoidType(funcType.Return) {
		callName = "calltmp"
	}
	var call C.LLVMValueRef
	if direct {
		call = s.buildTypedCall(llvmFnType, callee, args, callName, funcType)
	} else {
		call, err = s.emitFunctionValueCall(callee, funcType, args, "calltmp")
		if err != nil {
			return nil, nil, err
		}
	}
	return call, funcType.Return, nil
}
func (s *functionState) emitSafeFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, fmt.Errorf("optional field access requires a receiver")
	}
	resultType := s.exprType(expr)
	optionalType, ok := resultType.(*semantic.OptionalType)
	if !ok || optionalType == nil || optionalType.Value == nil {
		return nil, nil, fmt.Errorf("optional field access requires an optional result type")
	}
	presentValue, receiverValue, receiverType, err := s.emitSafeChainReceiverValue(expr.Object)
	if err != nil {
		return nil, nil, err
	}
	presentBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.present"))
	noneBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.none"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("safe.field.merge"))
	C.LLVMBuildCondBr(s.builder, presentValue, presentBB, noneBB)

	var (
		someValue  C.LLVMValueRef
		noneValue  C.LLVMValueRef
		presentEnd C.LLVMBasicBlockRef
		noneEnd    C.LLVMBasicBlockRef
	)

	C.LLVMPositionBuilderAtEnd(s.builder, presentBB)
	payloadValue, payloadType, err := s.emitFieldValueFromObjectValue(receiverValue, receiverType, expr.Field, "safe.field")
	if err != nil {
		return nil, nil, err
	}
	payloadValue, err = s.coerceValue(payloadValue, payloadType, optionalType.Value)
	if err != nil {
		return nil, nil, err
	}
	someValue, err = s.buildOptionalSome(optionalType, payloadValue)
	if err != nil {
		return nil, nil, err
	}
	presentEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, noneBB)
	noneValue, err = s.buildOptionalNone(optionalType)
	if err != nil {
		return nil, nil, err
	}
	noneEnd = C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	resultLLVMType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	phi := C.LLVMBuildPhi(s.builder, resultLLVMType, cStringFree("valuephi"))
	values := []C.LLVMValueRef{someValue, noneValue}
	blocks := []C.LLVMBasicBlockRef{presentEnd, noneEnd}
	C.LLVMAddIncoming(phi, llvmValueSlicePtr(values), llvmBlockSlicePtr(blocks), C.unsigned(len(values)))
	return phi, resultType, nil
}
