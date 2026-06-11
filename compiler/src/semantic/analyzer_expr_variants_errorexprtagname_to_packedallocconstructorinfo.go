package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) errorExprTagName(expr ast.Expr) (string, bool) {
	if callExpr, ok := expr.(*ast.CallExpr); ok {
		return a.errorExprTagName(callExpr.Func)
	}
	fieldExpr, ok := expr.(*ast.FieldExpr)
	if !ok {
		return "", false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	base, _, ok := a.lookupVisibleType(ident.Name)
	if !ok {
		return "", false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok || !errSet.HasQualifiedTag(ident.Name, fieldExpr.Field) {
		return "", false
	}
	return QualifyErrorTag(ident.Name, fieldExpr.Field), true
}

func (a *Analyzer) errorConstructorCall(expr *ast.CallExpr) (*ErrorSetType, string, []Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, "", nil, false
	}
	ident, ok := fieldExpr.Object.(*ast.Ident)
	if !ok {
		return nil, "", nil, false
	}
	base, _, ok := a.lookupVisibleType(ident.Name)
	if !ok {
		return nil, "", nil, false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok {
		return nil, "", nil, false
	}
	qualifiedTag := QualifyErrorTag(ident.Name, fieldExpr.Field)
	if !errSet.HasTag(qualifiedTag) {
		a.errorf(expr.Pos(), "error set %q has no tag %q", ErrorSetDiagnosticName(errSet), fieldExpr.Field)
		return errSet, qualifiedTag, nil, true
	}
	return errSet, qualifiedTag, errSet.PayloadForTag(qualifiedTag), true
}

func (a *Analyzer) errorTagType(expr *ast.FieldExpr) (Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(ident.Name)
	if !ok {
		return nil, false
	}
	errSet, ok := base.(*ErrorSetType)
	if !ok {
		return nil, false
	}
	if !errSet.HasQualifiedTag(ident.Name, expr.Field) {
		a.errorf(expr.Pos(), "error set %q has no tag %q", ErrorSetDiagnosticName(errSet), expr.Field)
		return invalidType, true
	}
	return errSet, true
}
func (a *Analyzer) packedEnumTagExprType(expr *ast.FieldExpr) (Type, bool) {
	ident, ok := expr.Object.(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, _, ok := a.lookupVisibleType(ident.Name)
	if !ok {
		return nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || expr.Field != "Tag" {
		return nil, false
	}
	if !enumType.Packed || enumType.TagType == nil {
		a.errorf(expr.Pos(), "enum %q has no nested tag type", enumType.Name)
		return invalidType, true
	}
	return enumType.TagType, true
}

// analyzeEnumColumnExpr types a first-class column scan `Expr of .field`
// (docs/76 §5). The enum must be declared `layout soa` (columnar), and the
// selected field must be a *dense* column — the tag, or a common(...) field.
// Per-variant payload fields are sparse and deferred to `layout soa(sparse)`.
// The result is a `view[fieldType]` so the existing iterable-for machinery
// derives the loop element type; the backend special-cases the scan.
func (a *Analyzer) analyzeEnumColumnExpr(expr *ast.EnumColumnExpr) Type {
	base, _, ok := a.lookupVisibleType(expr.Enum)
	if !ok {
		a.errorf(expr.Pos(), "column scan source %q is not a type", expr.Enum)
		return invalidType
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		a.errorf(expr.Pos(), "column scan requires an enum type, got %s", base)
		return invalidType
	}
	// docs/77 Phase 4: the column layout is a ROOT-level fact — a sealed hierarchy shares one
	// store, so a sub-category scan reads the root's columns filtered to the category's tag range.
	root := enumType.Root()
	if !root.Packed || !root.LayoutSet || root.Layout != ast.StructLayoutSOA {
		a.errorf(expr.Pos(), "column scan `%s of .%s` requires `enum %s layout soa`; the default layout stores nodes row-major (AoS), which has no dense columns", expr.Enum, expr.Field, root.Name)
		return invalidType
	}
	var elem Type
	switch {
	case expr.Field == "tag":
		if root.TagType == nil {
			a.errorf(expr.Pos(), "enum %q has no tag column", root.Name)
			return invalidType
		}
		elem = root.TagType
	default:
		field, ok := enumType.Common[expr.Field]
		if !ok {
			if _, isPayload := a.enumColumnVariantPayloadField(enumType, expr.Field); isPayload {
				a.errorf(expr.Pos(), "column scan `%s of .%s` selects a per-variant payload field, which is a sparse column; only `tag` and common(...) fields are dense. (per-variant column scans require `layout soa(sparse)`)", expr.Enum, expr.Field)
				return invalidType
			}
			a.errorf(expr.Pos(), "enum %q has no common field or tag named %q to scan", enumType.Name, expr.Field)
			return invalidType
		}
		elem = field.Type
	}
	return &ViewType{Elem: elem, SurfaceName: "view"}
}

// enumColumnVariantPayloadField reports whether name is a named payload field
// of any variant (used only to produce a precise diagnostic).
func (a *Analyzer) enumColumnVariantPayloadField(enumType *EnumType, name string) (Type, bool) {
	for _, variant := range enumType.Variants {
		for i, pname := range variant.PayloadNames {
			if pname == name && i < len(variant.Payload) {
				return variant.Payload[i], true
			}
		}
	}
	return nil, false
}

func shorthandMemberName(parts []string) string {
	return strings.Join(parts, ".")
}
func shorthandMemberDisplay(parts []string) string {
	return "." + shorthandMemberName(parts)
}
func contextualShorthandExpr(expr ast.Expr) (*ast.ShorthandMemberExpr, bool) {
	switch n := expr.(type) {
	case *ast.ShorthandMemberExpr:
		return n, true
	case *ast.ParenExpr:
		return contextualShorthandExpr(n.Inner)
	default:
		return nil, false
	}
}
func (a *Analyzer) analyzeShorthandMemberExpr(expr *ast.ShorthandMemberExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expected == nil {
		a.errorf(expr.Pos(), "shorthand member %q requires an expected const enum type", shorthandMemberDisplay(expr.Parts))
		return invalidType
	}
	if IsInvalidType(expected) {
		return invalidType
	}
	constEnumType, ok := expected.(*ConstEnumType)
	if !ok || constEnumType == nil {
		a.errorf(expr.Pos(), "shorthand member %q requires an expected const enum type", shorthandMemberDisplay(expr.Parts))
		return invalidType
	}
	memberName := shorthandMemberName(expr.Parts)
	if _, ok := constEnumType.Member(memberName); !ok {
		a.errorf(expr.Pos(), "const enum %q has no member %q", constEnumType.Name, memberName)
		return invalidType
	}
	return constEnumType
}
func (a *Analyzer) analyzeContextualShorthandValueExpr(expr ast.Expr, expected Type) (Type, bool) {
	switch n := expr.(type) {
	case *ast.ShorthandMemberExpr:
		result := a.analyzeShorthandMemberExpr(n, expected)
		a.recordAnalyzedExprType(n, result)
		return result, true
	case *ast.ParenExpr:
		innerType, ok := a.analyzeContextualShorthandValueExpr(n.Inner, expected)
		if !ok {
			return nil, false
		}
		a.recordAnalyzedExprType(n, innerType)
		return innerType, true
	default:
		return nil, false
	}
}
func (a *Analyzer) constEnumMemberInfoForExpr(expr ast.Expr) (*ConstEnumType, string, *ConstEnumMember, bool) {
	fullName, ok := qualifiedTypePathFromExpr(expr)
	if !ok || fullName == "" {
		return nil, "", nil, false
	}
	parts := strings.Split(fullName, ".")
	var matchedType *ConstEnumType
	var matchedMemberName string
	for i := len(parts) - 1; i >= 1; i-- {
		baseName := strings.Join(parts[:i], ".")
		base, _, ok := a.lookupVisibleType(baseName)
		if !ok {
			continue
		}
		constEnumType, ok := base.(*ConstEnumType)
		if !ok || constEnumType == nil {
			continue
		}
		memberName := strings.Join(parts[i:], ".")
		if matchedType == nil {
			matchedType = constEnumType
			matchedMemberName = memberName
		}
		if member, ok := constEnumType.Member(memberName); ok {
			return constEnumType, memberName, member, true
		}
	}
	if matchedType != nil {
		return matchedType, matchedMemberName, nil, true
	}
	return nil, "", nil, false
}
func (a *Analyzer) constEnumTypeForExpr(expr ast.Expr) (*ConstEnumType, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		base, _, ok := a.lookupVisibleType(n.Name)
		if !ok {
			return nil, false
		}
		constEnumType, ok := base.(*ConstEnumType)
		return constEnumType, ok
	case *ast.FieldExpr:
		tagType, ok := a.packedEnumTagExprType(n)
		if !ok {
			return nil, false
		}
		constEnumType, ok := tagType.(*ConstEnumType)
		return constEnumType, ok
	case *ast.ParenExpr:
		return a.constEnumTypeForExpr(n.Inner)
	default:
		return nil, false
	}
}
func (a *Analyzer) constEnumMemberExprType(expr *ast.FieldExpr) (Type, bool) {
	constEnumType, memberName, member, ok := a.constEnumMemberInfoForExpr(expr)
	if !ok {
		return nil, false
	}
	if member == nil {
		a.errorf(expr.Pos(), "const enum %q has no member %q", constEnumType.Name, memberName)
		return invalidType, true
	}
	return constEnumType, true
}
func qualifiedTypePathFromExpr(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return "", false
		}
		return n.Name, true
	case *ast.FieldExpr:
		prefix, ok := qualifiedTypePathFromExpr(n.Object)
		if !ok || prefix == "" || n.Field == "" {
			return "", false
		}
		return prefix + "." + n.Field, true
	case *ast.ParenExpr:
		return qualifiedTypePathFromExpr(n.Inner)
	case *ast.TypeExprExpr:
		named, ok := n.Type.(*ast.NamedType)
		if !ok || named == nil || named.Name == "" {
			return "", false
		}
		return named.Name, true
	default:
		return "", false
	}
}
func (a *Analyzer) enumVariantExprType(expr *ast.FieldExpr) (*EnumType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		a.errorf(expr.Pos(), "enum %q has no variant %q", enumType.Name, expr.Field)
		return enumType, invalidType, true
	}
	if enumType.Packed {
		if len(variant.Payload) == 0 {
			if _, ok := a.lookupPackedStore(enumType); ok {
				return enumType, nil, true
			}
			a.errorf(expr.Pos(), "packed enum constructor %q requires an active in %s: scope or explicit new[%s]", enumType.Name+"."+variant.Name, packedEnumStoreTypeName(enumType.Name), packedEnumStoreTypeName(enumType.Name))
			return enumType, invalidType, true
		}
		params := make([]Type, len(variant.Payload))
		copy(params, variant.Payload)
		return enumType, &FuncType{Name: enumType.Name + "." + variant.Name, Params: params, Return: enumType}, true
	}
	if len(variant.Payload) == 0 {
		return enumType, nil, true
	}
	params := make([]Type, len(variant.Payload))
	copy(params, variant.Payload)
	return enumType, &FuncType{Name: enumType.Name + "." + variant.Name, Params: params, Return: enumType}, true
}
func (a *Analyzer) packedStoreExprType(expr *ast.FieldExpr) (*PackedEnumStoreType, Type, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok || !enumType.Packed || expr.Field != "Store" {
		return nil, nil, false
	}
	if enumType.StoreType == nil {
		return nil, invalidType, true
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok {
		return enumType.StoreType, invalidType, true
	}
	localStore := PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"])
	return enumType.StoreType, &FuncType{Name: enumType.StoreType.Name, Params: []Type{arenaType}, Return: localStore}, true
}
func (a *Analyzer) packedStoreConstructorCall(expr *ast.CallExpr) (*PackedEnumStoreType, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, false
	}
	storeType, _, ok := a.packedStoreExprType(fieldExpr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 {
		a.errorf(expr.Pos(), "store constructor %q expects 1 argument, got %d", storeType, len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return PackedEnumStoreWithState(storeType, a.namedTypes["Local"]), true
	}
	if arenaType, ok := a.namedTypes["Arena"]; ok {
		actual := a.analyzeValueExpr(expr.Args[0], arenaType)
		if !AssignableTo(arenaType, actual) {
			a.errorf(expr.Args[0].Pos(), "store constructor %q expects %s, got %s", storeType, arenaType, actual)
		}
		a.markRegionAllocatedByOwnerExpr(expr.Args[0])
	} else {
		a.analyzeExpr(expr.Args[0])
	}
	return PackedEnumStoreWithState(storeType, a.namedTypes["Local"]), true
}

func (a *Analyzer) markRegionAllocatedByOwnerExpr(expr ast.Expr) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return
	}
	sym, state := a.lookupRegionState(ident.Name)
	if sym == nil || state.Destroyed {
		return
	}
	state.Allocated = true
	a.currentRegions[sym] = state
}

func (a *Analyzer) enumConstructorCall(expr *ast.CallExpr) (*EnumType, *EnumVariant, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok {
		return nil, nil, false
	}
	enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(fieldExpr)
	if !ok {
		return nil, nil, false
	}
	if variant == nil {
		a.errorf(fieldExpr.Pos(), "enum %q has no variant %q", enumType.Name, fieldExpr.Field)
		return enumType, nil, true
	}
	return enumType, variant, true
}
func (a *Analyzer) enumConstructorInfoFromFieldExpr(expr *ast.FieldExpr) (*EnumType, *EnumVariant, bool) {
	baseName, ok := qualifiedTypePathFromExpr(expr.Object)
	if !ok {
		return nil, nil, false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return nil, nil, false
	}
	enumType, ok := base.(*EnumType)
	if !ok {
		return nil, nil, false
	}
	variant, ok := enumType.Variant(expr.Field)
	if !ok {
		return enumType, nil, true
	}
	return enumType, variant, true
}
func (a *Analyzer) activePackedStoreRegionState(enumType *EnumType) (regionRefState, bool) {
	if a == nil || enumType == nil {
		return regionRefState{}, false
	}
	activeStore, ok := a.lookupPackedStore(enumType)
	if !ok || activeStore == nil {
		return regionRefState{}, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		for _, sym := range scope.Symbols {
			storeType, ok := sym.Type.(*PackedEnumStoreType)
			if !ok || storeType == nil || storeType.Enum != enumType || !SameType(storeType, activeStore) {
				continue
			}
			if a.currentRegionRefs != nil {
				if state, ok := a.currentRegionRefs[sym]; ok && hasRegionProvenance(state) {
					state = a.canonicalizeStoredRegionRefBinding(sym, state)
					return cloneRegionRefState(state), true
				}
			}
			return regionRefStateFromPackedStoreDependency(sym, storeType), true
		}
	}
	return regionRefState{}, false
}
func (a *Analyzer) packedAllocConstructorInfo(expr ast.Expr) (*EnumType, *EnumVariant, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		return a.enumConstructorInfoFromFieldExpr(n)
	case *ast.CallExpr:
		return a.enumConstructorCall(n)
	default:
		return nil, nil, false
	}
}
