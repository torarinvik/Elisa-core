//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) resolveErrorSetExpr(expr *ast.ErrorSetExpr) (semantic.Type, error) {
	if expr == nil || len(expr.Tags) == 0 {
		return nil, fmt.Errorf("error[...] requires at least one qualified error tag")
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		return nil, fmt.Errorf("error[Set.*, ...] is no longer supported; use error[Set] instead")
	}
	if expr.HasEllipsis {
		return nil, fmt.Errorf("the `, ...` suffix in error[...] is no longer supported; name the full sets directly (a bare set name already means the whole family)")
	}
	if containsWildcardErrorTag(expr.Tags) {
		return nil, fmt.Errorf("error[Set.*] is no longer supported; use error[Set] instead")
	}
	if len(expr.Tags) == 1 && expr.Tags[0].Tag == "" {
		_, errSet, err := s.lookupDeclaredErrorSet(expr.Tags[0])
		if err != nil {
			return nil, err
		}
		return errSet, nil
	}
	familySets := map[string]*semantic.ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range expr.Tags {
		_, errSet, err := s.lookupDeclaredErrorSet(tag)
		if err != nil {
			return nil, err
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := semantic.SplitErrorTagName(qualifiedTag)
					return nil, fmt.Errorf("duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: semantic.ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := semantic.QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			return nil, fmt.Errorf("error set %q has no tag %q", tag.SetName, tag.Tag)
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			return nil, fmt.Errorf("duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return semantic.CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags), nil
}
func (s *functionState) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *semantic.ErrorSetType, error) {
	t, ok := s.g.lookupNamedType(tag.SetName)
	if !ok {
		return "", nil, fmt.Errorf("unknown error set %q", tag.SetName)
	}
	errSet, ok := t.(*semantic.ErrorSetType)
	if !ok {
		return "", nil, fmt.Errorf("%q is not an error set", tag.SetName)
	}
	return tag.SetName, errSet, nil
}
func containsWildcardErrorTag(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "*" {
			return true
		}
	}
	return false
}
func containsBareFamily(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "" {
			return true
		}
	}
	return false
}
func onlyBareFamilies(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag != "" {
			return false
		}
	}
	return len(tags) > 0
}
func singleExplicitErrorFamily(tags []ast.ErrorTagExpr) (string, bool) {
	family := ""
	for _, tag := range tags {
		if tag.Tag == "" {
			return "", false
		}
		if family == "" {
			family = tag.SetName
			continue
		}
		if tag.SetName != family {
			return "", false
		}
	}
	return family, family != ""
}
func (s *functionState) resolveBuiltinSurfaceTypeExpr(expr *ast.BuiltinTypeExpr) (semantic.Type, error) {
	switch expr.Name {
	case "id":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("id expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		tag, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return &semantic.IDType{Tag: tag, Storage: s.g.namedType("u32")}, nil
	case "ptrid":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("ptrid expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		tag, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return &semantic.IDType{Tag: tag, Storage: s.g.namedType("uintptr")}, nil
	case "GuestVAddr", "HostPtr", "NativeMappedGuestPtr":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("%s expects 1 type argument, got %d", expr.Name, len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		// The pointee type is ABI-irrelevant: an address-space carrier always lowers to its uintptr
		// storage (i64), so the Elem is purely informational. When the type-arg is a docs/107 overlay
		// LAYOUT name (registered in the semantic overlay registry, not in the backend's NamedTypes),
		// it does not resolve here — fall back to uintptr as the opaque pointee. The semantic analyzer
		// has already validated the type-arg (real type or registered layout), so this is sound.
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			elem = s.g.namedType("uintptr")
		}
		space := "guest"
		if expr.Name == "HostPtr" {
			space = "host"
		} else if expr.Name == "NativeMappedGuestPtr" {
			space = "native_mapped_guest"
		}
		return &semantic.AddressSpaceType{Space: space, Elem: elem, Storage: s.g.namedType("uintptr")}, nil
	case "RowId":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("RowId expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		tag, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		store, ok := tag.(*semantic.StructType)
		if !ok || store == nil || store.StoreDecl == nil || !store.StoreDecl.Soa {
			return nil, fmt.Errorf("RowId expects an soa type argument, got %s", tag)
		}
		return &semantic.IDType{Tag: tag, Storage: s.g.namedType("u32")}, nil
	case "array":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("array expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		resolved, err := s.resolveTypeExpr(&ast.ArrayType{Position: expr.Position, Elem: expr.TypeArgs[0], Size: expr.ValueArgs[0]})
		if err != nil {
			return nil, err
		}
		if arrayType, ok := resolved.(*semantic.ArrayType); ok {
			arrayType.SurfaceName = "array"
		}
		return resolved, nil
	case "darray":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) > 1 {
			return nil, fmt.Errorf("darray expects 1 or 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		if len(expr.ValueArgs) == 0 {
			return &semantic.DArrayType{Elem: elem, Shape: &semantic.WildcardShape{}, SurfaceName: "darray", Region: substituteRegionName(expr.Region, s.typeMap)}, nil
		}
		return &semantic.DArrayType{Elem: elem, Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "darray", Region: substituteRegionName(expr.Region, s.typeMap)}, nil
	case "dict":
		if len(expr.TypeArgs) != 2 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("dict expects 2 type arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		keyType, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		valueType, err := s.resolveTypeExpr(expr.TypeArgs[1])
		if err != nil {
			return nil, err
		}
		return resolveBackendDictType(keyType, valueType, "dict", substituteRegionName(expr.Region, s.typeMap))
	case "set":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("set expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		elemType, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return resolveBackendSetType(elemType, "set", substituteRegionName(expr.Region, s.typeMap))
	case "cstr":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("cstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.CStrType{Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "cstr", Region: substituteRegionName(expr.Region, s.typeMap)}, nil
	case "view":
		if len(expr.TypeArgs) != 1 {
			return nil, fmt.Errorf("view expects 1 type argument, got %d", len(expr.TypeArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		viewType := &semantic.ViewType{Elem: elem, SurfaceName: "view"}
		viewType.Region = substituteRegionName(expr.Region, s.typeMap)
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = exprSummary(expr.ValueArgs[0])
			viewType.End = exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return viewType, nil
	case "packedview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("packedview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return s.resolvePackedVariantViewSurfaceTypeExpr(expr.TypeArgs[0])
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			return nil, fmt.Errorf("sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.SViewType{Begin: exprSummary(expr.ValueArgs[0]), End: exprSummary(expr.ValueArgs[1]), Region: substituteRegionName(expr.Region, s.typeMap)}, nil
	default:
		return nil, fmt.Errorf("unknown built-in type %q", expr.Name)
	}
}
func resolveBackendDictType(keyType semantic.Type, valueType semantic.Type, surfaceName string, region ...string) (semantic.Type, error) {
	dictRegion := ""
	if len(region) != 0 {
		dictRegion = region[0]
	}
	return &semantic.DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName, Region: dictRegion}, nil
}

func resolveBackendSetType(elemType semantic.Type, surfaceName string, region ...string) (semantic.Type, error) {
	setRegion := ""
	if len(region) != 0 {
		setRegion = region[0]
	}
	return &semantic.SetType{Elem: elemType, SurfaceName: surfaceName, Region: setRegion}, nil
}

// preferBackendLiveIdentType reconciles two valid-but-different views of an
// identifier's type during lowering:
//   - cached semantic expr types, which can carry flow-sensitive refinements
//     such as TreeVariantViewType or non-null optional payload types
//   - live backend bindings, which can carry the current generic
//     specialization for a function body instantiation
//
// We prefer the narrower cached refinement when it is compatible with the live
// binding, but fall back to the live type when the cached type is stale from a
// different specialization.
func preferBackendLiveIdentType(cached semantic.Type, live semantic.Type) semantic.Type {
	if live == nil {
		return cached
	}
	if cached == nil {
		return live
	}
	// Every branch below returns cached or live, so one type needs no comparison.
	if cached == live {
		return cached
	}
	// A stale <invalid> cache entry (e.g. from a failed speculative analysis
	// pass) must never shadow a live binding that carries a real type.
	if semantic.IsInvalidType(cached) && !semantic.IsInvalidType(live) {
		return live
	}
	if semantic.SameType(cached, live) {
		return cached
	}
	strippedCached := semantic.StripAggregateStateType(cached)
	strippedLive := semantic.StripAggregateStateType(live)
	if cachedView, ok := strippedCached.(*semantic.PackedVariantViewType); ok && cachedView != nil && cachedView.Enum != nil {
		if liveEnum, ok := strippedLive.(*semantic.EnumType); ok && liveEnum != nil && cachedView.Enum.Name == liveEnum.Name {
			return cached
		}
	}
	liveAcceptsCached := semantic.AssignableTo(live, cached)
	cachedAcceptsLive := semantic.AssignableTo(cached, live)
	switch {
	case liveAcceptsCached && !cachedAcceptsLive:
		return cached
	case cachedAcceptsLive && !liveAcceptsCached:
		return live
	case !liveAcceptsCached && !cachedAcceptsLive:
		return live
	default:
		return cached
	}
}
