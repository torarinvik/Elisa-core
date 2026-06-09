//go:build cgo

package backend

/*
#include <stdlib.h>
#include <llvm-c/Core.h>
*/
import "C"

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) exprType(expr ast.Expr) semantic.Type {
	t := s.g.exprType(expr)
	switch n := expr.(type) {
	case *ast.Ident:
		var live semantic.Type
		if binding, ok := s.lookupBinding(n.Name); ok {
			live = binding.typ
		} else if s.g != nil && s.g.result != nil && s.g.result.GlobalScope != nil {
			if sym, ok := s.g.result.GlobalScope.Lookup(n.Name); ok {
				live = sym.Type
			}
		}
		t = preferBackendLiveIdentType(t, live)
	case *ast.ParenExpr:
		return s.exprType(n.Inner)
	}
	if t == nil {
		switch n := expr.(type) {
		case *ast.FieldExpr:
			if fieldType, ok := cstrSyntheticFieldType(s.exprType(n.Object), n.Field); ok {
				t = fieldType
				break
			}
			if viewType, ok := s.exprType(n.Object).(*semantic.PackedVariantViewType); ok {
				if field, ok := viewType.Field(n.Field); ok {
					t = field.Type
					break
				}
			}
			if viewType, ok := s.exprType(n.Object).(*semantic.TreeVariantViewType); ok {
				if field, ok := viewType.Field(n.Field); ok {
					t = field.Type
					break
				}
			}
			objType := s.exprType(n.Object)
			if objType != nil {
				fieldType, _, _, _, err := s.g.fieldInfo(objType, n.Field)
				if err == nil {
					t = fieldType
				}
			}
		case *ast.CastExpr:
			resolved, err := s.resolveTypeExpr(n.Target)
			if err == nil {
				t = resolved
			}
		case *ast.SizeofExpr:
			t = s.g.result.NamedTypes["usize"]
		case *ast.AlignofExpr:
			t = s.g.result.NamedTypes["usize"]
		case *ast.OffsetofExpr:
			t = s.g.result.NamedTypes["usize"]
		case *ast.AddrOfExpr:
			innerType := s.exprType(n.Operand)
			if innerType != nil {
				t = &semantic.RefType{Elem: innerType, State: semantic.RefStateNonNull}
			}
		case *ast.SpecializeExpr:
			t = s.g.exprType(n)
		}
	}
	if t == nil || len(s.typeMap) == 0 {
		return t
	}
	return substituteType(t, s.typeMap, s.g.result.StaticImpls)
}
func (s *functionState) resolveDynamicShapeType(expr *ast.GenericType) (semantic.Type, bool, error) {
	switch expr.Name {
	case "Dict":
		return nil, true, legacyBuiltinReplacementError("Dict", "dict")
	case "view":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("view expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.ViewType{Elem: elem}, true, nil
	case "dview":
		if len(expr.Args) != 1 {
			return nil, true, fmt.Errorf("dview expects 1 argument, got %d", len(expr.Args))
		}
		elem, err := s.resolveTypeExpr(expr.Args[0])
		if err != nil {
			return nil, true, err
		}
		return &semantic.DArrayViewType{Elem: elem, SurfaceName: "dview"}, true, nil
	case "packedview":
		return nil, true, fmt.Errorf("packedview must be written with builtin syntax like packedview[Expr.Lit]")
	case "DArray":
		return nil, true, legacyBuiltinReplacementError("DArray", "darray")
	case "DArrayView":
		return nil, true, legacyBuiltinReplacementError("DArrayView", "dview")
	case "DList":
		return nil, true, fmt.Errorf("DList has been removed from the language; use darray instead")
	case "DListView":
		return nil, true, fmt.Errorf("DListView has been removed from the language; use dview instead")
	case "DStr":
		return nil, true, legacyBuiltinReplacementError("DStr", "cstr")
	default:
		return nil, false, nil
	}
}
func (g *llvmGenerator) fieldInfo(objType semantic.Type, fieldName string) (semantic.Type, int, semantic.Type, bool, error) {
	if objType == nil {
		return nil, 0, nil, false, fmt.Errorf("field access requires a typed base expression for .%s", fieldName)
	}
	pointerLike := false
	if ref, ok := objType.(*semantic.RefType); ok {
		pointerLike = true
		objType = ref.Elem
	}
	objType = semantic.StripAggregateStateType(objType)
	if enumType, ok := objType.(*semantic.EnumType); ok && enumType.Packed {
		// docs/77: common(...) fields live on the hierarchy root; look them up there.
		commonOwner := enumType.Root()
		if commonOwner.Decl == nil {
			return nil, 0, nil, false, fmt.Errorf("packed enum %s is missing declaration metadata", enumType.Name)
		}
		for _, fieldDecl := range commonOwner.Decl.Common {
			if fieldDecl.Name != fieldName {
				continue
			}
			layout, err := g.packedEnumCommonFieldLayout(enumType, fieldName)
			if err != nil {
				return nil, 0, nil, false, err
			}
			if !layout.StoredInline {
				return nil, 0, nil, false, fmt.Errorf("packed enum common field %s.%s is stored in a side table and does not have an inline address", enumType.Name, fieldName)
			}
			return layout.Field.Type, layout.RowFieldIndex, enumType, true, nil
		}
		return nil, 0, nil, false, fmt.Errorf("packed enum %s has no common field %s", enumType.Name, fieldName)
	}
	if runtimeBacked := g.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *semantic.BitGroupType:
		member, ok := t.MemberMap[fieldName]
		if !ok {
			return nil, 0, nil, false, fmt.Errorf("%s has no packed member %s", t, fieldName)
		}
		for i, candidate := range t.Members {
			if candidate.Name == member.Name {
				return member.Type, i, t, true, nil
			}
		}
		return nil, 0, nil, false, fmt.Errorf("%s has no packed member %s", t, fieldName)
	case *semantic.TupleType:
		for i, field := range t.Fields {
			if field.Name == fieldName {
				return field.Type, i, t, pointerLike, nil
			}
		}
		return nil, 0, nil, false, fmt.Errorf("tuple %s has no field %s", t.String(), fieldName)
	case *semantic.StructType:
		index, field, err := fieldInfoFromStruct(t, fieldName)
		return field.Type, index, t, pointerLike, err
	case *semantic.GenericInstanceType:
		base, ok := t.Base.(*semantic.StructType)
		if !ok {
			return nil, 0, nil, false, fmt.Errorf("field access requires a struct-backed type")
		}
		index, field, err := fieldInfoFromStruct(base, fieldName)
		if err != nil {
			return nil, 0, nil, false, err
		}
		subst := genericBindingsForArgs(structGenericParams(base), t.Args)
		return substituteType(field.Type, subst, g.result.StaticImpls), index, t, pointerLike, nil
	case *semantic.TreeBlockType:
		index, field, err := fieldInfoFromOrderedFields(t.Name, treeBlockFieldDecls(t), t.Fields, fieldName)
		return field.Type, index, t, pointerLike, err
	case *semantic.TreeStructType:
		index, field, err := fieldInfoFromOrderedFields(t.Name, treeStructFieldDecls(t), t.Fields, fieldName)
		return field.Type, index, t, pointerLike, err
	default:
		return nil, 0, nil, false, fmt.Errorf("field access requires a struct type, got %s", objType.String())
	}
}
func fieldInfoFromStruct(st *semantic.StructType, fieldName string) (int, semantic.Field, error) {
	if st == nil || st.Decl == nil {
		return 0, semantic.Field{}, fmt.Errorf("struct metadata is unavailable")
	}
	for i, fieldDecl := range st.Decl.Fields {
		if fieldDecl.Name == fieldName {
			field, ok := st.Fields[fieldName]
			if !ok {
				return 0, semantic.Field{}, fmt.Errorf("missing field %s.%s", st.Name, fieldName)
			}
			return i, field, nil
		}
	}
	return 0, semantic.Field{}, fmt.Errorf("struct %s has no field %s", st.Name, fieldName)
}
func fieldInfoFromOrderedFields(typeName string, decls []ast.FieldDecl, fields map[string]semantic.Field, fieldName string) (int, semantic.Field, error) {
	for i, fieldDecl := range decls {
		if fieldDecl.Name != fieldName {
			continue
		}
		field, ok := fields[fieldName]
		if !ok {
			return 0, semantic.Field{}, fmt.Errorf("missing field %s.%s", typeName, fieldName)
		}
		return i, field, nil
	}
	return 0, semantic.Field{}, fmt.Errorf("type %s has no field %s", typeName, fieldName)
}
func (g *llvmGenerator) runtimeBackedStructType(t semantic.Type) semantic.Type {
	if _, ok := t.(*semantic.DArrayViewType); ok {
		if base, ok := g.result.NamedTypes["DynArrayView"]; ok {
			return base
		}
		return nil
	}
	if _, ok := t.(*semantic.SViewType); ok {
		if base, ok := g.result.NamedTypes["StringView"]; ok {
			return base
		}
		return nil
	}
	if dict, ok := t.(*semantic.DictType); ok {
		base, ok := g.result.NamedTypes["DynDict"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{dict.Key, dict.Value}}
	}
	if set, ok := t.(*semantic.SetType); ok {
		base, ok := g.result.NamedTypes["DynSet"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynSet", Base: base, Args: []semantic.Type{set.Elem}}
	}
	if darray, ok := t.(*semantic.DArrayType); ok {
		base, ok := g.result.NamedTypes["DynArray"]
		if !ok {
			return nil
		}
		return &semantic.GenericInstanceType{Name: "DynArray", Base: base, Args: []semantic.Type{darray.Elem}}
	}
	return nil
}
func exprSummary(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Value
	case *ast.Ident:
		return n.Name
	default:
		return "?"
	}
}
// resolveBarePackedVariantWitnessType resolves a bare dotted packed-enum-variant name like
// `Expr.Lit` (enum `Expr`, variant `Lit`) to its PackedVariantViewType — the bare-form analogue of
// `packedview[Expr.Lit]`, mirroring the bare tree-variant form. Returns ok=false for any name that is
// not a packed enum variant, so the caller falls through to its normal unknown-type handling.
func (s *functionState) resolveBarePackedVariantWitnessType(name string) (semantic.Type, bool) {
	lastDot := strings.LastIndex(name, ".")
	if lastDot <= 0 || lastDot >= len(name)-1 {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[name[:lastDot]]
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || enumType == nil || !enumType.Packed {
		return nil, false
	}
	variant, ok := enumType.Variant(name[lastDot+1:])
	if !ok {
		return nil, false
	}
	return s.g.cachedPackedVariantViewType(enumType, variant), true
}

func (s *functionState) resolvePackedVariantViewSurfaceTypeExpr(expr ast.TypeExpr) (semantic.Type, error) {
	named, ok := expr.(*ast.NamedType)
	if !ok {
		return nil, fmt.Errorf("packedview expects a packed enum variant like packedview[Expr.Lit]")
	}
	lastDot := strings.LastIndex(named.Name, ".")
	if lastDot <= 0 || lastDot >= len(named.Name)-1 {
		return nil, fmt.Errorf("packedview expects a packed enum variant like packedview[Expr.Lit]")
	}
	enumName := named.Name[:lastDot]
	variantName := named.Name[lastDot+1:]
	base, ok := s.g.result.NamedTypes[enumName]
	if !ok {
		return nil, fmt.Errorf("unknown packed enum %q in packedview type", enumName)
	}
	enumType, ok := base.(*semantic.EnumType)
	if !ok || enumType == nil {
		return nil, fmt.Errorf("packedview expects a packed enum variant like packedview[Expr.Lit]")
	}
	if !enumType.Packed {
		return nil, fmt.Errorf("packedview requires a packed enum variant, got ordinary enum %q", enumType.Name)
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		return nil, fmt.Errorf("enum %q has no variant %q", enumType.Name, variantName)
	}
	viewType := s.g.cachedPackedVariantViewType(enumType, variant)
	return viewType, nil
}

// resolveBareTreeVariantWitnessType resolves a bare dotted tree-variant name like `Lua.Expr.Binary`
// (category `Lua.Expr`, variant `Binary`) to its TreeVariantViewType — the canonical replacement for
// the removed `treeview[Lua.Expr.Binary]` alias. Returns ok=false for any name that is not a tree
// category variant, so the caller falls through to its normal unknown-type handling.
func (s *functionState) resolveBareTreeVariantWitnessType(name string) (semantic.Type, bool) {
	lastDot := strings.LastIndex(name, ".")
	if lastDot <= 0 || lastDot >= len(name)-1 {
		return nil, false
	}
	base, ok := s.g.result.NamedTypes[name[:lastDot]]
	if !ok {
		return nil, false
	}
	categoryType, ok := base.(*semantic.TreeCategoryType)
	if !ok || categoryType == nil {
		return nil, false
	}
	variant, ok := categoryType.Variant(name[lastDot+1:])
	if !ok {
		return nil, false
	}
	return categoryType.VariantViewType(variant), true
}

func (s *functionState) materializePackedVariantViewValue(binding packedVariantViewBinding) (C.LLVMValueRef, semantic.Type, error) {
	if binding.typ == nil {
		return nil, nil, fmt.Errorf("missing packedview binding type")
	}
	handle := binding.handle
	if handle == nil {
		if binding.ptr == nil {
			return nil, nil, fmt.Errorf("packedview %s is missing both handle and decoded row", binding.typ.String())
		}
		if binding.typ.Enum != nil && !binding.typ.Enum.Packed {
			value, err := s.loadValue(binding.ptr, binding.typ.Enum, "enum.view.value")
			if err != nil {
				return nil, nil, err
			}
			return value, binding.typ.Enum, nil
		}
		if binding.typ.Enum != nil && binding.typ.Enum.Packed && (binding.typ.Enum.StoreType == nil || binding.store.typ == nil || binding.store.value == nil) {
			loadedHandle, err := s.loadValue(binding.ptr, binding.typ.Enum, "packedview.handle")
			if err != nil {
				return nil, nil, err
			}
			handle = loadedHandle
		} else {
			if binding.store.typ == nil || binding.store.value == nil {
				return nil, nil, fmt.Errorf("packedview %s requires store context for materialization", binding.typ.String())
			}
			var err error
			handle, err = s.encodePackedEnumHandleWithStore(binding.ptr, binding.typ.Enum, binding.store.value)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	value, err := s.buildPackedVariantViewValue(binding.typ, handle, &binding.store)
	if err != nil {
		return nil, nil, err
	}
	return value, binding.typ, nil
}
func (s *functionState) buildPackedVariantViewValue(viewType *semantic.PackedVariantViewType, handle C.LLVMValueRef, store *packedStoreBinding) (C.LLVMValueRef, error) {
	if viewType == nil || viewType.Enum == nil {
		return nil, fmt.Errorf("missing packedview type metadata")
	}
	llvmType, err := s.g.lowerType(viewType)
	if err != nil {
		return nil, err
	}
	value := C.LLVMGetUndef(llvmType)
	value = C.LLVMBuildInsertValue(s.builder, value, handle, 0, cStringFree("packedview.handle"))
	if viewType.Enum.StoreType != nil {
		storeValue := C.LLVMValueRef(nil)
		if store != nil && store.typ != nil && store.value != nil {
			storeValue = store.value
		}
		if storeValue == nil {
			storeValue, err = s.zeroValue(viewType.Enum.StoreType)
			if err != nil {
				return nil, err
			}
		}
		value = C.LLVMBuildInsertValue(s.builder, value, storeValue, 1, cStringFree("packedview.store"))
	}
	return value, nil
}
func (g *llvmGenerator) cachedPackedVariantViewType(enumType *semantic.EnumType, variant *semantic.EnumVariant) *semantic.PackedVariantViewType {
	if enumType == nil || variant == nil {
		return nil
	}
	if g != nil && g.packedViewTypes != nil {
		if cached, ok := g.packedViewTypes[variant]; ok && cached != nil {
			return cached
		}
	}
	viewType := variant.PackedViewType(enumType)
	if g != nil && g.packedViewTypes != nil && viewType != nil {
		g.packedViewTypes[variant] = viewType
	}
	return viewType
}
func (s *functionState) unpackPackedVariantViewValue(value C.LLVMValueRef, viewType *semantic.PackedVariantViewType) (packedVariantViewBinding, error) {
	if viewType == nil || viewType.Enum == nil {
		return packedVariantViewBinding{}, fmt.Errorf("missing packedview type metadata")
	}
	binding := packedVariantViewBinding{typ: viewType}
	binding.handle = C.LLVMBuildExtractValue(s.builder, value, 0, cStringFree("packedview.handle.extract"))
	if viewType.Enum.StoreType != nil {
		binding.store = packedStoreBinding{
			value: C.LLVMBuildExtractValue(s.builder, value, 1, cStringFree("packedview.store.extract")),
			typ:   viewType.Enum.StoreType,
		}
	}
	return binding, nil
}
