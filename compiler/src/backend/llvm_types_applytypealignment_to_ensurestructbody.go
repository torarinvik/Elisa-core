//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>

static void elisacoreAddAlwaysInlineAttr(LLVMContextRef Ctx, LLVMValueRef Fn, const char* Name, size_t NameLen) {
	unsigned Kind = LLVMGetEnumAttributeKindForName(Name, NameLen);
	if (Kind == 0) {
		return;
	}
	LLVMAttributeRef Attr = LLVMCreateEnumAttribute(Ctx, Kind, 0);
	LLVMAddAttributeAtIndex(Fn, LLVMAttributeFunctionIndex, Attr);
}

static LLVMTypeRef elisacoreGlobalValueType(LLVMValueRef Value) {
	return LLVMGlobalGetValueType(Value);
}

static void elisacoreSetAlignment(LLVMValueRef Value, unsigned Bytes) {
	LLVMSetAlignment(Value, Bytes);
}

static char* elisacorePrintType(LLVMTypeRef Type) {
	return LLVMPrintTypeToString(Type);
}
*/
import "C"

import (
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (g *llvmGenerator) applyTypeAlignment(value C.LLVMValueRef, t semantic.Type) {
	if g == nil || value == nil {
		return
	}
	alignment, ok := semantic.RequestedAlignment(t)
	if !ok || alignment == 0 {
		return
	}
	C.elisacoreSetAlignment(value, C.uint(alignment))
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
	case *semantic.BitIntType:
		return g.lowerBitInt(tt.Width), nil
	case *semantic.IDType:
		return g.lowerType(tt.Storage)
	case *semantic.ConstEnumType:
		return g.lowerType(tt.Storage)
	case *semantic.ErrorSetType:
		return g.lowerBuiltin("u32")
	case *semantic.AssociatedTypeProjection:
		resolved, err := g.resolveAssociatedTypeProjection(tt)
		if err != nil {
			return nil, err
		}
		return g.lowerType(resolved)
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
	case *semantic.TupleType:
		fields := make([]C.LLVMTypeRef, 0, len(tt.Fields))
		for _, field := range tt.Fields {
			fieldType, err := g.lowerType(field.Type)
			if err != nil {
				return nil, err
			}
			fields = append(fields, fieldType)
		}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.BitGroupType:
		return g.lowerBitInt(tt.BackingWidth), nil
	case *semantic.TypeParamType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.RefType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.PackedEnumStoreType:
		return g.lowerPackedEnumStoreType(tt)
	case *semantic.TreeStoreType:
		return g.lowerTreeStoreType(tt)
	case *semantic.PackedVariantViewType:
		return g.ensurePackedVariantViewCarrierType(tt)
	case *semantic.TreeVariantViewType:
		family, ok := treeNodeHandleFamily(tt)
		if !ok || family == nil {
			return nil, fmt.Errorf("missing tree variant view metadata")
		}
		if tt.Category != nil && treeCategoryLayoutPlan(tt.Category).isCategoryUnion() {
			return g.ensureTreeDenseHandleCarrierType()
		}
		return g.ensureTreeHandleCarrierType(family)
	case *semantic.TreeNodeType:
		if tt == nil || tt.Family == nil {
			return nil, fmt.Errorf("missing tree node metadata")
		}
		if treeFamilyLayoutPlan(tt.Family).isCategoryUnion() {
			return g.ensureTreeDenseHandleCarrierType()
		}
		return g.ensureTreeHandleCarrierType(tt.Family)
	case *semantic.TreeType:
		return nil, fmt.Errorf("tree family %s is not a runtime value type", tt.Name)
	case *semantic.TreeCategoryType:
		return g.lowerTreeCategoryType(tt)
	case *semantic.TreeBlockType:
		if tt == nil || tt.Family == nil {
			return nil, fmt.Errorf("missing tree block metadata")
		}
		return g.ensureTreeHandleCarrierType(tt.Family)
	case *semantic.TreeStructType:
		if tt == nil || tt.Family == nil {
			return nil, fmt.Errorf("missing tree struct metadata")
		}
		return g.ensureTreeHandleCarrierType(tt.Family)
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
	case *semantic.StoreRowsViewType:
		storePtrType := C.LLVMPointerTypeInContext(g.context, 0)
		fields := []C.LLVMTypeRef{storePtrType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.StoreRowViewType:
		usizeType, err := g.lowerType(g.result.NamedTypes["usize"])
		if err != nil {
			return nil, err
		}
		storePtrType := C.LLVMPointerTypeInContext(g.context, 0)
		fields := []C.LLVMTypeRef{storePtrType, usizeType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
	case *semantic.DStrType:
		return C.LLVMPointerTypeInContext(g.context, 0), nil
	case *semantic.DictType:
		base, ok := g.result.NamedTypes["DynDict"]
		if !ok {
			return nil, fmt.Errorf("missing runtime struct DynDict")
		}
		return g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Key, tt.Value}})
	case *semantic.DictEntryType:
		dictRefType, err := g.lowerType(&semantic.RefType{Elem: tt.Dict, Mutable: true, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny, ExplicitStorage: true})
		if err != nil {
			return nil, err
		}
		keyType, err := g.lowerType(tt.Dict.Key)
		if err != nil {
			return nil, err
		}
		valueRefType, err := g.lowerType(&semantic.RefType{Elem: tt.Dict.Value, Mutable: true, State: semantic.RefStateNullable, Storage: semantic.RefStorageAny, ExplicitStorage: true})
		if err != nil {
			return nil, err
		}
		fields := []C.LLVMTypeRef{dictRefType, keyType, valueRefType}
		return C.LLVMStructTypeInContext(g.context, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0), nil
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

func (g *llvmGenerator) lowerBitInt(width int) C.LLVMTypeRef {
	if width < 1 {
		width = 1
	}
	if width > 64 {
		width = 64
	}
	return C.LLVMIntTypeInContext(g.context, C.unsigned(width))
}
func (g *llvmGenerator) lowerPackedEnumType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum type")
	}
	switch g.packedModeForEnum(enumType) {
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
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse:
		return g.ensurePackedEnumStoreCarrierType(storeType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedLoweringForStore(storeType))
	}
}
func (g *llvmGenerator) lowerTreeStoreType(storeType *semantic.TreeStoreType) (C.LLVMTypeRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing tree store type")
	}
	name := treeStoreCarrierName(storeType)
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	fields := []C.LLVMTypeRef{
		C.LLVMPointerTypeInContext(g.context, 0),
		C.LLVMPointerTypeInContext(g.context, 0),
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func treeStoreCarrierName(storeType *semantic.TreeStoreType) string {
	if storeType == nil {
		return "TreeStore"
	}
	if storeType.Family != nil && storeType.Family.Name != "" {
		return sanitizeIdentifier(storeType.Family.Name) + "__TreeStore"
	}
	return sanitizeIdentifier(storeType.Name)
}
func (g *llvmGenerator) ensurePackedEnumStorageType(enumType *semantic.EnumType) (C.LLVMTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum storage type")
	}
	switch g.packedModeForEnum(enumType) {
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
