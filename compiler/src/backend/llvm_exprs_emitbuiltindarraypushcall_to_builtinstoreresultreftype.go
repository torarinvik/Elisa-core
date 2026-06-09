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

func (s *functionState) emitBuiltinDArrayPushCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray push expects 1 argument, got %d", len(expr.Args))
	}
	if s.darrayPushUsesBulkAppend(darrayType, expr.Args[0]) {
		extendExpr := &ast.CallExpr{
			Position: expr.Position,
			Func: &ast.FieldExpr{
				Position: fieldExpr.Position,
				Object:   fieldExpr.Object,
				Field:    "extend",
			},
			Args: expr.Args,
		}
		if s.g != nil && s.g.result != nil && s.g.result.ExprTypes != nil {
			s.g.result.ExprTypes[extendExpr.Func] = s.exprType(expr.Func)
			s.g.result.ExprTypes[extendExpr] = s.exprType(expr)
		}
		return s.emitBuiltinDArrayExtendCall(extendExpr)
	}
	// Region-parameterized containers: prefer the arena of the darray's own
	// region (if that region is live in s.regions) over the ambient scope. In
	// today's code a container's region == its ambient scope, so this resolves
	// to the same arena (behavior-preserving); it becomes load-bearing once a
	// helper pushes through a borrow whose region differs from the ambient one.
	owner, ok := s.darrayGrowthOwner(fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("darray push requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.push.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	itemValue, _, err := s.emitExpr(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.push.count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, C.LLVMConstInt(usizeLLVMType, 1, 0), "darray.push.needed")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.push"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.push.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	slotPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("darray.push.slot"))
	C.LLVMBuildStore(s.builder, itemValue, slotPtr)
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	return darrayPtr, resultType, true, nil
}

func (s *functionState) darrayPushUsesBulkAppend(darrayType *semantic.DArrayType, arg ast.Expr) bool {
	if s == nil || darrayType == nil || arg == nil {
		return false
	}
	sourceType := s.exprType(arg)
	if sourceType == nil || semantic.SameType(darrayType.Elem, sourceType) {
		return false
	}
	baseType, ok := builtinDArrayExtendSourceType(sourceType)
	if !ok || baseType == nil {
		return false
	}
	switch tt := baseType.(type) {
	case *semantic.DArrayType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	case *semantic.DArrayViewType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	case *semantic.ArrayType:
		return semantic.SameType(darrayType.Elem, tt.Elem)
	default:
		return false
	}
}

func (s *functionState) emitBuiltinDArrayExtendCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray extend expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.darrayGrowthOwner(fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("darray extend requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.extend.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceData, sourceCount, err := s.emitBuiltinDArrayExtendSource(expr.Args[0], darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	zeroCount := C.LLVMConstInt(usizeLLVMType, 0, 0)
	isZero := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), sourceCount, zeroCount, cStringFree("darray.extend.count.zero"))
	callBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.call"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.extend.merge"))
	C.LLVMBuildCondBr(s.builder, isZero, mergeBB, callBB)

	C.LLVMPositionBuilderAtEnd(s.builder, callBB)
	countPtr, _, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.extend.count"))
	neededValue, err := s.emitCheckedUSizeAdd(currentCount, sourceCount, "darray.extend.needed")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.extend"); err != nil {
		return nil, nil, true, err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	itemsValue := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree("darray.extend.items"))
	elemLLVMType, err := s.g.lowerType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	dstPtr := C.LLVMBuildGEP2(s.builder, elemLLVMType, itemsValue, llvmValueSlicePtr([]C.LLVMValueRef{currentCount}), 1, cStringFree("darray.extend.dst"))
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return nil, nil, true, err
	}
	byteCount, err := s.emitCheckedElemByteCount(sourceCount, elemSizeBytes, "darray.extend")
	if err != nil {
		return nil, nil, true, err
	}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memcpyType := s.g.cachedRuntimeHelperType("arena_memcpy", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_memcpy", Params: []semantic.Type{voidRefType, voidRefType, s.g.result.NamedTypes["usize"]}, Return: voidRefType}
	})
	memcpyCallee, err := s.g.ensureFunctionDeclared("arena_memcpy", memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return nil, nil, true, err
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dstPtr, sourceData, byteCount}, "darray.extend.memcpy")
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayExtendSource(arg ast.Expr, elemType semantic.Type) (C.LLVMValueRef, C.LLVMValueRef, error) {
	sourceType := s.exprType(arg)
	baseType, ok := builtinDArrayExtendSourceType(sourceType)
	if !ok || baseType == nil {
		return nil, nil, fmt.Errorf("darray extend expects a compatible darray, view, or array source")
	}
	switch tt := baseType.(type) {
	case *semantic.DArrayType:
		var arrayValue C.LLVMValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			arrayValue, err = s.loadValue(ptr, tt, "darray.extend.src.darray")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			arrayValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := C.LLVMBuildExtractValue(s.builder, arrayValue, 0, cStringFree("darray.extend.src.data"))
		count := C.LLVMBuildExtractValue(s.builder, arrayValue, 1, cStringFree("darray.extend.src.count"))
		return data, count, nil
	case *semantic.DArrayViewType:
		var viewValue C.LLVMValueRef
		if refType, ok := sourceType.(*semantic.RefType); ok && refType != nil {
			ptr, _, err := s.emitExpr(arg, sourceType)
			if err != nil {
				return nil, nil, err
			}
			viewValue, err = s.loadValue(ptr, tt, "darray.extend.src.view")
			if err != nil {
				return nil, nil, err
			}
		} else {
			var err error
			viewValue, _, err = s.emitExpr(arg, tt)
			if err != nil {
				return nil, nil, err
			}
		}
		data := C.LLVMBuildExtractValue(s.builder, viewValue, 0, cStringFree("darray.extend.src.data"))
		count := C.LLVMBuildExtractValue(s.builder, viewValue, 1, cStringFree("darray.extend.src.len"))
		return data, count, nil
	case *semantic.ArrayType:
		arrayType, arrayPtr, ok, err := s.fixedArraySliceBase(arg)
		if err != nil {
			return nil, nil, err
		}
		if !ok || arrayType == nil {
			return nil, nil, fmt.Errorf("darray extend could not materialize fixed array source")
		}
		usizeType := s.g.result.NamedTypes["usize"]
		usizeLLVMType, err := s.g.lowerType(usizeType)
		if err != nil {
			return nil, nil, err
		}
		count := C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0)
		elemRefType := &semantic.RefType{Elem: elemType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
		elemRefLLVMType, err := s.g.lowerType(elemRefType)
		if err != nil {
			return nil, nil, err
		}
		if arrayType.ConstSize == 0 {
			return C.LLVMConstNull(elemRefLLVMType), count, nil
		}
		arrayLLVMType, err := s.g.lowerType(arrayType)
		if err != nil {
			return nil, nil, err
		}
		zeroIndex := C.LLVMConstInt(usizeLLVMType, 0, 0)
		indices := []C.LLVMValueRef{zeroIndex, zeroIndex}
		data := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree("darray.extend.src.array.data"))
		return data, count, nil
	default:
		return nil, nil, fmt.Errorf("unsupported darray extend source %T", baseType)
	}
}
func (s *functionState) emitBuiltinDArrayReserveCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray reserve expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.darrayGrowthOwner(fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("darray reserve requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.reserve.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, err := s.emitReserveBoundExpr(expr.Args[0], "darray.reserve.bound")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.reserve"); err != nil {
		return nil, nil, true, err
	}
	return darrayPtr, resultType, true, nil
}

// emitBuiltinDArrayResizeCall lowers `da.resize(n)`: ensure capacity >= n (one growth, like
// reserve) then seal count := n (like clear, but storing n instead of 0). The result is a
// fixed-length array filled by index — no per-element push.
func (s *functionState) emitBuiltinDArrayResizeCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "resize" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray resize expects 1 argument, got %d", len(expr.Args))
	}
	owner, ok := s.darrayGrowthOwner(fieldExpr.Object, darrayType)
	if !ok || (owner.arenaRef == nil && owner.arenaRefPtr == nil) {
		return nil, nil, true, fmt.Errorf("darray resize requires an active in <arena>: scope")
	}
	if owner.arenaRef == nil {
		arenaRef, err := s.treeOwnerArenaRefValue(owner, "darray.resize.owner.arena")
		if err != nil {
			return nil, nil, true, err
		}
		owner.arenaRef = arenaRef
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	neededValue, err := s.emitReserveBoundExpr(expr.Args[0], "darray.resize.bound")
	if err != nil {
		return nil, nil, true, err
	}
	if err := s.emitBuiltinDArrayEnsureCapacity(darrayPtr, darrayType, owner.arenaRef, neededValue, "darray.resize"); err != nil {
		return nil, nil, true, err
	}
	countPtr, _, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, neededValue, countPtr)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayClearCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 0 {
		return nil, nil, true, fmt.Errorf("darray clear expects 0 arguments, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	C.LLVMBuildStore(s.builder, C.LLVMConstInt(usizeLLVMType, 0, 0), countPtr)
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayTruncateCall(expr *ast.CallExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	if expr == nil {
		return nil, nil, false, nil
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, nil, false, nil
	}
	receiverType := s.exprType(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, nil, false, nil
	}
	if len(expr.Args) != 1 {
		return nil, nil, true, fmt.Errorf("darray truncate expects 1 argument, got %d", len(expr.Args))
	}
	darrayPtr, resultType, err := s.emitBuiltinDArrayReceiverPtr(fieldExpr.Object, receiverRefType)
	if err != nil {
		return nil, nil, true, err
	}
	countPtr, usizeType, err := s.emitBuiltinDArrayCountPtr(darrayPtr, darrayType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	currentCount := C.LLVMBuildLoad2(s.builder, usizeLLVMType, countPtr, cStringFree("darray.truncate.count"))
	limitValue, _, err := s.emitExpr(expr.Args[0], usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	shouldStore := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), limitValue, currentCount, cStringFree("darray.truncate.lt"))
	entryBlock := C.LLVMGetInsertBlock(s.builder)
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.store"))
	mergeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree("darray.truncate.merge"))
	C.LLVMBuildCondBr(s.builder, shouldStore, storeBB, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	C.LLVMBuildStore(s.builder, limitValue, countPtr)
	C.LLVMBuildBr(s.builder, mergeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, mergeBB)
	_ = entryBlock
	return darrayPtr, resultType, true, nil
}
func (s *functionState) emitBuiltinDArrayReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	resultType := &semantic.RefType{Elem: s.exprType(receiver), Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	return ptr, resultType, nil
}
func (s *functionState) emitBuiltinDArrayCountPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 1, cStringFree("darray.count.ptr"))
	return countPtr, s.g.result.NamedTypes["usize"], nil
}
func (s *functionState) emitBuiltinDArrayItemsPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, error) {
	if darrayType == nil {
		return nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, err
	}
	return C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 0, cStringFree("darray.items.ptr")), nil
}
func (s *functionState) emitBuiltinDArrayCapacityPtr(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType) (C.LLVMValueRef, semantic.Type, error) {
	if darrayType == nil {
		return nil, nil, fmt.Errorf("missing darray type")
	}
	containerType, err := s.g.lowerType(darrayType)
	if err != nil {
		return nil, nil, err
	}
	capacityPtr := C.LLVMBuildStructGEP2(s.builder, containerType, darrayPtr, 2, cStringFree("darray.capacity.ptr"))
	return capacityPtr, s.g.result.NamedTypes["usize"], nil
}
func (s *functionState) emitBuiltinStoreReceiverPtr(receiver ast.Expr, receiverRefType *semantic.RefType) (C.LLVMValueRef, semantic.Type, error) {
	if receiverRefType != nil {
		ptr, _, err := s.emitExpr(receiver, receiverRefType)
		return ptr, receiverRefType.Elem, err
	}
	ptr, _, err := s.emitAddress(receiver)
	if err != nil {
		return nil, nil, err
	}
	return ptr, s.exprType(receiver), nil
}
func (s *functionState) emitBuiltinStoreFieldDArrayPtr(storePtr C.LLVMValueRef, storeType semantic.Type, fieldName string) (C.LLVMValueRef, *semantic.DArrayType, error) {
	fieldType, index, _, _, err := s.g.fieldInfo(storeType, fieldName)
	if err != nil {
		return nil, nil, err
	}
	darrayType, ok := fieldType.(*semantic.DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, fmt.Errorf("store field %q is not a darray column", fieldName)
	}
	containerType, err := s.g.lowerType(storeType)
	if err != nil {
		return nil, nil, err
	}
	fieldPtr := C.LLVMBuildStructGEP2(s.builder, containerType, storePtr, C.unsigned(index), cStringFree("store."+fieldName+".ptr"))
	return fieldPtr, darrayType, nil
}
func (s *functionState) emitBuiltinDArrayEnsureCapacity(darrayPtr C.LLVMValueRef, darrayType *semantic.DArrayType, arenaRef C.LLVMValueRef, neededValue C.LLVMValueRef, name string) error {
	if darrayType == nil {
		return fmt.Errorf("missing darray type")
	}
	capacityPtr, usizeType, err := s.emitBuiltinDArrayCapacityPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerType(usizeType)
	if err != nil {
		return err
	}
	currentCapacity := C.LLVMBuildLoad2(s.builder, usizeLLVMType, capacityPtr, cStringFree(name+".capacity"))
	hasCapacity := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntUGE), currentCapacity, neededValue, cStringFree(name+".capacity.ok"))
	growBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".grow"))
	contBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".cont"))
	C.LLVMBuildCondBr(s.builder, hasCapacity, contBB, growBB)

	C.LLVMPositionBuilderAtEnd(s.builder, growBB)
	zero := C.LLVMConstInt(usizeLLVMType, 0, 0)
	initCap := C.LLVMConstInt(usizeLLVMType, 256, 0)
	doubledOrNeeded, err := s.emitSafeDoubledCapacity(currentCapacity, neededValue, usizeLLVMType, name)
	if err != nil {
		return err
	}
	baseCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentCapacity, zero, cStringFree(name+".capacity.zero")),
		initCap,
		doubledOrNeeded,
		cStringFree(name+".capacity.base"),
	)
	newCapacity := C.LLVMBuildSelect(
		s.builder,
		C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntULT), baseCapacity, neededValue, cStringFree(name+".capacity.lt")),
		neededValue,
		baseCapacity,
		cStringFree(name+".capacity.new"),
	)
	elemSizeBytes, err := s.sizeOfType(darrayType.Elem)
	if err != nil {
		return err
	}
	oldSize, err := s.emitCheckedElemByteCount(currentCapacity, elemSizeBytes, name+".old")
	if err != nil {
		return err
	}
	newSize, err := s.emitCheckedElemByteCount(newCapacity, elemSizeBytes, name+".new")
	if err != nil {
		return err
	}
	itemsPtr, err := s.emitBuiltinDArrayItemsPtr(darrayPtr, darrayType)
	if err != nil {
		return err
	}
	voidPtrType := C.LLVMPointerTypeInContext(s.g.context, 0)
	currentItems := C.LLVMBuildLoad2(s.builder, voidPtrType, itemsPtr, cStringFree(name+".items"))
	isNull := C.LLVMBuildICmp(s.builder, C.LLVMIntPredicate(C.LLVMIntEQ), currentItems, C.LLVMConstPointerNull(voidPtrType), cStringFree(name+".items.null"))

	arenaType := s.g.result.NamedTypes["Arena"]
	arenaRefType := &semantic.RefType{Elem: arenaType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	voidRefType := &semantic.RefType{Elem: s.g.result.NamedTypes["void"], State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	allocType := s.g.cachedRuntimeHelperType("arena_alloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_alloc", Params: []semantic.Type{arenaRefType, usizeType}, Return: voidRefType}
	})
	reallocType := s.g.cachedRuntimeHelperType("arena_realloc", func() *semantic.FuncType {
		return &semantic.FuncType{Name: "arena_realloc", Params: []semantic.Type{arenaRefType, voidRefType, usizeType, usizeType}, Return: voidRefType}
	})
	allocCallee, err := s.g.ensureFunctionDeclared("arena_alloc", allocType)
	if err != nil {
		return err
	}
	reallocCallee, err := s.g.ensureFunctionDeclared("arena_realloc", reallocType)
	if err != nil {
		return err
	}
	allocLLVMType, err := s.g.lowerFunctionType(allocType)
	if err != nil {
		return err
	}
	reallocLLVMType, err := s.g.lowerFunctionType(reallocType)
	if err != nil {
		return err
	}
	allocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".alloc"))
	reallocBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".realloc"))
	storeBB := C.LLVMAppendBasicBlockInContext(s.g.context, s.fnValue, cStringFree(name+".store"))
	C.LLVMBuildCondBr(s.builder, isNull, allocBB, reallocBB)

	C.LLVMPositionBuilderAtEnd(s.builder, allocBB)
	allocated := s.buildCall(allocLLVMType, allocCallee, []C.LLVMValueRef{arenaRef, newSize}, name+".alloc")
	allocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, reallocBB)
	reallocated := s.buildCall(reallocLLVMType, reallocCallee, []C.LLVMValueRef{arenaRef, currentItems, oldSize, newSize}, name+".realloc")
	reallocEnd := C.LLVMGetInsertBlock(s.builder)
	C.LLVMBuildBr(s.builder, storeBB)

	C.LLVMPositionBuilderAtEnd(s.builder, storeBB)
	newItems := C.LLVMBuildPhi(s.builder, voidPtrType, cStringFree(name+".items.new"))
	C.LLVMAddIncoming(newItems, llvmValueSlicePtr([]C.LLVMValueRef{allocated, reallocated}), llvmBlockSlicePtr([]C.LLVMBasicBlockRef{allocEnd, reallocEnd}), 2)
	C.LLVMBuildStore(s.builder, newItems, itemsPtr)
	C.LLVMBuildStore(s.builder, newCapacity, capacityPtr)
	C.LLVMBuildBr(s.builder, contBB)

	C.LLVMPositionBuilderAtEnd(s.builder, contBB)
	return nil
}
func builtinStoreResultRefType(storeType *semantic.StructType) *semantic.RefType {
	return &semantic.RefType{Elem: storeType, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
}
func builtinStorePushResultType(storeType *semantic.StructType, u32Type semantic.Type) semantic.Type {
	if storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		return &semantic.IDType{Tag: storeType, Storage: u32Type}
	}
	return builtinStoreResultRefType(storeType)
}
