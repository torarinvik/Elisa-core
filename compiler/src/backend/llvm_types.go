//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void llcontextAddAlwaysInlineAttr(LLVMContextRef Ctx, LLVMValueRef Fn) {
	unsigned Kind = LLVMGetEnumAttributeKindForName("alwaysinline", 12);
	if (Kind == 0) {
		return;
	}
	LLVMAttributeRef Attr = LLVMCreateEnumAttribute(Ctx, Kind, 0);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
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
)

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
	key := noteTypeKey(t)
	if g.noteTypeDone[key] || g.noteTypeInProgress[key] {
		return nil
	}
	g.noteTypeInProgress[key] = true
	defer delete(g.noteTypeInProgress, key)
	var err error
	switch tt := t.(type) {
	case *semantic.InvalidType, *semantic.NeverType, *semantic.NullType, *semantic.BuiltinType, *semantic.TypeParamType, *semantic.DStrType, *semantic.ErrorSetType:
		err = nil
	case *semantic.PackedEnumStoreType:
		_, err = g.lowerPackedEnumStoreType(tt)
	case *semantic.ErrorUnionType:
		if err = g.noteType(tt.Value); err != nil {
			break
		}
		err = g.noteType(tt.Errors)
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
		if len(tt.TypeParams) == 0 {
			_, err = g.ensureStructBody(tt.Name, tt)
			break
		}
		_, err = g.ensureNamedStructType(tt.Name)
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

func noteTypeKey(t semantic.Type) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%s", t, t.String())
}

func (g *llvmGenerator) ensureFunctionDeclared(name string, fn *semantic.FuncType) (C.LLVMValueRef, error) {
	if value, ok := g.functions[name]; ok {
		return value, nil
	}
	value, err := g.addFunction(name, fn)
	if err != nil {
		return nil, err
	}
	g.functions[name] = value
	return value, nil
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

func (g *llvmGenerator) setDefinedFunctionLinkage(name string, value C.LLVMValueRef) {
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
	if linkage == C.LLVMLinkage(C.LLVMPrivateLinkage) {
		g.addAlwaysInlineAttribute(value)
	}
}

func (g *llvmGenerator) addAlwaysInlineAttribute(fn C.LLVMValueRef) {
	if g == nil || g.context == nil || fn == nil {
		return
	}
	C.llcontextAddAlwaysInlineAttr(g.context, fn)
}

func (g *llvmGenerator) lookupIntrinsic(name string, fn *semantic.FuncType) (C.uint, []C.LLVMTypeRef, bool, error) {
	nameC := cString(name)
	defer C.free(unsafe.Pointer(nameC))
	intrinsicID := C.LLVMLookupIntrinsicID(nameC, C.size_t(len(name)))
	if intrinsicID == 0 {
		return 0, nil, false, nil
	}
	paramTypes := make([]C.LLVMTypeRef, 0, len(fn.Params))
	for _, param := range fn.Params {
		paramType, err := g.lowerType(param)
		if err != nil {
			return 0, nil, false, err
		}
		paramTypes = append(paramTypes, paramType)
	}
	return intrinsicID, paramTypes, true, nil
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
	_ = external
	return value, nil
}

func (g *llvmGenerator) lowerFunctionType(fn *semantic.FuncType) (C.LLVMTypeRef, error) {
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
	return C.LLVMFunctionType(returnType, llvmTypeSlicePtr(params), C.unsigned(len(params)), boolToLLVMBool(fn.Variadic)), nil
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
	case *semantic.TypeParamType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.RefType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.PackedEnumStoreType:
		return g.lowerPackedEnumStoreType(tt)
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
		if len(tt.TypeParams) == 0 {
			return g.ensureStructBody(tt.Name, tt)
		}
		return nil, fmt.Errorf("cannot lower generic struct %s without concrete type arguments", tt.Name)
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
	switch g.packedEnumABI {
	case packedEnumABIRowHandle:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case packedEnumABIWordHandle:
		return g.lowerBuiltin("uintptr")
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedEnumABI)
	}
}

func (g *llvmGenerator) lowerPackedEnumStoreType(storeType *semantic.PackedEnumStoreType) (C.LLVMTypeRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	switch g.packedEnumABI {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		return g.ensurePackedEnumStoreCarrierType(storeType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedEnumABI)
	}
}

func (g *llvmGenerator) ensurePackedEnumStorageType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum storage type")
	}
	switch g.packedEnumABI {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		return g.ensurePackedEnumRowType(enumType.Name, enumType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedEnumABI)
	}
}

func (g *llvmGenerator) packedEnumPayloadFieldIndex(enumType *semantic.EnumType) (int, error) {
	if enumType == nil || !enumType.Packed {
		return 0, fmt.Errorf("missing packed enum payload metadata")
	}
	switch g.packedEnumABI {
	case packedEnumABIRowHandle:
		fallthrough
	case packedEnumABIWordHandle:
		payloadIndex := 1
		if enumType.Decl != nil {
			payloadIndex += len(enumType.Decl.Common)
		}
		return payloadIndex, nil
	default:
		return 0, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedEnumABI)
	}
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
	fields := []C.LLVMTypeRef{C.LLVMPointerTypeInContext(g.context, 0), usizeType}
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
	fields := make([]C.LLVMTypeRef, 0, len(variant.Payload))
	for _, payload := range variant.Payload {
		fieldType, err := g.lowerType(payload)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	return C.LLVMStructType(llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
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
	if len(base.TypeParams) != len(inst.Args) {
		return nil, fmt.Errorf("generic instance %s has %d args, expected %d", inst.Name, len(inst.Args), len(base.TypeParams))
	}
	subst := make(map[string]semantic.Type, len(base.TypeParams))
	for i, param := range base.TypeParams {
		subst[param] = inst.Args[i]
	}
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
	case *semantic.ErrorUnionType:
		return &semantic.ErrorUnionType{Value: substituteType(tt.Value, subst), Errors: tt.Errors}
	case *semantic.RefType:
		return &semantic.RefType{Elem: substituteType(tt.Elem, subst), State: tt.State, Storage: tt.Storage, ExplicitStorage: tt.ExplicitStorage}
	case *semantic.ArrayType:
		return &semantic.ArrayType{Elem: substituteType(tt.Elem, subst), Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, SurfaceName: tt.SurfaceName}
	case *semantic.DArrayType:
		return &semantic.DArrayType{Elem: substituteType(tt.Elem, subst), Shape: tt.Shape, SurfaceName: tt.SurfaceName}
	case *semantic.ViewType:
		return &semantic.ViewType{Elem: substituteType(tt.Elem, subst), Begin: tt.Begin, End: tt.End}
	case *semantic.DArrayViewType:
		return &semantic.DArrayViewType{Elem: substituteType(tt.Elem, subst), Begin: tt.Begin, End: tt.End, SurfaceName: tt.SurfaceName}
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
	case *semantic.FuncType:
		params := make([]semantic.Type, 0, len(tt.Params))
		for _, param := range tt.Params {
			params = append(params, substituteType(param, subst))
		}
		return &semantic.FuncType{
			Name:                   tt.Name,
			TypeParams:             append([]string(nil), tt.TypeParams...),
			ShapeParams:            append([]string(nil), tt.ShapeParams...),
			FreshReturnShapeParams: append([]string(nil), tt.FreshReturnShapeParams...),
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
