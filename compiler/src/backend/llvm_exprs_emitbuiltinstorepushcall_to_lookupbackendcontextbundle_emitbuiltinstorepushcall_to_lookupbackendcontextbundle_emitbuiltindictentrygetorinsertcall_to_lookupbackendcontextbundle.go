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

func (s *functionState) emitBuiltinDictEntryGetOrInsertCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(fieldExpr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("dict entry get_or_insert expects 1 argument, got %d", len(expr.Args))
	}
	// Region-parameterized dict: prefer the dict's own region arena (if live in
	// s.regions) over the ambient scope, mirroring darray push / dict insert.
	owner, ok := s.regionArenaOwner(entryType.Dict.Region)
	if !ok {
		owner, ok = s.lookupTreeAllocOwner()
	}
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("dict entry get_or_insert requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "dict.entry.get_or_insert.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	var entryPtr C.LLVMValueRef
	var err error
	if receiverRefType != nil {
		entryPtr, _, err = s.emitExpr(fieldExpr.Object, receiverRefType)
	} else {
		entryPtr, _, err = s.emitAddress(fieldExpr.Object)
		if err != nil {
			entryPtr = nil
			err = nil
		}
	}
	entryLLVMType, err := s.g.lowerType(entryType)
	if err != nil {
		return nil, nil, true, err
	}
	dictRefType := &semantic.RefType{Elem: entryType.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	dictRefLLVMType, err := s.g.lowerType(dictRefType)
	if err != nil {
		return nil, nil, true, err
	}
	keyLLVMType, err := s.g.lowerType(entryType.Dict.Key)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	var dictValue, keyValue, cachedValue C.LLVMValueRef
	var valuePtrPtr C.LLVMValueRef
	if entryPtr != nil {
		dictPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 0, cStringFree("dict.entry.get_or_insert.dict.ptr"))
		keyPtr := C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 1, cStringFree("dict.entry.get_or_insert.key.ptr"))
		valuePtrPtr = C.LLVMBuildStructGEP2(s.builder, entryLLVMType, entryPtr, 2, cStringFree("dict.entry.get_or_insert.value.ptr"))
		dictValue = C.LLVMBuildLoad2(s.builder, dictRefLLVMType, dictPtr, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = C.LLVMBuildLoad2(s.builder, keyLLVMType, keyPtr, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = C.LLVMBuildLoad2(s.builder, valueRefLLVMType, valuePtrPtr, cStringFree("dict.entry.get_or_insert.cached"))
	} else {
		entryValue, err := s.emitBuiltinDictEntryValue(fieldExpr.Object, entryType, receiverRefType)
		if err != nil {
			return nil, nil, true, err
		}
		dictValue = C.LLVMBuildExtractValue(s.builder, entryValue, 0, cStringFree("dict.entry.get_or_insert.dict"))
		keyValue = C.LLVMBuildExtractValue(s.builder, entryValue, 1, cStringFree("dict.entry.get_or_insert.key"))
		cachedValue = C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.get_or_insert.cached"))
	}
	nullValue := C.LLVMConstNull(valueRefLLVMType)
	hasCached := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), cachedValue, nullValue, cStringFree("dict.entry.get_or_insert.has"))
	nonNullBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.nonnull"))
	insertBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.insert"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("dict.entry.get_or_insert.merge"))
	C.LLVMBuildCondBr(s.builder, hasCached, nonNullBB, insertBB)
	C.LLVMPositionBuilderAtEnd(s.builder, nonNullBB)
	C.LLVMBuildBr(s.builder, mergeBB)
	nonNullEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, insertBB)
	insertedArg, _, err := s.emitExpr(expr.Args[0], entryType.Dict.Value)
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertCallee, getOrInsertType, err := s.ensureRuntimeFunction(s.dictMutationHelperName("arena_dict_get_or_insert"), map[string]semantic.Type{"K": entryType.Dict.Key, "T": entryType.Dict.Value})
	if err != nil {
		return nil, nil, true, err
	}
	getOrInsertLLVMType, err := s.g.lowerFunctionType(getOrInsertType)
	if err != nil {
		return nil, nil, true, err
	}
	insertedValue := s.buildCall(getOrInsertLLVMType, getOrInsertCallee, []C.LLVMValueRef{owner.arenaRef, dictValue, keyValue, insertedArg}, "dict.entry.get_or_insert.result")
	if valuePtrPtr != nil {
		C.LLVMBuildStore(s.builder, insertedValue, valuePtrPtr)
	}
	C.LLVMBuildBr(s.builder, mergeBB)
	insertEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	phi := C.LLVMBuildPhi(s.builder, valueRefLLVMType, cStringFree("dict.entry.get_or_insert.phi"))
	C.LLVMAddIncoming(phi, llvmValueSlicePtr([]C.LLVMValueRef{cachedValue, insertedValue}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{nonNullEnd, insertEnd}), 2)
	return phi, valueRefType, true, nil
}
func (s *functionState) emitBuiltinDictEntryFieldExpr(expr *ast.FieldExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil || expr.Object == nil {
		return nil, nil, false, nil
	}
	entryType, receiverRefType, ok := builtinDictEntryReceiverType(s.exprType(expr.Object))
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, nil, false, nil
	}
	entryValue, err := s.emitBuiltinDictEntryValue(expr.Object, entryType, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	valueRefLLVMType, err := s.g.lowerType(valueRefType)
	if err != nil {
		return nil, nil, true, err
	}
	switch expr.Field {
	case "value":
		value := C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.value"))
		return value, valueRefType, true, nil
	case "found":
		value := C.LLVMBuildExtractValue(s.builder, entryValue, 2, cStringFree("dict.entry.found.value"))
		nullValue := C.LLVMConstNull(valueRefLLVMType)
		found := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntNE), value, nullValue, cStringFree("dict.entry.found"))
		return found, s.g.result.NamedTypes["bool"], true, nil
	default:
		return nil, nil, false, nil
	}
}
// backendStructLikeValueType reports whether t is a by-value aggregate (struct or
// generic struct instance) for which a reference argument should be auto-dereferenced
// when fed to a by-value parameter.
func backendStructLikeValueType(t semantic.Type) bool {
	switch semantic.StripAggregateStateType(t).(type) {
	case *semantic.StructType:
		return true
	case *semantic.GenericInstanceType:
		return true
	}
	return false
}

func (s *functionState) emitCallArg(arg ast.Expr, expected semantic.Type, fnType *semantic.FuncType, index int) (C.LLVMValueRef, semantic.Type, error) {
	if s != nil && fnType != nil && fnType.SinkParamsKnown && index >= 0 && index < len(fnType.SinkParams) && fnType.SinkParams[index] {
		if operand, moved := backendExplicitMoveOperand(arg); moved {
			return s.emitMovedValue(operand, expected)
		}
		return s.emitMovedValue(arg, expected)
	}
	if expectedRef, ok := expected.(*semantic.RefType); ok && expectedRef != nil {
		actual := s.exprType(arg)
		if actual != nil && !semantic.AssignableTo(expected, actual) && semantic.AssignableTo(expectedRef.Elem, actual) {
			ptr, valueType, err := s.emitValueAddress(arg)
			if err == nil {
				return ptr, &semantic.RefType{
					Elem:            valueType,
					Mutable:         expectedRef.Mutable,
					State:           semantic.RefStateNonNull,
					Storage:         expectedRef.Storage,
					ExplicitStorage: expectedRef.ExplicitStorage,
				}, nil
			}
		}
	} else if expected != nil && backendStructLikeValueType(expected) {
		// Symmetric autoderef (mirror of the autoref branch above): a by-VALUE struct
		// parameter fed a REFERENCE argument whose pointee is that struct — load the
		// pointee. Reaches the generic-dispatch case where a `mutable R&` receiver is
		// passed to a protocol method whose `self` is by value (`self: Self`); the
		// numeric/bool ref→value path stays in coerceValue (with its uintptr nuance).
		actual := s.exprType(arg)
		if actualRef, ok := actual.(*semantic.RefType); ok && actualRef != nil &&
			!semantic.AssignableTo(expected, actual) && semantic.AssignableTo(expected, actualRef.Elem) {
			ptr, _, err := s.emitExpr(arg, actual)
			if err != nil {
				return nil, nil, err
			}
			loaded, err := s.loadValue(ptr, actualRef.Elem, "ref.deref")
			if err != nil {
				return nil, nil, err
			}
			return loaded, actualRef.Elem, nil
		}
	}
	return s.emitExpr(arg, expected)
}
