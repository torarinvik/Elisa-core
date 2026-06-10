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
)

// enumLayoutLeaves returns the variants that determine an enum's physical layout: for a sealed
// hierarchy node (docs/77) that is the union of all descendant leaves (so every member shares the
// root's representation); for a plain enum it is the enum's own variants.
func enumLayoutLeaves(enum *semantic.EnumType) []*semantic.EnumVariant {
	if enum == nil {
		return nil
	}
	if enum.Parent == nil && len(enum.Children) == 0 {
		return enum.Variants // plain enum
	}
	var out []*semantic.EnumVariant
	var visit func(e *semantic.EnumType)
	visit = func(e *semantic.EnumType) {
		out = append(out, e.Variants...)
		for _, child := range e.Children {
			visit(child)
		}
	}
	visit(enum.Root()) // any hierarchy member shares the root's leaf set
	return out
}

func enumLeavesAreTagOnly(leaves []*semantic.EnumVariant) bool {
	for _, variant := range leaves {
		if variant != nil && len(variant.Payload) > 0 {
			return false
		}
	}
	return true
}

func (g *llvmGenerator) ensureEnumBody(name string, enum *semantic.EnumType) (C.LLVMTypeRef, error) {
	leaves := enumLayoutLeaves(enum)
	if enumLeavesAreTagOnly(leaves) {
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
	for _, variant := range leaves {
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
	for _, variant := range enumLayoutLeaves(enum) { // docs/77: union over the whole hierarchy's leaves
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
	for _, variant := range enumLayoutLeaves(enum) {
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
	// Hierarchy-aware (docs/77): a member is tag-only iff the whole hierarchy is, so all members
	// agree on representation (bare u32 vs {tag, union}).
	return enumLeavesAreTagOnly(enumLayoutLeaves(enum))
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
	return g.ensureRuntimeSizedStruct("DynArrayView", 2)
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
	name := "ErrUnion__" + sanitizeIdentifier(unionType.Errors.String()) + "__" + llvmTypeSymbolName(unionType.Value)
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

func llvmTypeSymbolName(t semantic.Type) string {
	switch tt := t.(type) {
	case *semantic.OptionalType:
		if tt == nil || tt.Value == nil {
			return "Optional__invalid"
		}
		return "Optional__" + llvmTypeSymbolName(tt.Value)
	default:
		return sanitizeIdentifier(t.String())
	}
}

func (g *llvmGenerator) ensureErrorSetType(errorSet *semantic.ErrorSetType) (C.LLVMTypeRef, error) {
	if errorSet == nil {
		return nil, fmt.Errorf("missing error set metadata")
	}
	name := "ErrSet__" + sanitizeIdentifier(errorSet.String())
	ty, err := g.ensureNamedStructType(name)
	if err != nil {
		return nil, err
	}
	if g.structBodies[name] {
		return ty, nil
	}
	codeType, err := g.lowerBuiltin("u32")
	if err != nil {
		return nil, err
	}
	fields := []C.LLVMTypeRef{codeType}
	for _, tag := range errorSet.Tags {
		for _, payloadType := range errorSet.PayloadForTag(tag) {
			loweredPayload, err := g.lowerType(payloadType)
			if err != nil {
				return nil, err
			}
			fields = append(fields, loweredPayload)
		}
	}
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
	for _, arg := range inst.Args {
		if _, unresolved := arg.(*semantic.ConstParamType); unresolved {
			g.structBodies[name] = true
			return ty, nil
		}
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
		fieldType, err := g.lowerType(substituteType(field.Type, subst, g.result.StaticImpls))
		if err != nil {
			return nil, fmt.Errorf("lowering field %s.%s for %s: %w", base.Name, fieldDecl.Name, name, err)
		}
		fields = append(fields, fieldType)
	}
	C.LLVMStructSetBody(ty, llvmTypeSlicePtr(fields), C.unsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
