//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
)

func (g *llvmGenerator) ensureRuntimeDynArray(elem semantic.Type) (llvmIRTypeRef, error) {
	name, err := g.runtimeDynArrayName(elem)
	if err != nil {
		return nil, err
	}
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
	fields := []llvmIRTypeRef{
		llvmPointerTypeInContext(g.context, 0),
		countType,
		countType,
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) ensureRuntimeDynArrayView() (llvmIRTypeRef, error) {
	return g.ensureRuntimeSizedStruct("DynArrayView", 2)
}
func (g *llvmGenerator) ensureRuntimeSizedStruct(name string, fieldCount int) (llvmIRTypeRef, error) {
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
	fields := make([]llvmIRTypeRef, 0, fieldCount)
	fields = append(fields, llvmPointerTypeInContext(g.context, 0))
	for i := 1; i < fieldCount; i++ {
		fields = append(fields, countType)
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) ensureErrorUnionType(unionType *semantic.ErrorUnionType) (llvmIRTypeRef, error) {
	if unionType == nil || unionType.Errors == nil {
		return nil, fmt.Errorf("missing error union metadata")
	}
	name := "ErrUnion__" + sanitizeIdentifier(unionType.Errors.String()) + "__" + llvmTypeSymbolName(errorUnionLayoutValue(unionType.Value))
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
	// A value-carrying error union is an internal descriptor, not an inline copy of
	// its payload.  The payload lives in function-owned storage and the descriptor
	// carries its address.  This keeps `T error[E]` cheap and bounded even when T is
	// a very large enum/struct, while the function ABI continues to return the error
	// code plus a caller-provided payload out-pointer.
	if _, err := g.lowerType(unionType.Value); err != nil {
		return nil, err
	}
	fields := []llvmIRTypeRef{errType, llvmPointerTypeInContext(g.context, 0)}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

// errorUnionLayoutValue drops a reference's mutability: `mutable T& error[E]` and `T& error[E]`
// have one layout, so they must name one LLVM type rather than two identical named structs.
func errorUnionLayoutValue(t semantic.Type) semantic.Type {
	ref, ok := t.(*semantic.RefType)
	if !ok || ref == nil || (!ref.Mutable && !ref.Linear) {
		return t
	}
	cloned := *ref
	cloned.Mutable = false
	cloned.Linear = false
	return &cloned
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

func (g *llvmGenerator) ensureErrorSetType(errorSet *semantic.ErrorSetType) (llvmIRTypeRef, error) {
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
	fields := []llvmIRTypeRef{codeType}
	for _, tag := range errorSet.Tags {
		for _, payloadType := range errorSet.PayloadForTag(tag) {
			loweredPayload, err := g.lowerType(payloadType)
			if err != nil {
				return nil, err
			}
			fields = append(fields, loweredPayload)
		}
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}

func (g *llvmGenerator) ensureOptionalType(optionalType *semantic.OptionalType) (llvmIRTypeRef, error) {
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
	fields := []llvmIRTypeRef{tagType, valueType}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), 0)
	g.structBodies[name] = true
	return ty, nil
}
func (g *llvmGenerator) ensureGenericInstanceStruct(inst *semantic.GenericInstanceType) (llvmIRTypeRef, error) {
	qualifiedBase := inst.Name
	if base, ok := inst.Base.(*semantic.StructType); ok && base.Namespace != "" {
		qualifiedBase = base.Namespace + "::" + base.Name
	}
	name, err := g.mangleGenericLayoutType(qualifiedBase, inst.Args)
	if err != nil {
		return nil, err
	}
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
	fields := make([]llvmIRTypeRef, 0, len(base.Decl.Fields))
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
	packed := llvmIRBool(0)
	if base.PackedLayout {
		packed = llvmIRBool(1)
		g.hasPackedRecords = true
	}
	llvmStructSetBody(ty, llvmTypeSlicePtr(fields), llvmIRUnsigned(len(fields)), packed)
	g.structBodies[name] = true
	return ty, nil
}
