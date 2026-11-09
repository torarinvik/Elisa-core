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
	"unsafe"
)

func llvmValueIsZeroConstant(value C.LLVMValueRef) bool {
	return value != nil && C.elisacoreLLVMIsZeroValue(value) != 0
}

const largeZeroStoreMemsetThresholdBytes = 128

func (s *functionState) storeValue(ptr C.LLVMValueRef, value C.LLVMValueRef, typ semantic.Type, name string) error {
	if ptr == nil || value == nil {
		return fmt.Errorf("cannot store nil LLVM value")
	}
	if typ != nil && llvmValueIsZeroConstant(value) {
		sizeBytes, err := s.g.abiSizeOfType(typ)
		if err != nil {
			return err
		}
		if sizeBytes >= largeZeroStoreMemsetThresholdBytes {
			return s.emitZeroMemset(ptr, sizeBytes, name)
		}
	}
	// Large aggregate copy: when `value` is a load of the aggregate from a source
	// pointer, lower the copy as a memcpy (and drop the now-dead element-wise
	// load) instead of materializing and storing a giant first-class aggregate
	// value. Without this, copying multi-KB structs (e.g. the shader IR's
	// inst/block/program types, which embed large fixed arrays) makes llc -O0
	// emit thousands of element-wise loads/stores and dominates build time. Elisa
	// aggregates are POD, so memcpy is semantically equivalent to load+store.
	if typ != nil && C.LLVMIsALoadInst(value) != nil {
		if sizeBytes, err := s.g.abiSizeOfType(typ); err == nil && sizeBytes >= largeAggregateCopyMemcpyThresholdBytes {
			if srcPtr := C.LLVMGetOperand(value, 0); srcPtr != nil {
				if memcpyErr := s.emitMemcpy(ptr, srcPtr, sizeBytes, name); memcpyErr == nil {
					// Drop the dead element-wise load if nothing else uses it.
					if C.LLVMGetFirstUse(value) == nil {
						C.LLVMInstructionEraseFromParent(value)
					}
					return nil
				}
			}
		}
	}
	valueType := C.LLVMTypeOf(value)
	if C.LLVMGetTypeKind(valueType) == C.LLVMVoidTypeKind {
		return fmt.Errorf("storeValue %q: cannot store an LLVM void value as semantic type %s", name, typ)
	}
	ptrType := C.LLVMTypeOf(ptr)
	if C.LLVMGetTypeKind(ptrType) != C.LLVMPointerTypeKind {
		ptrText := disposeLLVMMessage(C.LLVMPrintTypeToString(ptrType), "<unknown>")
		return fmt.Errorf("storeValue %q: destination has non-pointer LLVM type %s (semantic type %s)", name, ptrText, typ)
	}
	store := C.LLVMBuildStore(s.builder, value, ptr)
	s.tagDarrayElementAccess(store, ptr)
	return nil
}

// largeAggregateCopyMemcpyThresholdBytes is the size at/above which an aggregate
// copy-from-pointer is lowered via memcpy rather than a first-class load+store.
const largeAggregateCopyMemcpyThresholdBytes = 256

func (s *functionState) emitMemcpy(dst C.LLVMValueRef, src C.LLVMValueRef, sizeBytes uint64, name string) error {
	if sizeBytes == 0 {
		return nil
	}
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	usizeType := s.g.namedType("usize")
	if voidType == nil || usizeType == nil {
		return fmt.Errorf("missing builtin types for memcpy lowering")
	}
	memcpyType := &semantic.FuncType{Name: "memcpy", Params: []semantic.Type{voidRefType, voidRefType, usizeType}, Return: voidRefType}
	memcpyCallee, err := s.g.ensureFunctionDeclared("memcpy", memcpyType)
	if err != nil {
		return err
	}
	memcpyLLVMType, err := s.g.lowerFunctionType(memcpyType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	callName := "store.copy.memcpy"
	if name != "" {
		callName = name + ".memcpy"
	}
	_ = s.buildCall(memcpyLLVMType, memcpyCallee, []C.LLVMValueRef{dst, src, sizeValue}, callName)
	return nil
}

func (s *functionState) emitZeroMemset(ptr C.LLVMValueRef, sizeBytes uint64, name string) error {
	if sizeBytes == 0 {
		return nil
	}
	voidType := s.g.namedType("void")
	voidRefType := &semantic.RefType{Elem: voidType, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true}
	memsetValueType := s.g.namedType("i32")
	usizeType := s.g.namedType("usize")
	if voidType == nil || memsetValueType == nil || usizeType == nil {
		return fmt.Errorf("missing builtin types for memset lowering")
	}
	memsetType := &semantic.FuncType{Name: "memset", Params: []semantic.Type{voidRefType, memsetValueType, usizeType}, Return: voidRefType}
	memsetCallee, err := s.g.ensureFunctionDeclared("memset", memsetType)
	if err != nil {
		return err
	}
	memsetLLVMType, err := s.g.lowerFunctionType(memsetType)
	if err != nil {
		return err
	}
	usizeLLVMType, err := s.g.lowerBuiltin("usize")
	if err != nil {
		return err
	}
	intLLVMType, err := s.g.lowerBuiltin("i32")
	if err != nil {
		return err
	}
	fillValue := C.LLVMConstInt(intLLVMType, 0, 0)
	sizeValue := C.LLVMConstInt(usizeLLVMType, C.ulonglong(sizeBytes), 0)
	callName := "store.zero.memset"
	if name != "" {
		callName = name + ".memset"
	}
	_ = s.buildCall(memsetLLVMType, memsetCallee, []C.LLVMValueRef{ptr, fillValue, sizeValue}, callName)
	return nil
}

func (s *functionState) addCallSiteEnumAttribute(call C.LLVMValueRef, index C.uint, name string) {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	kind := C.LLVMGetEnumAttributeKindForName(nameC, C.size_t(len(name)))
	if kind == 0 {
		return
	}
	attr := C.LLVMCreateEnumAttribute(s.g.context, kind, 0)
	C.LLVMAddCallSiteAttribute(call, index, attr)
}
func (s *functionState) attachAliasScopeMetadataWithNames(inst C.LLVMValueRef, domainName string, aliasScopeName string, noAliasScopeNames []string) {
	if s == nil || s.g == nil || inst == nil || domainName == "" || aliasScopeName == "" {
		return
	}
	domainNameC := cString(domainName)
	defer C.free(unsafe.Pointer(domainNameC))
	aliasScopeNameC := cString(aliasScopeName)
	defer C.free(unsafe.Pointer(aliasScopeNameC))
	var noAliasScope1C *C.char
	var noAliasScope2C *C.char
	hasNoAliasScope1 := C.int(0)
	hasNoAliasScope2 := C.int(0)
	if len(noAliasScopeNames) != 0 && noAliasScopeNames[0] != "" {
		noAliasScope1C = cString(noAliasScopeNames[0])
		defer C.free(unsafe.Pointer(noAliasScope1C))
		hasNoAliasScope1 = 1
	}
	if len(noAliasScopeNames) > 1 && noAliasScopeNames[1] != "" {
		noAliasScope2C = cString(noAliasScopeNames[1])
		defer C.free(unsafe.Pointer(noAliasScope2C))
		hasNoAliasScope2 = 1
	}
	C.elisa_coreAttachAliasScopeMetadata(inst, s.g.context, domainNameC, aliasScopeNameC, noAliasScope1C, hasNoAliasScope1, noAliasScope2C, hasNoAliasScope2)
}
func callIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	ident, ok := expr.Func.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
func callSpecializedIdent(expr ast.Expr) (*ast.Ident, *ast.SpecializeExpr, bool) {
	if expr == nil {
		return nil, nil, false
	}
	specialize, ok := expr.(*ast.SpecializeExpr)
	if !ok || specialize == nil {
		return nil, nil, false
	}
	ident, ok := specialize.Operand.(*ast.Ident)
	if !ok || ident == nil {
		return nil, nil, false
	}
	return ident, specialize, true
}
func callSpecializedIdentName(expr *ast.CallExpr) string {
	if expr == nil {
		return ""
	}
	if ident, _, ok := callSpecializedIdent(expr.Func); ok {
		return ident.Name
	}
	return ""
}
func denseKeySourceEnumType(t semantic.Type) (*semantic.EnumType, bool) {
	if t == nil {
		return nil, false
	}
	t = semantic.StripAggregateStateType(t)
	if enumType, ok := t.(*semantic.EnumType); ok && enumType != nil && enumType.Packed {
		return enumType, true
	}
	if viewType, ok := t.(*semantic.PackedVariantViewType); ok && viewType != nil && viewType.Enum != nil {
		return viewType.Enum, true
	}
	return nil, false
}
