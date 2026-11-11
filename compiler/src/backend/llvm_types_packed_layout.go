//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
	"unsafe"
)

func (g *llvmGenerator) lowerPackedEnumType(enumType *semantic.EnumType) (llvmIRTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum type")
	}
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		// docs/82: the opaque handle's width is the ROOT's `layout(handle: uN)` dial (a sealed
		// hierarchy shares one store and one handle shape; default u32).
		switch enumType.Root().ResolvedIndexWidthBits() {
		case 8:
			return g.lowerBuiltin("u8")
		case 16:
			return g.lowerBuiltin("u16")
		case 64:
			return g.lowerBuiltin("u64")
		default:
			return g.lowerBuiltin("u32")
		}
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedModeForEnum(enumType))
	}
}
func (g *llvmGenerator) lowerPackedEnumStoreType(storeType *semantic.PackedEnumStoreType) (llvmIRTypeRef, error) {
	if storeType == nil {
		return nil, fmt.Errorf("missing packed enum store type")
	}
	switch g.packedLoweringForStore(storeType) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		return g.ensurePackedEnumStoreCarrierType(storeType)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedLoweringForStore(storeType))
	}
}

func (g *llvmGenerator) ensurePackedEnumStorageType(enumType *semantic.EnumType) (llvmIRTypeRef, error) {
	if enumType == nil || !enumType.Packed {
		return nil, fmt.Errorf("missing packed enum storage type")
	}
	// docs/77: a sealed hierarchy shares ONE record type (the root's union), keyed by the root name,
	// so all members allocate identically-shaped nodes into the single per-root store.
	root := enumType.Root()
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		return g.ensurePackedEnumRowType(root.Name, root)
	default:
		return nil, fmt.Errorf("unsupported packed enum ABI mode %d", g.packedModeForEnum(enumType))
	}
}
func (g *llvmGenerator) ensurePackedVariantViewCarrierType(viewType *semantic.PackedVariantViewType) (llvmIRTypeRef, error) {
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
	fields := []llvmIRTypeRef{handleType}
	// docs/77: a sealed hierarchy shares ONE store per root, so the carrier embeds the
	// ROOT's store type (the store value flowing in IS the root store).
	if root := viewType.Enum.Root(); root.StoreType != nil {
		storeType, err := g.lowerPackedEnumStoreType(root.StoreType)
		if err != nil {
			return nil, err
		}
		fields = append(fields, storeType)
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) packedEnumPayloadFieldIndex(enumType *semantic.EnumType) (int, error) {
	if enumType == nil || !enumType.Packed {
		return 0, fmt.Errorf("missing packed enum payload metadata")
	}
	switch g.packedModeForEnum(enumType) {
	case packedEnumABIIndexSOA, packedEnumABIVariantSparse, packedEnumABIAoS:
		// docs/77: payload sits after tag + the ROOT's inline common fields (the shared record layout).
		inlineCommonCount, err := g.packedEnumInlineCommonFieldCount(enumType.Root())
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

// packedFieldUsesSideTable resolves a common field's placement. Commons are per-node metadata that
// hot paths (match/dispatch) never touch, so the unannotated Default is the COLD side table whenever
// the ABI's handle can index a parallel array; `handle: ptr` (and any non-dense mode) has no second
// array to index, so Default falls back to inline-in-row there. @storage(inline|side_table) overrides.
func (g *llvmGenerator) packedFieldUsesSideTable(enumType *semantic.EnumType, field semantic.Field) bool {
	switch field.PackedStorage {
	case semantic.PackedFieldStorageSideTable:
		return true
	case semantic.PackedFieldStorageInline:
		return false
	default:
		return g.packedEnumSupportsSideTableCommonFields(enumType)
	}
}
func (g *llvmGenerator) packedEnumSupportsSideTableCommonFields(enumType *semantic.EnumType) bool {
	if enumType == nil {
		return false
	}
	return packedModeUsesDenseIndexHandle(g.packedModeForEnum(enumType))
}
func (g *llvmGenerator) packedEnumInlineCommonFieldCount(enumType *semantic.EnumType) (int, error) {
	if enumType == nil {
		return 0, nil
	}
	// docs/77: common(...) lives on the hierarchy root; a child enum's own Decl.Common is empty, so
	// resolve against the root or this miscounts to 0 for every promoted hierarchy.
	enumType = enumType.Root()
	if enumType.Decl == nil {
		return 0, nil
	}
	count := 0
	for _, fieldDecl := range enumType.Decl.Common {
		field, ok := enumType.Common[fieldDecl.Name]
		if !ok {
			return 0, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
		}
		if !g.packedFieldUsesSideTable(enumType, field) {
			count++
		}
	}
	return count, nil
}
func (g *llvmGenerator) packedEnumCommonSideTableWordCount(enumType *semantic.EnumType) (uint64, error) {
	if enumType == nil {
		return 0, nil
	}
	// docs/77: common(...) lives on the hierarchy root; resolve against it so a child enum (whose
	// own Decl.Common is empty) reports the same side-word count as the constructor/read paths.
	enumType = enumType.Root()
	if enumType.Decl == nil {
		return 0, nil
	}
	if !g.packedEnumSupportsSideTableCommonFields(enumType) {
		for _, fieldDecl := range enumType.Decl.Common {
			field, ok := enumType.Common[fieldDecl.Name]
			if !ok {
				return 0, fmt.Errorf("missing packed enum common field %s.%s", enumType.Name, fieldDecl.Name)
			}
			if g.packedFieldUsesSideTable(enumType, field) {
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
		if !g.packedFieldUsesSideTable(enumType, field) {
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
	if enumType == nil {
		return nil, fmt.Errorf("missing packed enum common field layout metadata")
	}
	// docs/77: common(...) fields live on the hierarchy root and are laid out in the single root record;
	// resolve a refinement's common field against the root so offsets match the shared record.
	enumType = enumType.Root()
	if !enumType.Packed || enumType.Decl == nil {
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
		storedInline := !g.packedFieldUsesSideTable(enumType, field)
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
func (g *llvmGenerator) ensurePackedEnumStoreCarrierType(storeType *semantic.PackedEnumStoreType) (llvmIRTypeRef, error) {
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
	fields := []llvmIRTypeRef{llvmPointerTypeInContext(g.context, 0), usizeType, llvmPointerTypeInContext(g.context, 0)}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
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
func (g *llvmGenerator) lowerBuiltin(name string) (llvmIRTypeRef, error) {
	switch name {
	case "void":
		return llvmVoidTypeInContext(g.context), nil
	case "bool":
		return llvmInt1TypeInContext(g.context), nil
	case "char":
		return llvmInt64TypeInContext(g.context), nil
	case "i8", "u8":
		return llvmInt8TypeInContext(g.context), nil
	case "i16", "u16":
		return llvmInt16TypeInContext(g.context), nil
	case "i32", "u32":
		return llvmInt32TypeInContext(g.context), nil
	case "i64", "u64":
		return llvmInt64TypeInContext(g.context), nil
	case "f32":
		return llvmFloatTypeInContext(g.context), nil
	case "f64":
		return llvmDoubleTypeInContext(g.context), nil
	case "int", "isize", "usize", "uintptr":
		if g.wordBits == 32 {
			return llvmInt32TypeInContext(g.context), nil
		}
		return llvmInt64TypeInContext(g.context), nil
	default:
		return nil, fmt.Errorf("unsupported builtin type %q", name)
	}
}
func (g *llvmGenerator) lookupStructType(name string) (*semantic.StructType, bool) {
	t, ok := g.lookupNamedType(name)
	if !ok {
		return nil, false
	}
	st, ok := t.(*semantic.StructType)
	return st, ok
}
func (g *llvmGenerator) ensureNamedStructType(name string) (llvmIRTypeRef, error) {
	if ty, ok := g.structTypes[name]; ok {
		return ty, nil
	}
	nameC := cString(name)
	defer llvmFree(unsafe.Pointer(nameC))
	ty := llvmStructCreateNamed(g.context, nameC)
	g.structTypes[name] = ty
	return ty, nil
}
func (g *llvmGenerator) ensureStructBody(name string, st *semantic.StructType) (llvmIRTypeRef, error) {
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] || st == nil || st.Decl == nil {
		return ty, nil
	}
	fields := make([]llvmIRTypeRef, 0, len(st.Decl.Fields))
	for _, fieldDecl := range st.Decl.Fields {
		field, ok := st.Fields[fieldDecl.Name]
		if !ok {
			return nil, fmt.Errorf("missing semantic field %s.%s", name, fieldDecl.Name)
		}
		// Skip phantom fields (e.g., typestate __typestate field) from the runtime layout.
		// Phantom fields are compile-time only and have zero layout impact (docs/111 S0).
		if field.Phantom {
			continue
		}
		fieldType, err := g.lowerType(field.Type)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fieldType)
	}
	packed := llvmIRBool(0)
	if st.PackedLayout {
		g.hasPackedRecords = true
		packed = llvmIRBool(1)
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), packed)
	g.structBodies[name] = true
	return ty, nil
}

// Apply once before optimization, while the lowered GEP type path is intact.
// LLVM can subsequently strengthen alignment if it proves a particular access
// aligned. This covers direct packed-record addresses, including nested fields;
// it does not change the language's contracts for escaped scalar references.
func (g *llvmGenerator) alignPackedRecordAccesses() {
	if g.hasPackedRecords {
		llvmAlignPackedRecordAccesses(g.module)
	}
}
