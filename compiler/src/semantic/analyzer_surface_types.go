package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (a *Analyzer) resolveDynamicShapeType(expr *ast.GenericType) (Type, bool) {
	switch expr.Name {
	case "Dict":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "Dict", "dict")
		return invalidType, true
	case "view":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "view expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &ViewType{Elem: a.resolveType(expr.Args[0]), SurfaceName: "view"}, true
	case "dview":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "dview expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayViewType{Elem: a.resolveType(expr.Args[0]), SurfaceName: "dview"}, true
	case "DArray":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DArray", "darray")
		return invalidType, true
	case "DArrayView":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DArrayView", "dview")
		return invalidType, true
	case "DList":
		a.errorf(expr.Pos(), "DList has been removed from the language; use darray instead")
		return invalidType, true
	case "DListView":
		a.errorf(expr.Pos(), "DListView has been removed from the language; use dview instead")
		return invalidType, true
	case "DStr":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "DStr", "cstr")
		return invalidType, true
	default:
		return nil, false
	}
}

func (a *Analyzer) resolvePackedVariantViewSurfaceType(expr ast.TypeExpr, pos lexer.Pos) Type {
	named, ok := expr.(*ast.NamedType)
	if !ok {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	lastDot := strings.LastIndex(named.Name, ".")
	if lastDot <= 0 || lastDot >= len(named.Name)-1 {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	enumName := named.Name[:lastDot]
	variantName := named.Name[lastDot+1:]
	base, ok := a.namedTypes[enumName]
	if !ok {
		a.errorf(pos, "unknown packed enum %q in packedview type", enumName)
		return invalidType
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		a.errorf(pos, "packedview expects a packed enum variant like packedview[Expr.Lit]")
		return invalidType
	}
	if !enumType.Packed {
		a.errorf(pos, "packedview requires a packed enum variant, got ordinary enum %q", enumType.Name)
		return invalidType
	}
	variant, ok := enumType.Variant(variantName)
	if !ok {
		a.errorf(pos, "enum %q has no variant %q", enumType.Name, variantName)
		return invalidType
	}
	viewType := &PackedVariantViewType{Enum: enumType, Variant: variant}
	return viewType
}

func (a *Analyzer) resolveNamedVariantWitnessType(named *ast.NamedType) (Type, bool) {
	if named == nil {
		return nil, false
	}
	idx := strings.LastIndex(named.Name, ".")
	if idx <= 0 || idx+1 >= len(named.Name) {
		return nil, false
	}
	baseName := named.Name[:idx]
	variantName := named.Name[idx+1:]
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, false
	}
	switch tt := base.(type) {
	case *TreeCategoryType:
		variant, ok := tt.Variant(variantName)
		if !ok || variant == nil {
			a.errorf(named.Pos(), "tree category %q has no variant %q", tt.Name, variantName)
			return invalidType, true
		}
		return tt.VariantViewType(variant), true
	case *EnumType:
		variant, ok := tt.Variant(variantName)
		if !ok || variant == nil {
			a.errorf(named.Pos(), "enum %q has no variant %q", tt.Name, variantName)
			return invalidType, true
		}
		if !tt.Packed {
			a.errorf(named.Pos(), "bare variant type %q requires a packed enum or tree category; ordinary enum variants are not first-class types", named.Name)
			return invalidType, true
		}
		return variant.PackedViewType(tt), true
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveBuiltinSurfaceType(expr *ast.BuiltinTypeExpr) Type {
	switch expr.Name {
	case "id":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "id expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		tag := a.resolveType(expr.TypeArgs[0])
		if IsInvalidType(tag) {
			return invalidType
		}
		return &IDType{Tag: tag, Storage: a.namedTypes["u32"]}
	case "ptrid":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "ptrid expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		tag := a.resolveType(expr.TypeArgs[0])
		if IsInvalidType(tag) {
			return invalidType
		}
		return &IDType{Tag: tag, Storage: a.namedTypes["uintptr"]}
	case "GuestVAddr", "HostPtr", "NativeMappedGuestPtr":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "%s expects 1 type argument, got %d", expr.Name, len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		elem := a.resolveType(expr.TypeArgs[0])
		if IsInvalidType(elem) {
			return invalidType
		}
		space := "guest"
		if expr.Name == "HostPtr" {
			space = "host"
		} else if expr.Name == "NativeMappedGuestPtr" {
			space = "native_mapped_guest"
		}
		return &AddressSpaceType{Space: space, Elem: elem, Storage: a.namedTypes["uintptr"]}
	case "RowId":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "RowId expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		tag := a.resolveType(expr.TypeArgs[0])
		store, ok := tag.(*StructType)
		if IsInvalidType(tag) || !ok || store == nil || store.StoreDecl == nil || !store.StoreDecl.Soa {
			a.errorf(expr.Pos(), "RowId expects an soa type argument, got %s", tag)
			return invalidType
		}
		return &IDType{Tag: tag, Storage: a.namedTypes["u32"]}
	case "array":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "array expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{Position: expr.Position, Elem: expr.TypeArgs[0], Size: expr.ValueArgs[0]})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "array"
		}
		return resolved
	case "darray":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) > 1 {
			a.errorf(expr.Pos(), "darray expects 1 or 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		if len(expr.ValueArgs) == 0 {
			return &DArrayType{Elem: a.resolveType(expr.TypeArgs[0]), Shape: &WildcardShape{}, SurfaceName: "darray", Region: expr.Region}
		}
		return &DArrayType{Elem: a.resolveType(expr.TypeArgs[0]), Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "darray", Region: expr.Region}
	case "dict":
		if len(expr.TypeArgs) != 2 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "dict expects 2 type arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolveDictType(expr.TypeArgs[0], expr.TypeArgs[1], "dict", expr.Region)
	case "set":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "set expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolveSetType(expr.TypeArgs[0], "set", expr.Region)
	case "str":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "str expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{
			Position: expr.Position,
			Elem:     &ast.NamedType{Position: expr.Position, Name: "u8"},
			Size:     expr.ValueArgs[0],
		})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "str"
		}
		return resolved
	case "string":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "string", "str")
		return invalidType
	case "cstr":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "cstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DStrType{Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "cstr"}
	case "cstring":
		a.errorLegacyBuiltinReplacement(expr.Pos(), "cstring", "cstr")
		return invalidType
	case "view":
		if len(expr.TypeArgs) != 1 {
			a.errorf(expr.Pos(), "view expects 1 type argument, got %d", len(expr.TypeArgs))
			return invalidType
		}
		viewType := &ViewType{Elem: a.resolveType(expr.TypeArgs[0]), SurfaceName: "view"}
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = a.exprSummary(expr.ValueArgs[0])
			viewType.End = a.exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return viewType
	case "dview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "dview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DArrayViewType{Elem: a.resolveType(expr.TypeArgs[0]), SurfaceName: "dview"}
	case "packedview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "packedview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return a.resolvePackedVariantViewSurfaceType(expr.TypeArgs[0], expr.Pos())
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			a.errorf(expr.Pos(), "sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &SViewType{Begin: a.exprSummary(expr.ValueArgs[0]), End: a.exprSummary(expr.ValueArgs[1])}
	default:
		a.errorf(expr.Pos(), "unknown built-in type %q", expr.Name)
		return invalidType
	}
}

func (a *Analyzer) resolveDictType(keyExpr ast.TypeExpr, valueExpr ast.TypeExpr, surfaceName string, region string) Type {
	keyType := a.resolveType(keyExpr)
	valueType := a.resolveType(valueExpr)
	if IsInvalidType(keyType) || IsInvalidType(valueType) {
		return invalidType
	}
	if a.containsAffineHandleValues(keyType, map[string]bool{}) {
		a.errorf(keyExpr.Pos(), "dict keys cannot contain linear handles, got %s", keyType)
		return invalidType
	}
	return &DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName, Region: region}
}

func (a *Analyzer) resolveSetType(elemExpr ast.TypeExpr, surfaceName string, region string) Type {
	elemType := a.resolveType(elemExpr)
	if IsInvalidType(elemType) {
		return invalidType
	}
	if a.containsAffineHandleValues(elemType, map[string]bool{}) {
		a.errorf(elemExpr.Pos(), "set elements cannot contain linear handles, got %s", elemType)
		return invalidType
	}
	return &SetType{Elem: elemType, SurfaceName: surfaceName, Region: region}
}

// dictRuntimeBackedKeyType reports whether a dict key type is supported by the runtime-backed
// hash table. Runtime lookup hashes the key with ctx_hash_value and confirms matches with `==`,
// so any type with deterministic hashing and well-defined value equality works: cstr (content
// hash/equality), every integral type (value hash/equality: int/iN/uN/usize/uintptr/char/BitInt),
// bool, and const enums (integer-backed). Floats are excluded (== is unsafe: NaN, precision).
// A generic K (TypeParamType) is deferred — its concrete binding is re-checked at instantiation.
func dictRuntimeBackedKeyType(keyType Type) bool {
	stripped := StripAggregateStateType(keyType)
	switch stripped.(type) {
	case *DStrType, *TypeParamType, *ConstEnumType:
		return true
	}
	return IsIntegralType(stripped) || IsBoolType(stripped)
}

func dictSupportsRuntimeBackedOps(dictType *DictType) bool {
	return dictType != nil && dictRuntimeBackedKeyType(dictType.Key)
}

func setSupportsRuntimeBackedOps(setType *SetType) bool {
	return setType != nil && dictRuntimeBackedKeyType(setType.Elem)
}
