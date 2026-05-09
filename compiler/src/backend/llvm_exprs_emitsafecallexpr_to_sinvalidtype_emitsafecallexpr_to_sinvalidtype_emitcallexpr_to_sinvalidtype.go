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

func (s *functionState) emitCallExpr(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, error) {
	if storeType, ok := s.packedStoreConstructorCall(expr); ok {
		return s.emitPackedStoreConstructorValue(expr, storeType)
	}
	if storeType, ok := s.treeStoreConstructorCall(expr); ok {
		return s.emitTreeStoreConstructorValue(expr, storeType)
	}
	if callIdentName(expr) == "freeze" {
		if len(expr.Args) != 1 {
			return nil, nil, fmt.Errorf("freeze expects 1 argument, got %d", len(expr.Args))
		}
		frozenType := s.exprType(expr)
		return s.emitExpr(expr.Args[0], frozenType)
	}
	if value, actualType, handled, err := s.emitDenseKeyHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitNodeTableFillHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinCloneCall(expr); handled {
		return value, actualType, err
	}
	if errorType, qualifiedTag, ok := s.errorConstructorInfo(expr); ok {
		return s.emitErrorConstructorValue(expr, errorType, qualifiedTag)
	}
	if enumType, variant, ok := s.enumConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown enum constructor")
		}
		if enumType != nil && enumType.Packed {
			store, ok := s.lookupPackedStore(enumType)
			if !ok {
				return nil, nil, fmt.Errorf("packed enum constructor %s.%s requires an active in %s: scope or explicit new[%s]", enumType.Name, variant.Name, enumType.StoreType.Name, enumType.StoreType.Name)
			}
			return s.emitPackedEnumConstructorAlloc(expr, store.value, enumType, variant, expr.Args, expr.ArgNames)
		}
		return s.emitEnumConstructorValue(expr, enumType, variant, expr.Args, expr.ArgNames)
	}
	if treeType, variant, ok := s.treeConstructorInfo(expr); ok {
		if variant == nil {
			return nil, nil, fmt.Errorf("unknown tree constructor")
		}
		return s.emitTreeConstructorValue(expr, treeType, variant, expr.Args, expr.ArgNames, nil)
	}
	if memberType, ok := s.treeExactMemberConstructorCall(expr); ok {
		return s.emitTreeExactMemberConstructorValue(expr, memberType, nil)
	}
	if value, actualType, handled, err := s.emitProofCarryingViewHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitTreeTraversalHelperCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedRuntimeCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringSliceCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedStringViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewCopyCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewEqCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaFromViewCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedArenaViewFillCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayPushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayExtendCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDArrayTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStorePushCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreReserveCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreClearCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreTruncateCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinStoreRowsCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinSOAValidCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitBuiltinDictEntryGetOrInsertCall(expr); handled {
		return value, actualType, err
	}
	if value, actualType, handled, err := s.emitSpecializedMemcpyCall(expr); handled {
		return value, actualType, err
	}
	callee, funcType, err := s.resolveCallTarget(expr)
	if err != nil {
		return nil, nil, err
	}
	if funcType == nil {
		return nil, nil, fmt.Errorf("call target does not have a function type")
	}
	if len(funcType.ImplicitParamNames) != 0 && !expr.ResolvedImplicitArgsValid {
		if recovered, ok := s.recoverImplicitCallArgs(expr, funcType); ok {
			expr.ResolvedImplicitArgs = recovered
			expr.ResolvedImplicitArgsValid = true
		} else {
			return nil, nil, fmt.Errorf("call to %s is missing resolved implicit arguments", funcType.Name)
		}
	}
	loweredArgs := expr.LoweredArgs()
	args := make([]C.LLVMValueRef, 0, len(loweredArgs))
	for i, arg := range loweredArgs {
		var expected semantic.Type
		if i < len(funcType.Params) {
			expected = funcType.Params[i]
		}
		value, _, err := s.emitCallArg(arg, expected, funcType, i)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, value)
	}
	return s.emitResolvedCall(callee, funcType, s.directCallTarget(expr.Func), args)
}
func builtinDArrayPushReceiverType(t semantic.Type) (*semantic.DArrayType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*semantic.DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}
func builtinDArrayExtendSourceType(t semantic.Type) (semantic.Type, bool) {
	if t == nil {
		return nil, false
	}
	switch tt := t.(type) {
	case *semantic.DArrayType, *semantic.DArrayViewType, *semantic.ArrayType:
		return t, true
	case *semantic.RefType:
		switch tt.Elem.(type) {
		case *semantic.DArrayType, *semantic.DArrayViewType, *semantic.ArrayType:
			return tt.Elem, true
		}
	}
	return nil, false
}
func builtinStoreReceiverType(t semantic.Type) (*semantic.StructType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := semantic.StripAggregateStateType(t).(*semantic.StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}
func builtinDictEntryReceiverType(t semantic.Type) (*semantic.DictEntryType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if entryType, ok := semantic.StripAggregateStateType(t).(*semantic.DictEntryType); ok && entryType != nil {
		return entryType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	entryType, ok := semantic.StripAggregateStateType(refType.Elem).(*semantic.DictEntryType)
	if !ok || entryType == nil {
		return nil, nil, false
	}
	return entryType, refType, true
}
func builtinDictReceiverType(t semantic.Type) (*semantic.DictType, *semantic.RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if dictType, ok := t.(*semantic.DictType); ok && dictType != nil {
		return dictType, nil, true
	}
	refType, ok := t.(*semantic.RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	dictType, ok := refType.Elem.(*semantic.DictType)
	if !ok || dictType == nil {
		return nil, nil, false
	}
	return dictType, refType, true
}
func builtinDictEntryValueRefType(dictType *semantic.DictType) *semantic.RefType {
	if dictType == nil {
		return &semantic.RefType{Elem: sInvalidType(), Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	}
	return &semantic.RefType{Elem: dictType.Value, Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}
func sInvalidType() semantic.Type {
	return &semantic.InvalidType{}
}
