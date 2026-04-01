//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void llcontextAddAlwaysInlineAttr(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen) {
	unsigned Kind = LLVMGetEnumAttributeKindForName(Name, NameLen);
	if (Kind == 0) {
		return;
	}
	LLVMAttributeRef Attr = LLVMCreateEnumAttribute(Ctx, Kind, 0);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
}

static LLVMTypeRef llcontextGlobalValueType(LLVMValueRef Value) {
	return LLVMGlobalGetValueType(Value);
}

static void llcontextSetAlignment(LLVMValueRef Value, unsigned Bytes) {
	LLVMSetAlignment(Value, Bytes);
}

static char* llcontextPrintType(LLVMTypeRef Type) {
	return LLVMPrintTypeToString(Type);
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

type packedEnumABIMode int

const (
	packedEnumABIRowHandle packedEnumABIMode = iota
	packedEnumABIWordHandle
	packedEnumABIIndexSOA
	packedEnumABIVariantSparse
)

type PackedEnumABI string

const (
	PackedEnumABIRowHandle     PackedEnumABI = "row-handle"
	PackedEnumABIWordHandle    PackedEnumABI = "word-handle"
	PackedEnumABIDenseFixed    PackedEnumABI = "dense-fixed"
	PackedEnumABIIndexSOA      PackedEnumABI = "index-soa"
	PackedEnumABIVariantSparse PackedEnumABI = "variant-sparse"
)

func ParsePackedEnumABI(value string) (PackedEnumABI, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(PackedEnumABIRowHandle), "row", "rowhandle", "row_handle":
		return PackedEnumABIRowHandle, nil
	case string(PackedEnumABIWordHandle), "word", "wordhandle", "word_handle":
		return PackedEnumABIWordHandle, nil
	case string(PackedEnumABIDenseFixed), "dense_fixed", "densefixed", "fixed_dense", "fixed-dense":
		return PackedEnumABIDenseFixed, nil
	case string(PackedEnumABIIndexSOA), "index", "soa", "indexsoa", "index_soa":
		return PackedEnumABIIndexSOA, nil
	case string(PackedEnumABIVariantSparse), "variant", "variantsparse", "variant_sparse", "sparse":
		return PackedEnumABIVariantSparse, nil
	default:
		return "", fmt.Errorf("unsupported packed enum ABI %q (expected %q, %q, %q, %q, or %q)", value, PackedEnumABIRowHandle, PackedEnumABIWordHandle, PackedEnumABIDenseFixed, PackedEnumABIIndexSOA, PackedEnumABIVariantSparse)
	}
}

func (abi PackedEnumABI) mode() (packedEnumABIMode, error) {
	normalized, err := ParsePackedEnumABI(string(abi))
	if err != nil {
		return packedEnumABIRowHandle, err
	}
	switch normalized {
	case PackedEnumABIRowHandle:
		return packedEnumABIRowHandle, nil
	case PackedEnumABIWordHandle:
		return packedEnumABIWordHandle, nil
	case PackedEnumABIDenseFixed:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIIndexSOA:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIVariantSparse:
		return packedEnumABIVariantSparse, nil
	default:
		return packedEnumABIRowHandle, fmt.Errorf("unsupported packed enum ABI %q", abi)
	}
}

func packedModeUsesDenseIndexHandle(mode packedEnumABIMode) bool {
	return mode == packedEnumABIIndexSOA || mode == packedEnumABIVariantSparse
}

func packedModeUsesDirectWordReads(mode packedEnumABIMode) bool {
	return mode == packedEnumABIWordHandle || packedModeUsesDenseIndexHandle(mode)
}

func packedModeName(mode packedEnumABIMode) string {
	switch mode {
	case packedEnumABIRowHandle:
		return string(PackedEnumABIRowHandle)
	case packedEnumABIWordHandle:
		return string(PackedEnumABIWordHandle)
	case packedEnumABIIndexSOA:
		return string(PackedEnumABIIndexSOA)
	case packedEnumABIVariantSparse:
		return string(PackedEnumABIVariantSparse)
	default:
		return fmt.Sprintf("mode-%d", mode)
	}
}

func isVoidRefLikeType(t semantic.Type) bool {
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return false
	}
	builtin, ok := ref.Elem.(*semantic.BuiltinType)
	return ok && builtin.Name == "void"
}

func nonVoidErrorUnion(t semantic.Type) (*semantic.ErrorUnionType, bool) {
	unionType, ok := t.(*semantic.ErrorUnionType)
	if !ok || unionType == nil || isVoidType(unionType.Value) {
		return nil, false
	}
	return unionType, true
}

func (g *llvmGenerator) noteType(t semantic.Type) error {
	if t == nil {
		return nil
	}
	key := noteTypeKeyFor(t)
	if g.noteTypeDone[key] || g.noteTypeInProgress[key] {
		return nil
	}
	g.noteTypeInProgress[key] = true
	defer delete(g.noteTypeInProgress, key)
	var err error
	switch tt := t.(type) {
	case *semantic.InvalidType, *semantic.NeverType, *semantic.NullType, *semantic.BuiltinType, *semantic.TypeParamType, *semantic.DStrType, *semantic.ErrorSetType:
		err = nil
	case *semantic.ConstEnumType:
		err = g.noteType(tt.Storage)
	case *semantic.PackedVariantViewType:
		if err = g.noteType(tt.Enum); err != nil {
			break
		}
		if tt.Enum != nil && tt.Enum.StoreType != nil {
			if err = g.noteType(tt.Enum.StoreType); err != nil {
				break
			}
		}
		_, err = g.ensurePackedVariantViewCarrierType(tt)
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			err = fmt.Errorf("missing tree variant view metadata")
			break
		}
		err = g.noteType(tt.Category)
	case *semantic.TreeType:
		if tt != nil && tt.Decl != nil {
			for _, fieldDecl := range tt.Decl.Common {
				field, ok := tt.Common[fieldDecl.Name]
				if !ok {
					continue
				}
				if err = g.noteType(field.Type); err != nil {
					break
				}
			}
		}
		if err != nil {
			break
		}
		for _, memberType := range tt.MemberTypes {
			if err = g.noteType(memberType); err != nil {
				break
			}
		}
	case *semantic.TreeCategoryType:
		if tt == nil {
			err = fmt.Errorf("missing tree category metadata")
			break
		}
		for _, fieldDecl := range treeCommonFieldDecls(tt) {
			field, ok := tt.Common[fieldDecl.Name]
			if !ok {
				continue
			}
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if err = g.noteType(payload); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			_, err = g.ensureTreeCategoryBody(tt)
		}
	case *semantic.TreeBlockType:
		if tt == nil {
			err = fmt.Errorf("missing tree block metadata")
			break
		}
		for _, fieldDecl := range treeBlockFieldDecls(tt) {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				continue
			}
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
		if err == nil {
			_, err = g.ensureTreeBlockBody(tt)
		}
	case *semantic.TreeStructType:
		if tt == nil {
			err = fmt.Errorf("missing tree struct metadata")
			break
		}
		for _, fieldDecl := range treeStructFieldDecls(tt) {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				continue
			}
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
		if err == nil {
			_, err = g.ensureTreeStructBody(tt)
		}
	case *semantic.PackedEnumStoreType:
		_, err = g.lowerPackedEnumStoreType(tt)
	case *semantic.ErrorUnionType:
		if err = g.noteType(tt.Value); err != nil {
			break
		}
		err = g.noteType(tt.Errors)
	case *semantic.OptionalType:
		err = g.noteType(tt.Value)
	case *semantic.SViewType:
		if st, ok := g.lookupStructType("StringView"); ok {
			_, err = g.ensureStructBody(st.Name, st)
			break
		}
		err = fmt.Errorf("missing runtime struct StringView")
	case *semantic.RefType:
		err = g.noteType(tt.Elem)
	case *semantic.ArrayType:
		err = g.noteType(tt.Elem)
	case *semantic.DArrayType:
		_, err = g.ensureRuntimeDynArray(tt.Elem)
	case *semantic.ViewType:
		_, err = g.ensureRuntimeDynArrayView()
	case *semantic.DArrayViewType:
		_, err = g.ensureRuntimeDynArrayView()
	case *semantic.DictType:
		if err = g.noteType(tt.Key); err != nil {
			break
		}
		if err = g.noteType(tt.Value); err != nil {
			break
		}
		base, ok := g.result.NamedTypes["DynDict"]
		if !ok {
			err = fmt.Errorf("missing runtime struct DynDict")
			break
		}
		_, err = g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Value}})
	case *semantic.EnumType:
		if tt.Decl != nil {
			for _, fieldDecl := range tt.Decl.Common {
				field, ok := tt.Common[fieldDecl.Name]
				if !ok {
					continue
				}
				if err = g.noteType(field.Type); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if err = g.noteType(payload); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			if tt.Packed {
				_, err = g.ensurePackedEnumStorageType(tt)
			} else {
				_, err = g.ensureEnumBody(tt.Name, tt)
			}
		}
	case *semantic.StructType:
		if len(structGenericParams(tt)) == 0 {
			_, err = g.ensureStructBody(tt.Name, tt)
			break
		}
		_, err = g.ensureNamedStructType(tt.Name)
	case *semantic.AggregateStateType:
		err = g.noteType(tt.Base)
	case *semantic.OpaqueType:
		_, err = g.ensureNamedStructType(tt.Name)
	case *semantic.GenericInstanceType:
		_, err = g.ensureGenericInstanceStruct(tt)
	case *semantic.FuncType:
		for _, param := range tt.Params {
			if err = g.noteType(param); err != nil {
				break
			}
		}
		if err == nil {
			err = g.noteType(tt.Return)
		}
	default:
		err = fmt.Errorf("unsupported semantic type %T", t)
	}
	if err == nil {
		g.noteTypeDone[key] = true
	}
	return err
}

func (g *llvmGenerator) ensureFunctionDeclared(name string, fn *semantic.FuncType) (C.LLVMValueRef, error) {
	if value, ok := g.functions[name]; ok {
		if err := g.ensureDeclaredFunctionType(name, value, fn); err != nil {
			return nil, err
		}
		return value, nil
	}
	value, err := g.addFunction(name, fn)
	if err != nil {
		return nil, err
	}
	g.functions[name] = value
	return value, nil
}

func (g *llvmGenerator) ensureDeclaredFunctionType(name string, value C.LLVMValueRef, fn *semantic.FuncType) error {
	if g == nil || value == nil || fn == nil {
		return nil
	}
	expectedType, err := g.lowerFunctionType(fn)
	if err != nil {
		return err
	}
	actualType := C.llcontextGlobalValueType(value)
	if actualType == expectedType {
		return nil
	}
	actualText := disposeLLVMMessage(C.llcontextPrintType(actualType), "<unknown>")
	expectedText := disposeLLVMMessage(C.llcontextPrintType(expectedType), "<unknown>")
	if actualText == expectedText {
		return nil
	}
	return fmt.Errorf("conflicting LLVM function declaration for %q: existing %s, requested %s", name, actualText, expectedText)
}

func (g *llvmGenerator) ensureGlobalDeclared(name string, t semantic.Type, external bool) (C.LLVMValueRef, error) {
	if value, ok := g.globals[name]; ok {
		return value, nil
	}
	value, err := g.addGlobal(name, t, external)
	if err != nil {
		return nil, err
	}
	g.globals[name] = value
	return value, nil
}

func (g *llvmGenerator) addFunction(name string, fn *semantic.FuncType) (C.LLVMValueRef, error) {
	if intrinsicID, overloadedParamTypes, ok, err := g.lookupIntrinsic(name, fn); err != nil {
		return nil, err
	} else if ok {
		return C.LLVMGetIntrinsicDeclaration(g.module, intrinsicID, llvmTypeSlicePtr(overloadedParamTypes), C.size_t(len(overloadedParamTypes))), nil
	}

	fnType, err := g.lowerFunctionType(fn)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddFunction(g.module, nameC, fnType)
	C.LLVMSetLinkage(value, C.LLVMExternalLinkage)
	return value, nil
}

func (g *llvmGenerator) isDirectlyExportedFunction(name string) bool {
	if g == nil || g.result == nil {
		return false
	}
	for _, exported := range g.result.ExportedFuncs {
		if exported == nil {
			continue
		}
		if exported.PublicName == name && exported.TargetName == name {
			return true
		}
	}
	return false
}

func (g *llvmGenerator) setDefinedFunctionLinkage(name string, value C.LLVMValueRef, fnType *semantic.FuncType) {
	if value == nil {
		return
	}
	linkage := C.LLVMLinkage(C.LLVMExternalLinkage)
	if g.preferPrivateLinkage && !g.isDirectlyExportedFunction(name) {
		linkage = C.LLVMLinkage(C.LLVMPrivateLinkage)
	}
	if g.isDirectlyExportedFunction(name) {
		linkage = C.LLVMLinkage(C.LLVMExternalLinkage)
	}
	C.LLVMSetLinkage(value, linkage)
	explicitInlineMode := false
	if fnType != nil {
		if fnType.HasInlineMode {
			explicitInlineMode = true
			switch fnType.InlineMode {
			case semantic.FuncInlineModeAlways:
				g.addAlwaysInlineAttribute(value)
			case semantic.FuncInlineModeNever:
				g.addNoInlineAttribute(value)
			}
		}
		g.applyFunctionNoRecurseAttributes(value, fnType)
		g.applyFunctionTemperatureAttributes(value, fnType)
	}
	if explicitInlineMode {
		return
	}
	if linkage == C.LLVMLinkage(C.LLVMPrivateLinkage) {
		if g.shouldNeverInlineDefinedFunction(name) {
			g.addNoInlineAttribute(value)
		}
	}
}

func (g *llvmGenerator) applyFunctionNoRecurseAttributes(fn C.LLVMValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil || !fnType.HasNoRecurse {
		return
	}
	g.addNoRecurseAttribute(fn)
}

func (g *llvmGenerator) applyFunctionTemperatureAttributes(fn C.LLVMValueRef, fnType *semantic.FuncType) {
	if g == nil || fn == nil || fnType == nil || !fnType.HasTemperatureMode {
		return
	}
	switch fnType.TemperatureMode {
	case semantic.FuncTemperatureModeHot:
		g.addHotAttribute(fn)
	case semantic.FuncTemperatureModeCold:
		g.addColdAttribute(fn)
	}
}

func (g *llvmGenerator) addAlwaysInlineAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "alwaysinline")
}

func (g *llvmGenerator) addNoInlineAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "noinline")
}

func (g *llvmGenerator) addNoRecurseAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "norecurse")
}

func (g *llvmGenerator) addHotAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "hot")
}

func (g *llvmGenerator) addColdAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	g.addFunctionEnumAttribute(fn, "cold")
}

func (g *llvmGenerator) addFunctionEnumAttribute(fn C.LLVMValueRef, name string) {
	if g == nil || g.context == nil || fn == nil || name == "" {
		return
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	C.llcontextAddAlwaysInlineAttr(g.context, fn, nameC, C.size_t(len(name)))
}

func (g *llvmGenerator) shouldNeverInlineDefinedFunction(name string) bool {
	switch name {
	case "ctx_packed_store_reserve":
		return true
	case "ctx_packed_store_alloc_result", "ctx_packed_store_alloc_fixed_result", "ctx_packed_store_alloc_index_result", "ctx_packed_store_alloc_fixed_index_result":
		return true
	case "ctx_packed_store_alloc_fixed_tagged_result", "ctx_packed_store_alloc_fixed_tagged_index_result":
		return true
	case "ctx_packed_store_alloc_fixed_tagged_variant_sparse_result", "ctx_packed_store_alloc_tagged_variant_sparse_result":
		return true
	default:
		return false
	}
}

func (g *llvmGenerator) lookupIntrinsic(name string, fn *semantic.FuncType) (C.uint, []C.LLVMTypeRef, bool, error) {
	if !strings.HasPrefix(strings.TrimSpace(name), "llvm.") {
		return 0, nil, false, nil
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	intrinsicID := C.LLVMLookupIntrinsicID(nameC, C.size_t(len(name)))
	if intrinsicID == 0 {
		return 0, nil, false, nil
	}
	overloadParams := intrinsicOverloadParams(name, fn)
	paramTypes := make([]C.LLVMTypeRef, 0, len(overloadParams))
	for _, param := range overloadParams {
		paramType, err := g.lowerType(param)
		if err != nil {
			return 0, nil, false, err
		}
		paramTypes = append(paramTypes, paramType)
	}
	return intrinsicID, paramTypes, true, nil
}

func intrinsicOverloadParams(name string, fn *semantic.FuncType) []semantic.Type {
	if fn == nil {
		return nil
	}
	switch name {
	case "memset", "llvm.memset":
		if len(fn.Params) >= 3 {
			return []semantic.Type{fn.Params[0], fn.Params[2]}
		}
	}
	return fn.Params
}

func (g *llvmGenerator) addGlobal(name string, t semantic.Type, external bool) (C.LLVMValueRef, error) {
	globalType, err := g.lowerType(t)
	if err != nil {
		return nil, err
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	value := C.LLVMAddGlobal(g.module, globalType, nameC)
	C.LLVMSetLinkage(value, C.LLVMExternalLinkage)
	g.applyTypeAlignment(value, t)
	_ = external
	return value, nil
}

func (g *llvmGenerator) applyTypeAlignment(value C.LLVMValueRef, t semantic.Type) {
	if g == nil || value == nil {
		return
	}
	alignment, ok := semantic.RequestedAlignment(t)
	if !ok || alignment == 0 {
		return
	}
	C.llcontextSetAlignment(value, C.uint(alignment))
}

func (g *llvmGenerator) lowerFunctionType(fn *semantic.FuncType) (C.LLVMTypeRef, error) {
	if g == nil || fn == nil {
		return nil, fmt.Errorf("missing function type for LLVM lowering")
	}
	if cached, ok := g.functionTypes[fn]; ok && cached != nil {
		return cached, nil
	}
	returnType, err := g.lowerFunctionReturnType(fn.Return)
	if err != nil {
		return nil, err
	}
	params := make([]C.LLVMTypeRef, 0, len(fn.Params))
	if unionType, ok := nonVoidErrorUnion(fn.Return); ok {
		outParamType, err := g.lowerErrorUnionOutParamType(unionType)
		if err != nil {
			return nil, err
		}
		params = append(params, outParamType)
	}
	for _, param := range fn.Params {
		paramType, err := g.lowerType(param)
		if err != nil {
			return nil, err
		}
		params = append(params, paramType)
	}
	lowered := C.LLVMFunctionType(returnType, llvmTypeSlicePtr(params), C.unsigned(len(params)), boolToLLVMBool(fn.Variadic))
	g.functionTypes[fn] = lowered
	return lowered, nil
}

func (g *llvmGenerator) lowerFunctionReturnType(t semantic.Type) (C.LLVMTypeRef, error) {
	if unionType, ok := nonVoidErrorUnion(t); ok {
		return g.lowerType(unionType.Errors)
	}
	return g.lowerType(t)
}

func (g *llvmGenerator) lowerErrorUnionOutParamType(unionType *semantic.ErrorUnionType) (C.LLVMTypeRef, error) {
	if unionType == nil || isVoidType(unionType.Value) {
		return nil, fmt.Errorf("missing value-carrying error union metadata")
	}
	if _, err := g.lowerType(unionType.Value); err != nil {
		return nil, err
	}
	return C.LLVMPointerTypeInContext(g.context, 0), nil
}

func (g *llvmGenerator) lowerType(t semantic.Type) (C.LLVMTypeRef, error) {
	if t == nil {
		return C.LLVMVoidTypeInContext(g.context), nil
	}
	switch tt := t.(type) {
	case *semantic.InvalidType:
		return C.LLVMVoidTypeInContext(g.context), nil
	case *semantic.NeverType:
		return C.LLVMVoidTypeInContext(g.context), nil
	case *semantic.NullType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.BuiltinType:
		return g.lowerBuiltin(tt.Name)
	case *semantic.ConstEnumType:
		return g.lowerType(tt.Storage)
	case *semantic.ErrorSetType:
		return g.lowerBuiltin("u32")
	case *semantic.ErrorUnionType:
		errType, err := g.lowerType(tt.Errors)
		if err != nil {
			return nil, err
		}
		if isVoidType(tt.Value) {
			return errType, nil
		}
		return g.ensureErrorUnionType(tt)
	case *semantic.OptionalType:
		if isVoidType(tt.Value) {
			return nil, fmt.Errorf("optional type %s cannot wrap void", tt.String())
		}
		return g.ensureOptionalType(tt)
	case *semantic.TypeParamType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.RefType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.PackedEnumStoreType:
		return g.lowerPackedEnumStoreType(tt)
	case *semantic.PackedVariantViewType:
		return g.ensurePackedVariantViewCarrierType(tt)
	case *semantic.TreeVariantViewType:
		if tt == nil || tt.Category == nil {
			return nil, fmt.Errorf("missing tree variant view metadata")
		}
		return g.lowerTreeCategoryType(tt.Category)
	case *semantic.TreeType:
		return nil, fmt.Errorf("tree family %s is not a runtime value type", tt.Name)
	case *semantic.TreeCategoryType:
		return g.lowerTreeCategoryType(tt)
	case *semantic.TreeBlockType:
		return g.ensureTreeBlockBody(tt)
	case *semantic.TreeStructType:
		return g.ensureTreeStructBody(tt)
	case *semantic.ArrayType:
		elemType, err := g.lowerType(tt.Elem)
		if err != nil {
			return nil, err
		}
		if tt.HasConstSize {
			return C.LLVMArrayType2(elemType, C.ulonglong(tt.ConstSize)), nil
		}
		return nil, fmt.Errorf("array type %s is missing a compile-time constant size", tt.String())
	case *semantic.DArrayType:
		return g.ensureRuntimeDynArray(tt.Elem)
	case *semantic.ViewType:
		return g.ensureRuntimeDynArrayView()
	case *semantic.DArrayViewType:
		return g.ensureRuntimeDynArrayView()
	case *semantic.DStrType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.DictType:
		base, ok := g.result.NamedTypes["DynDict"]
		if !ok {
			return nil, fmt.Errorf("missing runtime struct DynDict")
		}
		return g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Value}})
	case *semantic.SViewType:
		st, ok := g.lookupStructType("StringView")
		if !ok {
			return nil, fmt.Errorf("missing runtime struct StringView")
		}
		return g.ensureStructBody(st.Name, st)
	case *semantic.EnumType:
		if tt.Packed {
			return g.lowerPackedEnumType(tt)
		}
		return g.ensureEnumBody(tt.Name, tt)
	case *semantic.StructType:
		if len(structGenericParams(tt)) == 0 {
			return g.ensureStructBody(tt.Name, tt)
		}
		return nil, fmt.Errorf("cannot lower generic struct %s without concrete type arguments", tt.Name)
	case *semantic.AggregateStateType:
		return g.lowerType(tt.Base)
	case *semantic.OpaqueType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.GenericInstanceType:
		return g.ensureGenericInstanceStruct(tt)
	case *semantic.FuncType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	default:
		return nil, fmt.Errorf("unsupported semantic type %T", t)
	}
}

func (g *llvmGenerator) lowerPackedEnumType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum type")
	}
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIRowHandle:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case packedEnumABIWordHandle:
		return g.lowerBuiltin("uintptr")
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		return g.lowerBuiltin("u32")
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedModeForEnum(enumType))
	}
}

func (g *llvmGenerator) lowerPackedEnumStoreType(storeType *semantic.PackedEnumStoreType) (C.LLVMTypeRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	switch g.packedLoweringForStore(storeType) {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		fallthrough
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		return g.ensurePackedEnumStoreCarrierType(storeType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedLoweringForStore(storeType))
	}
}

func (g *llvmGenerator) ensurePackedEnumStorageType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum storage type")
	}
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		fallthrough
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		return g.ensurePackedEnumRowType(enumType.Name, enumType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedModeForEnum(enumType))
	}
}

func (g *llvmGenerator) ensurePackedVariantViewCarrierType(viewType *semantic.PackedVariantViewType) (C.LLVMTypeRef, error) {
	if viewType == nil || viewType.Enum == nil || viewType.Variant == nil {
		return nil, fmt.Errorf("missing packedview carrier metadata")
	}
	name := "PackedView__" + sanitizeIdentifier(viewType.Enum.Name) + "__" + sanitizeIdentifier(viewType.Variant.Name)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	handleType, err := g.lowerPackedEnumType(viewType.Enum)
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{handleType}
	if viewType.Enum.StoreType != nil {
		storeType, err := g.lowerPackedEnumStoreType(viewType.Enum.StoreType)
		if err != nil {
			return nil, err
		}
		fields = append(fields, storeType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) packedEnumPayloadFieldIndex(enumType *semantic.EnumType) (int, error) {
	if enumType == nil || !enumType.Packed {
		return 0, fmt.Errorf("missing packed enum payload metadata")
	}
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		fallthrough
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		inlineCommonCount, err := g.packedEnumInlineCommonFieldCount(enumType)
		if err != nil {
			return 0, err
		}
		return 1 + inlineCommonCount, nil
	default:
		return 0, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedModeForEnum(enumType))
	}
}

type packedEnumCommonFieldLayout struct {
	Field          semantic.Field
	DeclarationIdx int
	RowFieldIndex  int
	SideWordOffset uint64
	WordCount      uint64
	StoredInline   bool
}

type packedEnumCommonFieldLayoutCacheKey struct {
	enum  *semantic.EnumType
	field string
}

func packedFieldUsesSideTable(field semantic.Field) bool {
	return field.PackedStorage == semantic.PackedFieldStorageSideTable
}

func (g *llvmGenerator) packedEnumSupportsSideTableCommonFields(enumType *semantic.EnumType) bool {
	if enumType == nil {
		return false
	}
	return packedModeUsesDenseIndexHandle(g.packedModeForEnum(enumType))
}

func (g *llvmGenerator) packedEnumInlineCommonFieldCount(enumType *semantic.EnumType) (int, error) {
	if enumType == nil || enumType.Decl == nil {
		return 0, nil
	}
	count := 0
	for _, fieldDecl := range enumType.Decl.Common {
		field, ok := enumType.Common[fieldDecl.Name]
		if !ok {
			return 0, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
		}
		if !packedFieldUsesSideTable(field) {
			count++
		}
	}
	return count, nil
}

func (g *llvmGenerator) packedEnumCommonSideTableWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil || enumType.Decl == nil {
		return 0, nil
	}
	if !g.packedEnumSupportsSideTableCommonFields(enumType) {
		for _, fieldDecl := range enumType.Decl.Common {
			field, ok := enumType.Common[fieldDecl.Name]
			if !ok {
				return 0, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
			}
			if packedFieldUsesSideTable(field) {
				return 0, fmt.Errorf("packed enum %s common field %s uses @storage(side_table), but packed ABI %q does not support side-tabled common fields", enumType.Name, fieldDecl.Name, packedModeName(g.packedModeForEnum(enumType)))
			}
		}
		return 0, nil
	}
	wordBytes := uint64(g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	total := uint64(0)
	for _, fieldDecl := range enumType.Decl.Common {
		field, ok := enumType.Common[fieldDecl.Name]
		if !ok {
			return 0, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
		}
		if !packedFieldUsesSideTable(field) {
			continue
		}
		sizeBytes, err := g.abiSizeOfType(field.Type)
		if err != nil {
			return 0, err
		}
		if sizeBytes == 0 {
			continue
		}
		total += (sizeBytes + wordBytes - 1) / wordBytes
	}
	return total, nil
}

func (g *llvmGenerator) packedEnumCommonFieldLayout(enumType *semantic.EnumType, fieldName string) (*packedEnumCommonFieldLayout, error) {
	if enumType == nil || !enumType.Packed || enumType.Decl == nil {
		return nil, fmt.Errorf("missing packed enum common field layout metadata")
	}
	cacheKey := packedEnumCommonFieldLayoutCacheKey{enum: enumType, field: fieldName}
	if layout, ok := g.commonFieldLayouts[cacheKey]; ok && layout != nil {
		return layout, nil
	}
	wordBytes := uint64(g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	rowFieldIndex := 1
	sideWordOffset := uint64(0)
	for declarationIdx, fieldDecl := range enumType.Decl.Common {
		field, ok := enumType.Common[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
		}
		storedInline := !packedFieldUsesSideTable(field)
		wordCount := uint64(0)
		if !storedInline {
			if !g.packedEnumSupportsSideTableCommonFields(enumType) {
				return nil, fmt.Errorf("packed enum %s common field %s uses @storage(side_table), but packed ABI %q does not support side-tabled common fields", enumType.Name, fieldDecl.Name, packedModeName(g.packedModeForEnum(enumType)))
			}
			sizeBytes, err := g.abiSizeOfType(field.Type)
			if err != nil {
				return nil, err
			}
			if sizeBytes > 0 {
				wordCount = (sizeBytes + wordBytes - 1) / wordBytes
			}
		}
		if fieldDecl.Name == fieldName {
			layout := &packedEnumCommonFieldLayout{
				Field:          field,
				DeclarationIdx: declarationIdx,
				RowFieldIndex:  -1,
				SideWordOffset: sideWordOffset,
				WordCount:      wordCount,
				StoredInline:   storedInline,
			}
			if storedInline {
				layout.RowFieldIndex = rowFieldIndex
			}
			g.commonFieldLayouts[cacheKey] = layout
			return layout, nil
		}
		if storedInline {
			rowFieldIndex++
		} else {
			sideWordOffset += wordCount
		}
	}
	return nil, fmt.Errorf("packed enum %s has no common field %s", enumType.Name, fieldName)
}

func (g *llvmGenerator) ensurePackedEnumStoreCarrierType(storeType *semantic.PackedEnumStoreType) (C.LLVMTypeRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	name := packedEnumStoreCarrierName(storeType)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	usizeType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{C.LLVMPointerTypeInContext(g.context, 0), usizeType, C.LLVMPointerTypeInContext(g.context, 0)}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func packedEnumStoreCarrierName(storeType *semantic.PackedEnumStoreType) string {
	if storeType == nil {
		return "PackedEnumStore"
	}
	if storeType.Enum != nil && storeType.Enum.Name != "" {
		return sanitizeIdentifier(storeType.Enum.Name) + "__Store"
	}
	return sanitizeIdentifier(storeType.Name)
}

func (g *llvmGenerator) lowerBuiltin(name string) (C.LLVMTypeRef, error) {
	switch name {
	case "void":
		return C.LLVMVoidTypeInContext(g.context), nil
	case "bool":
		return C.LLVMInt1TypeInContext(g.context), nil
	case "char":
		return C.LLVMInt64TypeInContext(g.context), nil
	case "i8", "u8":
		return C.LLVMInt8TypeInContext(g.context), nil
	case "i16", "u16":
		return C.LLVMInt16TypeInContext(g.context), nil
	case "i32", "u32":
		return C.LLVMInt32TypeInContext(g.context), nil
	case "i64", "u64":
		return C.LLVMInt64TypeInContext(g.context), nil
	case "f32":
		return C.LLVMFloatTypeInContext(g.context), nil
	case "f64":
		return C.LLVMDoubleTypeInContext(g.context), nil
	case "int", "isize", "usize", "uintptr":
		if g.wordBits == 32 {
			return C.LLVMInt32TypeInContext(g.context), nil
		}
		return C.LLVMInt64TypeInContext(g.context), nil
	default:
		return nil, fmt.Errorf("unsupported builtin type %q", name)
	}
}

func (g *llvmGenerator) lookupStructType(name string) (*semantic.StructType, bool) {
	t, ok := g.result.NamedTypes[name]
	if !ok {
		return nil, false
	}
	st, ok := t.(*semantic.StructType)
	return st, ok
}

func (g *llvmGenerator) ensureNamedStructType(name string) (C.LLVMTypeRef, error) {
	if ty, ok := g.structTypes[name]; ok {
		return ty, nil
	}
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	ty := C.LLVMStructCreateNamed(g.context, nameC)
	g.structTypes[name] = ty
	return ty, nil
}

func (g *llvmGenerator) ensureStructBody(name string, st *semantic.StructType) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] || st == nil || st.Decl == nil {
		return ty, nil
	}
	fields := make([]C.LLVMTypeRef, 0, len(st.Decl.Fields))
	for _, fieldDecl := range st.Decl.Fields {
		field, ok := st.Fields[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic field %s.%s", name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(field.Type)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureEnumBody(name string, enum *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumIsTagOnly(enum) {
		return g.lowerBuiltin("u32")
	}
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] || enum == nil {
		return ty, nil
	}
	tagType, err := g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	wordType, err := g.lowerBuiltin("uintptr")
	if err != nil {
		return nil, err
	}
	maxSlots := uint64(0)
	for _, variant := range enum.Variants {
		slots, err := g.enumVariantPayloadSlots(variant)
		if err != nil {
			return nil, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	payloadType := C.LLVMArrayType2(wordType, C.ulonglong(maxSlots))
	fields := []C.LLVMTypeRef{tagType, payloadType}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) lowerTreeCategoryType(category *semantic.TreeCategoryType) (C.LLVMTypeRef, error) {
	if category == nil {
		return nil, fmt.Errorf("missing tree category type")
	}
	if _, err := g.ensureTreeCategoryStorageNamedType(category); err != nil {
		return nil, err
	}
	return C.LLVMPointerTypeInContext(g.context, 0), nil
}

func treeCategoryStorageName(category *semantic.TreeCategoryType) string {
	if category == nil {
		return "TreeCategory__Node"
	}
	return sanitizeIdentifier(category.Name) + "__Node"
}

func (g *llvmGenerator) ensureTreeCategoryStorageNamedType(category *semantic.TreeCategoryType) (C.LLVMTypeRef, error) {
	if category == nil {
		return nil, fmt.Errorf("missing tree category storage metadata")
	}
	return g.ensureNamedStructType(treeCategoryStorageName(category))
}

func (g *llvmGenerator) ensureTreeCategoryBody(category *semantic.TreeCategoryType) (C.LLVMTypeRef, error) {
	ty, err := g.ensureTreeCategoryStorageNamedType(category)
	if err != nil {
		return nil, err
	}
	name := treeCategoryStorageName(category)
	if g.structBodies[name] || category == nil {
		return ty, nil
	}
	tagType, err := g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{tagType}
	for _, fieldDecl := range treeCommonFieldDecls(category) {
		field, ok := category.Common[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing tree common field %s.%s", category.Name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(field.Type)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	maxSlots := uint64(0)
	for _, variant := range category.Variants {
		slots, err := g.enumVariantPayloadSlots(variant)
		if err != nil {
			return nil, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	if maxSlots > 0 {
		wordType, err := g.lowerBuiltin("uintptr")
		if err != nil {
			return nil, err
		}
		payloadType := C.LLVMArrayType2(wordType, C.ulonglong(maxSlots))
		fields = append(fields, payloadType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureTreeBlockBody(blockType *semantic.TreeBlockType) (C.LLVMTypeRef, error) {
	if blockType == nil {
		return nil, fmt.Errorf("missing tree block metadata")
	}
	return g.ensureTreeFieldsBody(blockType.Name, treeBlockFieldDecls(blockType), blockType.Fields)
}

func (g *llvmGenerator) ensureTreeStructBody(structType *semantic.TreeStructType) (C.LLVMTypeRef, error) {
	if structType == nil {
		return nil, fmt.Errorf("missing tree struct metadata")
	}
	return g.ensureTreeFieldsBody(structType.Name, treeStructFieldDecls(structType), structType.Fields)
}

func (g *llvmGenerator) ensureTreeFieldsBody(name string, decls []ast.FieldDecl, fieldsMap map[string]semantic.Field) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	fields := make([]C.LLVMTypeRef, 0, len(decls))
	for _, fieldDecl := range decls {
		field, ok := fieldsMap[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic tree field %s.%s", name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(field.Type)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func treeCommonFieldDecls(category *semantic.TreeCategoryType) []ast.FieldDecl {
	if category == nil || category.Family == nil || category.Family.Decl == nil {
		return nil
	}
	return category.Family.Decl.Common
}

func treeBlockFieldDecls(blockType *semantic.TreeBlockType) []ast.FieldDecl {
	if blockType == nil || blockType.Decl == nil {
		return nil
	}
	return blockType.Decl.Fields
}

func treeStructFieldDecls(structType *semantic.TreeStructType) []ast.FieldDecl {
	if structType == nil || structType.Decl == nil {
		return nil
	}
	return structType.Decl.Fields
}

func (g *llvmGenerator) ensurePackedEnumRowType(name string, enum *semantic.EnumType) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] || enum == nil {
		return ty, nil
	}
	tagType, err := g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{tagType}
	if enum.Decl != nil {
		for _, fieldDecl := range enum.Decl.Common {
			field, ok := enum.Common[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing packed enum common field %s.%s", enum.Name, fieldDecl.Name)
			}
			if packedFieldUsesSideTable(field) {
				if !g.packedEnumSupportsSideTableCommonFields(enum) {
					return nil, fmt.Errorf("packed enum %s common field %s uses @storage(side_table), but packed ABI %q does not support side-tabled common fields", enum.Name, fieldDecl.Name, packedModeName(g.packedModeForEnum(enum)))
				}
				continue
			}
			fieldType, err := g.lowerType(field.Type)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fieldType)
		}
	}
	maxSlots := uint64(0)
	for _, variant := range enum.Variants {
		slots, err := g.enumVariantPayloadSlots(variant)
		if err != nil {
			return nil, err
		}
		if slots > maxSlots {
			maxSlots = slots
		}
	}
	if maxSlots > 0 {
		wordType, err := g.lowerBuiltin("uintptr")
		if err != nil {
			return nil, err
		}
		payloadType := C.LLVMArrayType2(wordType, C.ulonglong(maxSlots))
		fields = append(fields, payloadType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) packedEnumCommonPrefixWordCount(enum *semantic.EnumType) (uint64, error) {
	if enum == nil || enum.Decl == nil || len(enum.Decl.Common) == 0 {
		return 0, nil
	}
	rowType, err := g.ensurePackedEnumStorageType(enum)
	if err != nil {
		return 0, err
	}
	hasPayload := false
	for _, variant := range enum.Variants {
		slots, err := g.enumVariantPayloadSlots(variant)
		if err != nil {
			return 0, err
		}
		if slots > 0 {
			hasPayload = true
			break
		}
	}
	prefixBytes := uint64(0)
	if hasPayload {
		payloadIndex, payloadErr := g.packedEnumPayloadFieldIndex(enum)
		if payloadErr != nil {
			return 0, payloadErr
		}
		prefixBytes, err = g.abiOffsetOfLLVMElement(rowType, payloadIndex)
	} else {
		prefixBytes, err = g.abiSizeOfLLVMType(rowType)
	}
	if err != nil {
		return 0, err
	}
	wordType, err := g.lowerBuiltin("uintptr")
	if err != nil {
		return 0, err
	}
	wordBytes, err := g.abiSizeOfLLVMType(wordType)
	if err != nil {
		return 0, err
	}
	if wordBytes == 0 {
		return 0, fmt.Errorf("uintptr ABI size resolved to zero bytes")
	}
	return (prefixBytes + wordBytes - 1) / wordBytes, nil
}

func (g *llvmGenerator) packedEnumConfiguredPrefixWordCount(enum *semantic.EnumType) (uint64, error) {
	if enum == nil || !enum.Packed {
		return 0, nil
	}
	if enum.HasPackedPrefixOverride && enum.PackedPrefixOverride == "common-only" {
		return g.packedEnumCommonPrefixWordCount(enum)
	}
	rowType, err := g.ensurePackedEnumStorageType(enum)
	if err != nil {
		return 0, err
	}
	rowBytes, err := g.abiSizeOfLLVMType(rowType)
	if err != nil {
		return 0, err
	}
	wordType, err := g.lowerBuiltin("uintptr")
	if err != nil {
		return 0, err
	}
	wordBytes, err := g.abiSizeOfLLVMType(wordType)
	if err != nil {
		return 0, err
	}
	if wordBytes == 0 {
		return 0, fmt.Errorf("uintptr ABI size resolved to zero bytes")
	}
	return (rowBytes + wordBytes - 1) / wordBytes, nil
}

func enumIsTagOnly(enum *semantic.EnumType) bool {
	if enum == nil {
		return false
	}
	for _, variant := range enum.Variants {
		if variant != nil && len(variant.Payload) > 0 {
			return false
		}
	}
	return true
}

func (g *llvmGenerator) enumVariantPayloadSlots(variant *semantic.EnumVariant) (uint64, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return 0, nil
	}
	if err := g.ensureTargetMachine(); err != nil {
		return 0, err
	}
	payloadType, err := g.lowerEnumVariantPayloadType(variant)
	if err != nil {
		return 0, err
	}
	sizeBytes, err := g.abiSizeOfLLVMType(payloadType)
	if err != nil {
		return 0, err
	}
	wordBytes := uint64(g.wordBits / 8)
	if wordBytes == 0 {
		wordBytes = 8
	}
	return (sizeBytes + wordBytes - 1) / wordBytes, nil
}

func (g *llvmGenerator) lowerEnumVariantPayloadType(variant *semantic.EnumVariant) (C.LLVMTypeRef, error) {
	if variant == nil || len(variant.Payload) == 0 {
		return C.LLVMVoidTypeInContext(g.context), nil
	}
	if len(variant.Payload) == 1 {
		return g.lowerType(variant.Payload[0])
	}
	if cached := g.packedVariantPayloadTypes[variant]; cached != nil {
		return cached, nil
	}
	fields := make([]C.LLVMTypeRef, 0, len(variant.Payload))
	for _, payload := range variant.Payload {
		fieldType, err := g.lowerType(payload)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	payloadType := C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.packedVariantPayloadTypes[variant] = payloadType
	return payloadType, nil
}

func (g *llvmGenerator) ensureRuntimeDynArray(elem semantic.Type) (C.LLVMTypeRef, error) {
	name := runtimeDynArrayName(elem)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	countType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{
		C.LLVMPointerTypeInContext(g.context, 0),
		countType,
		countType,
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureRuntimeDynArrayView() (C.LLVMTypeRef, error) {
	return g.ensureRuntimeSizedStruct("DynArrayView", 3)
}

func (g *llvmGenerator) ensureRuntimeSizedStruct(name string, fieldCount int) (C.LLVMTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	countType, err := g.lowerBuiltin("usize")
	if err != nil {
		return nil, err
	}
	fields := make([]C.LLVMTypeRef, 0, fieldCount)
	fields = append(fields, C.LLVMPointerTypeInContext(g.context, 0))
	for i := 1; i < fieldCount; i++ {
		fields = append(fields, countType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureErrorUnionType(unionType *semantic.ErrorUnionType) (C.LLVMTypeRef, error) {
	if unionType == nil || unionType.Errors == nil {
		return nil, fmt.Errorf("missing error union metadata")
	}
	name := "ErrUnion__" + sanitizeIdentifier(unionType.Errors.String()) + "__" + sanitizeIdentifier(unionType.Value.String())
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	errType, err := g.lowerType(unionType.Errors)
	if err != nil {
		return nil, err
	}
	valueType, err := g.lowerType(unionType.Value)
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{errType, valueType}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureOptionalType(optionalType *semantic.OptionalType) (C.LLVMTypeRef, error) {
	if optionalType == nil || optionalType.Value == nil {
		return nil, fmt.Errorf("missing optional metadata")
	}
	name := "Optional__" + sanitizeIdentifier(optionalType.Value.String())
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	tagType, err := g.lowerBuiltin("bool")
	if err != nil {
		return nil, err
	}
	valueType, err := g.lowerType(optionalType.Value)
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{tagType, valueType}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureGenericInstanceStruct(inst *semantic.GenericInstanceType) (C.LLVMTypeRef, error) {
	name := mangleGenericType(inst.Name, inst.Args)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	base, ok := inst.Base.(*semantic.StructType)
	if !ok {
		return nil, fmt.Errorf("generic instance %s does not resolve to a struct base", inst.Name)
	}
	params := structGenericParams(base)
	if len(params) != len(inst.Args) {
		return nil, fmt.Errorf("generic instance %s has %d args, expected %d", inst.Name, len(inst.Args), len(params))
	}
	subst := genericBindingsForArgs(params, inst.Args)
	if base.Decl == nil {
		return nil, fmt.Errorf("generic struct %s is missing declaration metadata", base.Name)
	}
	fields := make([]C.LLVMTypeRef, 0, len(base.Decl.Fields))
	for _, fieldDecl := range base.Decl.Fields {
		field, ok := base.Fields[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic field %s.%s", base.Name, fieldDecl.Name)
		}
		fieldType, err := g.lowerType(substituteType(field.Type, subst))
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func substituteType(t semantic.Type, subst map[string]semantic.Type) semantic.Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *semantic.TypeParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RefStorageParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.RefStateParamType:
		if mapped, ok := subst[tt.Name]; ok {
			return mapped
		}
		return t
	case *semantic.ErrorUnionType:
		return &semantic.ErrorUnionType{Value: substituteType(tt.Value, subst), Errors: tt.Errors}
	case *semantic.RefType:
		state := tt.State
		stateParam := tt.StateParam
		if stateParam != "" {
			if mapped, ok := subst[stateParam]; ok {
				switch mapped := mapped.(type) {
				case *semantic.RefStateValueType:
					state = mapped.State
					stateParam = ""
				case *semantic.RefStateParamType:
					stateParam = mapped.Name
				}
			}
		}
		storage := tt.Storage
		storageParam := tt.StorageParam
		if storageParam != "" {
			if mapped, ok := subst[storageParam]; ok {
				switch mapped := mapped.(type) {
				case *semantic.RefStorageValueType:
					storage = mapped.Storage
					storageParam = ""
				case *semantic.RefStorageParamType:
					storageParam = mapped.Name
				}
			}
		}
		return &semantic.RefType{Elem: substituteType(tt.Elem, subst), State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: tt.Region, ExplicitStorage: tt.ExplicitStorage}
	case *semantic.ArrayType:
		return &semantic.ArrayType{Elem: substituteType(tt.Elem, subst), Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayType:
		return &semantic.DArrayType{Elem: substituteType(tt.Elem, subst), Shape: tt.Shape, SurfaceName: tt.SurfaceName}
	case *semantic.ViewType:
		return &semantic.ViewType{Elem: substituteType(tt.Elem, subst), Begin: tt.Begin, End: tt.End}
	case *semantic.DArrayViewType:
		return &semantic.DArrayViewType{Elem: substituteType(tt.Elem, subst), Begin: tt.Begin, End: tt.End, SurfaceName: tt.SurfaceName}
	case *semantic.PackedVariantViewType:
		return tt
	case *semantic.DictType:
		return &semantic.DictType{Key: substituteType(tt.Key, subst), Value: substituteType(tt.Value, subst), SurfaceName: tt.SurfaceName}
	case *semantic.SViewType:
		return &semantic.SViewType{Begin: tt.Begin, End: tt.End}
	case *semantic.GenericInstanceType:
		args := make([]semantic.Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteType(arg, subst))
		}
		return &semantic.GenericInstanceType{Name: tt.Name, Base: substituteType(tt.Base, subst), Args: args}
	case *semantic.AggregateStateType:
		return &semantic.AggregateStateType{Base: substituteType(tt.Base, subst), State: tt.State, States: append([]semantic.RefState(nil), tt.States...)}
	case *semantic.FuncType:
		params := make([]semantic.Type, 0, len(tt.Params))
		for _, param := range tt.Params {
			params = append(params, substituteType(param, subst))
		}
		return &semantic.FuncType{
			Name:                   tt.Name,
			TypeParams:             append([]string(nil), tt.TypeParams...),
			RefStorageParams:       append([]string(nil), tt.RefStorageParams...),
			RefStateParams:         append([]string(nil), tt.RefStateParams...),
			RegionParams:           append([]string(nil), tt.RegionParams...),
			GenericParams:          append([]ast.GenericParam(nil), tt.GenericParams...),
			ShapeParams:            append([]string(nil), tt.ShapeParams...),
			FreshReturnShapeParams: append([]string(nil), tt.FreshReturnShapeParams...),
			InlineMode:             tt.InlineMode,
			HasInlineMode:          tt.HasInlineMode,
			HasNoRecurse:           tt.HasNoRecurse,
			TemperatureMode:        tt.TemperatureMode,
			HasTemperatureMode:     tt.HasTemperatureMode,
			Params:                 params,
			Return:                 substituteType(tt.Return, subst),
			Variadic:               tt.Variadic,
		}
	default:
		return t
	}
}

func runtimeDynArrayName(elem semantic.Type) string {
	return mangleGenericType("DynArray", []semantic.Type{elem})
}

func structGenericParams(base *semantic.StructType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams)+1)
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	if len(base.NamedStateCases) != 0 {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), base.NamedStateCases...), StateOwner: base.Name})
	}
	return params
}

func funcGenericParams(base *semantic.FuncType) []ast.GenericParam {
	if base == nil {
		return nil
	}
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams))
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	return params
}

func genericBindingsForArgs(params []ast.GenericParam, args []semantic.Type) map[string]semantic.Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	bindings := make(map[string]semantic.Type, len(params))
	for i, param := range params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		bindings[param.Name] = args[i]
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func mangleGenericType(base string, args []semantic.Type) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, sanitizeIdentifier(arg.String()))
	}
	if len(parts) == 0 {
		return sanitizeIdentifier(base)
	}
	return sanitizeIdentifier(base) + "__" + strings.Join(parts, "__")
}

func sanitizeIdentifier(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	value := strings.Trim(out.String(), "_")
	if value == "" {
		return "anon"
	}
	return value
}

func boolToLLVMBool(v bool) C.LLVMBool {
	if v {
		return 1
	}
	return 0
}

func cString(s string) *C.char {
	return C.CString(s)
}

func llvmTypeSlicePtr(types []C.LLVMTypeRef) *C.LLVMTypeRef {
	if len(types) == 0 {
		return nil
	}
	return (*C.LLVMTypeRef)(unsafe.Pointer(&types[0]))
}

func declIsExternVar(decl ast.Decl) bool {
	_, ok := decl.(*ast.ExternVarDecl)
	return ok
}

func llvmValueSlicePtr(values []C.LLVMValueRef) *C.LLVMValueRef {
	if len(values) == 0 {
		return nil
	}
	return (*C.LLVMValueRef)(unsafe.Pointer(&values[0]))
}

func llvmBlockSlicePtr(blocks []C.LLVMBasicBlockRef) *C.LLVMBasicBlockRef {
	if len(blocks) == 0 {
		return nil
	}
	return (*C.LLVMBasicBlockRef)(unsafe.Pointer(&blocks[0]))
}

func (g *llvmGenerator) exprType(expr ast.Expr) semantic.Type {
	if g.result == nil || expr == nil {
		return nil
	}
	if t, ok := g.result.ExprTypes[expr]; ok {
		return t
	}
	return nil
}

func (g *llvmGenerator) constValue(name string) (semantic.ConstValue, bool) {
	if g.result == nil {
		return semantic.ConstValue{}, false
	}
	value, ok := g.result.ConstValues[name]
	return value, ok
}
