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

func (s *functionState) prepareSafeIndexFallback(expr *ast.IndexExpr, indexValue C.LLVMValueRef) (C.LLVMValueRef, func() (C.LLVMValueRef, semantic.Type, error), error) {
	objectType := s.exprType(expr.Object)
	if objectType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for safe index operand")
	}
	if storeType, ok := packedStoreOperandType(objectType); ok {
		storeValue, _, err := s.emitPackedStoreValueFromExpr(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitPackedStoreCountValue(storeValue, storeType, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		ops := &packedStoreOps{s: s, storeValue: storeValue, storeType: storeType}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			return ops.storeValueAt(indexValue, "safe.index.value")
		}, nil
	}
	switch t := objectType.(type) {
	case *semantic.ArrayType:
		arrayPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.safeIndexArrayCountValue(t)
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			return s.loadArrayIndexValue(arrayPtr, t, indexValue, "safe.index.value")
		}, nil
	case *semantic.DArrayType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.ViewType:
		containerPtr, _, err := s.emitAddressOrTemp(expr.Object)
		if err != nil {
			return nil, nil, err
		}
		countValue, err := s.emitContainerCountValue(containerPtr, t, "safe.index.count")
		if err != nil {
			return nil, nil, err
		}
		return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
			ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, t, t.Elem, indexValue)
			if err != nil {
				return nil, nil, err
			}
			value, err := s.loadValue(ptr, elemType, "safe.index.value")
			return value, elemType, err
		}, nil
	case *semantic.RefType:
		switch elem := t.Elem.(type) {
		case *semantic.ArrayType:
			arrayPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.safeIndexArrayCountValue(elem)
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				return s.loadArrayIndexValue(arrayPtr, elem, indexValue, "safe.index.value")
			}, nil
		case *semantic.DArrayType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		case *semantic.ViewType:
			containerPtr, _, err := s.emitExpr(expr.Object, objectType)
			if err != nil {
				return nil, nil, err
			}
			countValue, err := s.emitContainerCountValue(containerPtr, elem, "safe.index.count")
			if err != nil {
				return nil, nil, err
			}
			return countValue, func() (C.LLVMValueRef, semantic.Type, error) {
				ptr, elemType, err := s.emitRuntimeIndexedAddress(containerPtr, elem, elem.Elem, indexValue)
				if err != nil {
					return nil, nil, err
				}
				value, err := s.loadValue(ptr, elemType, "safe.index.value")
				return value, elemType, err
			}, nil
		}
	}
	return nil, nil, fmt.Errorf("safe index fallback is not implemented for %s", objectType.String())
}
func (s *functionState) safeIndexArrayCountValue(arrayType *semantic.ArrayType) (C.LLVMValueRef, error) {
	if arrayType == nil || !arrayType.HasConstSize || arrayType.ConstSize < 0 {
		return nil, fmt.Errorf("safe index fallback requires a concrete array bound")
	}
	usizeLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	return C.LLVMConstInt(usizeLLVMType, C.ulonglong(arrayType.ConstSize), 0), nil
}
func (s *functionState) loadArrayIndexValue(arrayPtr C.LLVMValueRef, arrayType *semantic.ArrayType, indexValue C.LLVMValueRef, name string) (C.LLVMValueRef, semantic.Type, error) {
	arrayLLVMType, err := s.g.lowerType(arrayType)
	if err != nil {
		return nil, nil, err
	}
	zero := C.LLVMConstInt(C.LLVMInt32TypeInContext(s.g.context), 0, 0)
	indices := []C.LLVMValueRef{zero, indexValue}
	ptr := C.LLVMBuildGEP2(s.builder, arrayLLVMType, arrayPtr, llvmValueSlicePtr(indices), C.unsigned(len(indices)), cStringFree(name+".ptr"))
	value, err := s.loadValue(ptr, arrayType.Elem, name)
	return value, arrayType.Elem, err
}
func (s *functionState) emitContainerCountValue(containerPtr C.LLVMValueRef, containerType semantic.Type, name string) (C.LLVMValueRef, error) {
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, err
	}
	countPtr := C.LLVMBuildStructGEP2(s.builder, containerLLVMType, containerPtr, 1, cStringFree(name+".ptr"))
	usizeType := s.g.result.NamedTypes["usize"]
	return s.loadValue(countPtr, usizeType, name)
}
func (s *functionState) emitChunksExactIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, bool, error) {
	chunkType, ok := semantic.ChunksExactViewItemType(s.exprType(expr.Object))
	if !ok || chunkType == nil {
		return nil, nil, false, nil
	}
	objectType, ok := s.exprType(expr.Object).(*semantic.GenericInstanceType)
	if !ok || objectType == nil {
		return nil, nil, true, fmt.Errorf("chunks_exact index expects a carrier value")
	}
	objectValue, _, err := s.emitExpr(expr.Object, objectType)
	if err != nil {
		return nil, nil, true, err
	}
	usizeType := s.g.result.NamedTypes["usize"]
	indexValue, _, err := s.emitExpr(expr.Index, usizeType)
	if err != nil {
		return nil, nil, true, err
	}
	sourceValue := C.LLVMBuildExtractValue(s.builder, objectValue, 0, cStringFree("chunks.source"))
	chunkSizeValue := C.LLVMBuildExtractValue(s.builder, objectValue, 1, cStringFree("chunks.chunk_size"))
	startValue := C.LLVMBuildMul(s.builder, indexValue, chunkSizeValue, cStringFree("chunks.start"))
	endValue := C.LLVMBuildAdd(s.builder, startValue, chunkSizeValue, cStringFree("chunks.end"))
	value, err := s.emitArenaViewSliceValue(sourceValue, chunkType, startValue, endValue, "chunks.item")
	if err != nil {
		return nil, nil, true, err
	}
	return value, chunkType, true, nil
}
func (s *functionState) emitRuntimeStringIndexExpr(expr *ast.IndexExpr, helperName string, operandType semantic.Type) (C.LLVMValueRef, semantic.Type, error) {
	stringValue, _, err := s.emitExpr(expr.Object, operandType)
	if err != nil {
		return nil, nil, err
	}
	indexType := s.g.result.NamedTypes["i64"]
	resultType := s.g.result.NamedTypes["char"]
	indexValue, _, err := s.emitExpr(expr.Index, indexType)
	if err != nil {
		return nil, nil, err
	}
	helperType := &semantic.FuncType{
		Name:   helperName,
		Params: []semantic.Type{operandType, indexType},
		Return: resultType,
	}
	callee, err := s.g.ensureFunctionDeclared(helperName, helperType)
	if err != nil {
		return nil, nil, err
	}
	llvmFnType, err := s.g.lowerFunctionType(helperType)
	if err != nil {
		return nil, nil, err
	}
	args := []C.LLVMValueRef{stringValue, indexValue}
	call := s.buildCall(llvmFnType, callee, args, "stridx")
	return call, resultType, nil
}
func semanticStringArrayType(t semantic.Type) (*semantic.ArrayType, bool) {
	if arrayType, ok := t.(*semantic.ArrayType); ok {
		if arrayType.SurfaceName == "str" || arrayType.SurfaceName == "string" {
			return arrayType, true
		}
		return nil, false
	}
	ref, ok := t.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	arrayType, ok := ref.Elem.(*semantic.ArrayType)
	if !ok || (arrayType.SurfaceName != "str" && arrayType.SurfaceName != "string") {
		return nil, false
	}
	return arrayType, true
}
func (s *functionState) emitStaticStringIndexExpr(expr *ast.IndexExpr) (C.LLVMValueRef, semantic.Type, error) {
	ptr, _, err := s.emitIndexAddress(expr, false)
	if err != nil {
		return nil, nil, err
	}
	byteType := s.g.result.NamedTypes["u8"]
	loaded, err := s.loadValue(ptr, byteType, "str.byte")
	if err != nil {
		return nil, nil, err
	}
	resultType := s.g.result.NamedTypes["char"]
	llvmResultType, err := s.g.lowerType(resultType)
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMBuildZExt(s.builder, loaded, llvmResultType, cStringFree("stridx.zext")), resultType, nil
}
func runtimeStringIndexedOperand(t semantic.Type) (string, semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return "ctx_string_index", t, true
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return "ctx_string_view_index", t, true
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return "", nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return "", nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return "ctx_string_index", ref.Elem, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return "ctx_string_view_index", ref.Elem, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return "ctx_string_view_index", ref.Elem, true
	}
	return "", nil, false
}
func cstrSyntheticFieldType(t semantic.Type, fieldName string) (semantic.Type, bool) {
	if fieldName != "len" {
		return nil, false
	}
	if _, ok := cstrFieldOperandType(t); !ok {
		return nil, false
	}
	return &semantic.BuiltinType{Name: "i64"}, true
}
func cstrFieldOperandType(t semantic.Type) (semantic.Type, bool) {
	if _, ok := t.(*semantic.DStrType); ok {
		return t, true
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return nil, false
	}
	if ref.State != semantic.RefStateNonNull {
		return nil, false
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return ref.Elem, true
	}
	return nil, false
}

type runtimeSliceInfo struct {
	helperName  string
	operandType semantic.Type
	resultType  semantic.Type
	indexType   semantic.Type
	useAddress  bool
}

func runtimeSliceOperandInfo(objectType semantic.Type, resultType semantic.Type) (runtimeSliceInfo, bool) {
	i64Type := &semantic.BuiltinType{Name: "i64"}
	usizeType := &semantic.BuiltinType{Name: "usize"}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem, SurfaceName: "view"},
			indexType:   usizeType,
		}, true
	}
	if view, ok := objectType.(*semantic.ViewType); ok {
		return runtimeSliceInfo{
			helperName:  "arena_da_view_slice",
			operandType: objectType,
			resultType:  &semantic.ViewType{Elem: view.Elem},
			indexType:   usizeType,
		}, true
	}
	if _, ok := objectType.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := objectType.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := objectType.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: objectType, resultType: resultType, indexType: i64Type}, true
	}
	ref, ok := objectType.(*semantic.RefType)
	if !ok || ref.State != semantic.RefStateNonNull {
		return runtimeSliceInfo{}, false
	}
	if view, ok := ref.Elem.(*semantic.ViewType); ok {
		return runtimeSliceInfo{helperName: "arena_da_view_slice", operandType: ref.Elem, resultType: &semantic.ViewType{Elem: view.Elem, SurfaceName: "view"}, indexType: usizeType}, true
	}
	if _, ok := ref.Elem.(*semantic.DStrType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	if st, ok := ref.Elem.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeSliceInfo{helperName: "ctx_string_view_slice", operandType: ref.Elem, resultType: resultType, indexType: i64Type}, true
	}
	return runtimeSliceInfo{}, false
}
func runtimeStringCompareInfo(leftType semantic.Type, rightType semantic.Type) (string, semantic.Type, semantic.Type, bool, bool) {
	leftKind := classifyRuntimeStringCompareKind(leftType)
	rightKind := classifyRuntimeStringCompareKind(rightType)
	if leftKind == runtimeStringCompareNone || rightKind == runtimeStringCompareNone {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareRaw && rightKind == runtimeStringCompareRaw {
		return "", nil, nil, false, false
	}
	if leftKind == runtimeStringCompareView && rightKind == runtimeStringCompareView {
		return "ctx_string_views_eq", leftType, rightType, false, true
	}
	if leftKind == runtimeStringCompareView {
		return "ctx_string_view_eq", leftType, rightType, false, true
	}
	if rightKind == runtimeStringCompareView {
		return "ctx_string_view_eq", rightType, leftType, true, true
	}
	return "ctx_streq", leftType, rightType, false, true
}

type runtimeStringCompareKind int

const (
	runtimeStringCompareNone runtimeStringCompareKind = iota
	runtimeStringCompareDStr
	runtimeStringCompareView
	runtimeStringCompareRaw
)

func classifyRuntimeStringCompareKind(t semantic.Type) runtimeStringCompareKind {
	if t == nil {
		return runtimeStringCompareNone
	}
	if _, ok := t.(*semantic.DStrType); ok {
		return runtimeStringCompareDStr
	}
	if _, ok := t.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	if st, ok := t.(*semantic.StructType); ok && st.Name == "StringView" {
		return runtimeStringCompareView
	}
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return runtimeStringCompareNone
	}
	if builtin, ok := ref.Elem.(*semantic.BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringCompareRaw
	}
	if _, ok := ref.Elem.(*semantic.SViewType); ok {
		return runtimeStringCompareView
	}
	return runtimeStringCompareNone
}
func (s *functionState) emitCastExpr(expr *ast.CastExpr) (C.LLVMValueRef, semantic.Type, error) {
	// A postfix-shorthand cast `recv.Name()` whose target was not a type is lowered
	// by the analyzer to the call `Name(recv)`; emit that call instead.
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.PostfixShorthandCalls != nil {
		if call, ok := s.g.result.PostfixShorthandCalls[expr]; ok && call != nil {
			return s.emitExpr(call, s.exprType(expr))
		}
	}
	if s != nil && s.g != nil && s.g.result != nil && s.g.result.CastHooks != nil {
		if sym, ok := s.g.result.CastHooks[expr]; ok && sym != nil {
			fnType, ok := sym.Type.(*semantic.FuncType)
			if !ok || fnType == nil || len(fnType.Params) == 0 {
				return nil, nil, fmt.Errorf("invalid semantic cast hook for %T", expr)
			}
			args, err := s.emitCastHookArgs(fmt.Sprintf("%T", expr), expr.Operand, fnType)
			if err != nil {
				return nil, nil, err
			}
			callee, err := s.g.ensureFunctionDeclared(sym.Name, fnType)
			if err != nil {
				return nil, nil, err
			}
			llvmFnType, err := s.g.lowerFunctionType(fnType)
			if err != nil {
				return nil, nil, err
			}
			call := s.buildCall(llvmFnType, callee, args, "casthook")
			return call, fnType.Return, nil
		}
	}
	targetType := s.exprType(expr)
	if targetType == nil {
		return nil, nil, fmt.Errorf("missing semantic type for cast target")
	}
	operandExpected := semantic.Type(nil)
	if _, ok := expr.Operand.(*ast.ZeroedLit); ok {
		operandExpected = targetType
	}
	value, actualType, err := s.emitExpr(expr.Operand, operandExpected)
	if err != nil {
		return nil, nil, err
	}
	coerced, err := s.coerceValue(value, actualType, targetType)
	if err != nil {
		return nil, nil, err
	}
	return coerced, targetType, nil
}
func (s *functionState) emitSizeofExpr(expr *ast.SizeofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	size, err := s.sizeOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(size), 0), s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitAlignofExpr(expr *ast.AlignofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	alignment, err := s.g.abiAlignmentOfType(t)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(alignment), 0), s.g.result.NamedTypes["usize"], nil
}

func (s *functionState) emitOffsetofExpr(expr *ast.OffsetofExpr) (C.LLVMValueRef, semantic.Type, error) {
	t, err := s.resolveTypeExpr(expr.Type)
	if err != nil {
		return nil, nil, err
	}
	_, fieldIndex, containerType, _, err := s.g.fieldInfo(t, expr.Field)
	if err != nil {
		return nil, nil, err
	}
	containerLLVMType, err := s.g.lowerType(containerType)
	if err != nil {
		return nil, nil, err
	}
	offset, err := s.g.abiOffsetOfLLVMElement(containerLLVMType, fieldIndex)
	if err != nil {
		return nil, nil, err
	}
	usizeType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return nil, nil, err
	}
	return C.LLVMConstInt(usizeType, C.ulonglong(offset), 0), s.g.result.NamedTypes["usize"], nil
}
