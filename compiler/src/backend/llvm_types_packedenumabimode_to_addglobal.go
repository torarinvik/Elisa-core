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
	"llcontext/src/semantic"
	"strings"
	"unsafe"
)

type packedEnumABIMode int

const (
	packedEnumABIIndexSOA packedEnumABIMode = iota
	packedEnumABIVariantSparse
)

type PackedEnumABI string

const (
	PackedEnumABIDenseFixed    PackedEnumABI = "dense-fixed"
	PackedEnumABIIndexSOA      PackedEnumABI = "index-soa"
	PackedEnumABIVariantSparse PackedEnumABI = "variant-sparse"
)

func ParsePackedEnumABI(value string) (PackedEnumABI, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(PackedEnumABIDenseFixed), "dense_fixed", "densefixed", "fixed_dense", "fixed-dense":
		return PackedEnumABIDenseFixed, nil
	case string(PackedEnumABIIndexSOA), "index", "soa", "indexsoa", "index_soa":
		return PackedEnumABIIndexSOA, nil
	case string(PackedEnumABIVariantSparse), "variant", "variantsparse", "variant_sparse", "sparse":
		return PackedEnumABIVariantSparse, nil
	default:
		return "", fmt.Errorf("unsupported packed enum ABI %q (expected %q, %q, or %q)", value, PackedEnumABIDenseFixed, PackedEnumABIIndexSOA, PackedEnumABIVariantSparse)
	}
}
func (abi PackedEnumABI) mode() (packedEnumABIMode, error) {
	normalized, err := ParsePackedEnumABI(string(abi))
	if err != nil {
		return packedEnumABIIndexSOA, err
	}
	switch normalized {
	case PackedEnumABIDenseFixed:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIIndexSOA:
		return packedEnumABIIndexSOA, nil
	case PackedEnumABIVariantSparse:
		return packedEnumABIVariantSparse, nil
	default:
		return packedEnumABIIndexSOA, fmt.Errorf("unsupported packed enum ABI %q", abi)
	}
}
func packedModeUsesDenseIndexHandle(mode packedEnumABIMode) bool {
	return mode == packedEnumABIIndexSOA || mode == packedEnumABIVariantSparse
}
func packedModeUsesDirectWordReads(mode packedEnumABIMode) bool {
	return packedModeUsesDenseIndexHandle(mode)
}
func packedModeName(mode packedEnumABIMode) string {
	switch mode {
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
func (g *llvmGenerator) resolveAssociatedTypeProjection(proj *semantic.AssociatedTypeProjection) (semantic.Type, error) {
	if proj == nil {
		return nil, fmt.Errorf("missing associated type projection")
	}
	if g == nil || g.result == nil {
		return nil, fmt.Errorf("missing semantic result for associated type projection %s", proj.String())
	}
	resolved, ok := semantic.ResolveAssociatedTypeProjection(proj, g.result.StaticImpls)
	if !ok || resolved == nil {
		return nil, fmt.Errorf("unresolved associated type projection %s", proj.String())
	}
	return resolved, nil
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
	case *semantic.AssociatedTypeProjection:
		resolved, resolveErr := g.resolveAssociatedTypeProjection(tt)
		if resolveErr != nil {
			err = resolveErr
			break
		}
		err = g.noteType(resolved)
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
	case *semantic.TreeNodeType:
		if tt == nil || tt.Family == nil {
			err = fmt.Errorf("missing tree family node metadata")
			break
		}
		err = g.noteType(tt.Family)
	case *semantic.TreeStoreType:
		if tt != nil && tt.Family != nil {
			if err = g.noteType(tt.Family); err != nil {
				break
			}
		}
		_, err = g.lowerTreeStoreType(tt)
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
		if err == nil && tt.NodeType != nil {
			_, err = g.ensureTreeHandleCarrierType(tt)
		}
		if err == nil && tt.StoreType != nil {
			_, err = g.ensureTreeStoreStateType(tt)
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
		if err == nil && tt.Family != nil {
			_, err = g.ensureTreeHandleCarrierType(tt.Family)
		}
	case *semantic.TreeBlockType:
		if tt == nil {
			err = fmt.Errorf("missing tree block metadata")
			break
		}
		for _, fieldDecl := range semantic.TreeBlockFieldDeclsWithCommon(tt) {
			field, ok := semantic.TreeExactFieldInfo(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
		if err == nil {
			_, err = g.ensureTreeExactTableType(tt)
		}
	case *semantic.TreeStructType:
		if tt == nil {
			err = fmt.Errorf("missing tree struct metadata")
			break
		}
		for _, fieldDecl := range semantic.TreeStructFieldDeclsWithCommon(tt) {
			field, ok := semantic.TreeExactFieldInfo(tt, fieldDecl.Name)
			if !ok {
				continue
			}
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
		if err == nil {
			_, err = g.ensureTreeExactTableType(tt)
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
	case *semantic.TupleType:
		for _, field := range tt.Fields {
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
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
		_, err = g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Key, tt.Value}})
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
	case *semantic.IDType:
		err = g.noteType(tt.Storage)
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
	if g.isDirectlyExportedFunction(name) || g.isDefaultNativeRuntimeSupportExport(name) {
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

func (g *llvmGenerator) isDefaultNativeRuntimeSupportExport(name string) bool {
	if g == nil || g.result == nil || g.result.File == nil {
		return false
	}
	if !strings.HasSuffix(g.result.File.Filename, "native_runtime_support.llcontext") {
		return false
	}
	switch name {
	case "new_region",
		"free_region",
		"arena_alloc",
		"arena_realloc",
		"arena_memcpy",
		"arena_memeq",
		"arena_strdup",
		"arena_memdup",
		"arena_snapshot",
		"arena_reset",
		"arena_rewind",
		"arena_free",
		"arena_trim",
		"alloc_perm",
		"alloc_scratch",
		"reset_scratch",
		"ctx_strlen",
		"ctx_streq",
		"ctx_string_slice_eq",
		"ctx_string_slices_eq",
		"ctx_string_index",
		"ctx_string_slice",
		"ctx_string_view",
		"ctx_string_view_len",
		"ctx_string_view_slice",
		"ctx_string_view_prefix",
		"ctx_string_view_suffix",
		"ctx_string_view_index",
		"ctx_string_view_eq",
		"ctx_string_views_eq",
		"ctx_string_from_view",
		"ctx_llvm_codegen_fatal":
		return true
	default:
		return false
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
