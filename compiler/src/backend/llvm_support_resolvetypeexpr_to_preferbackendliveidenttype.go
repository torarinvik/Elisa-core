//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strings"
)

func (s *functionState) resolveTypeExpr(expr ast.TypeExpr) (semantic.Type, error) {
	switch n := expr.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "cstr":
			return &semantic.DStrType{Shape: &semantic.WildcardShape{}, SurfaceName: "cstr"}, nil
		case "sview":
			return &semantic.SViewType{}, nil
		case "dstr":
			return &semantic.DArrayType{Elem: s.g.result.NamedTypes["u8"], Shape: &semantic.WildcardShape{}, SurfaceName: "dstr"}, nil
		case "cstring":
			return nil, legacyBuiltinReplacementError("cstring", "cstr")
		case "DStr":
			return nil, legacyBuiltinReplacementError("DStr", "cstr")
		}
		if bound, ok := s.typeMap[n.Name]; ok {
			return bound, nil
		}
		// Prefer the analyzer's recorded resolution (handles namespace / using /
		// import qualification that the bare-name lookup below cannot see).
		if s.g != nil && s.g.result != nil && s.g.result.ResolvedTypeNames != nil {
			if canonical, ok := s.g.result.ResolvedTypeNames[n]; ok {
				if t, ok := s.g.result.NamedTypes[canonical]; ok {
					return semantic.DefaultStatefulType(t), nil
				}
			}
		}
		if t, ok := s.g.result.NamedTypes[n.Name]; ok {
			return semantic.DefaultStatefulType(t), nil
		}
		if t, handled, err := s.resolveStaticAssociatedType(n.Name); handled {
			if err != nil {
				return nil, err
			}
			return t, nil
		}
		return nil, fmt.Errorf("unknown type %q", n.Name)
	case *ast.StateSetTypeExpr:
		return nil, fmt.Errorf("state unions like %q are only valid as named struct state arguments", strings.Join(n.Cases, " | "))
	case *ast.AggregateStateTypeExpr:
		baseType, err := s.resolveTypeExpr(n.Base)
		if err != nil {
			return nil, err
		}
		baseType = semantic.StripAggregateStateType(baseType)
		count := semantic.AggregateStateParamCount(baseType)
		if count == 0 {
			return nil, fmt.Errorf("type %q does not declare an aggregate state parameter", baseType.String())
		}
		states := backendAggregateStates(n)
		if len(states) != count {
			return nil, fmt.Errorf("type %q expects %d aggregate state arguments, got %d", baseType.String(), count, len(states))
		}
		return &semantic.AggregateStateType{Base: baseType, State: states[0], States: states}, nil
	case *ast.RefStateLiteralTypeExpr:
		return &semantic.RefStateValueType{State: semantic.RefState(n.State)}, nil
	case *ast.RefStorageLiteralTypeExpr:
		return &semantic.RefStorageValueType{Storage: semantic.RefStorage(n.Storage)}, nil
	case *ast.ErrorSetExpr:
		return s.resolveErrorSetExpr(n)
	case *ast.ErrorUnionTypeExpr:
		valueType, err := s.resolveTypeExpr(n.Value)
		if err != nil {
			return nil, err
		}
		errorType, err := s.resolveTypeExpr(n.Errors)
		if err != nil {
			return nil, err
		}
		errSet, ok := errorType.(*semantic.ErrorSetType)
		if !ok {
			return nil, fmt.Errorf("error union expects an error set on the right-hand side")
		}
		return &semantic.ErrorUnionType{Value: valueType, Errors: errSet}, nil
	case *ast.OptionalTypeExpr:
		valueType, err := s.resolveTypeExpr(n.Value)
		if err != nil {
			return nil, err
		}
		if isVoidType(valueType) {
			return nil, fmt.Errorf("value optionals cannot wrap void")
		}
		if _, ok := valueType.(*semantic.RefType); ok {
			return nil, fmt.Errorf("value optionals cannot wrap references; use &? instead of %s?", valueType.String())
		}
		return &semantic.OptionalType{Value: valueType}, nil
	case *ast.TupleTypeExpr:
		fields := make([]semantic.TupleField, 0, len(n.Fields))
		for _, field := range n.Fields {
			resolved, err := s.resolveTypeExpr(field.Type)
			if err != nil {
				return nil, err
			}
			fields = append(fields, semantic.TupleField{Name: field.Name, Type: resolved})
		}
		return &semantic.TupleType{Fields: fields}, nil
	case *ast.RefType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefState(n.State), StateParam: n.StateParam, Storage: semantic.RefStorage(n.Storage), StorageParam: n.StorageParam, Region: n.Region, ExplicitStorage: n.Explicit}, nil
	case *ast.FuncTypeExpr:
		params := make([]semantic.Type, 0, len(n.Params))
		for _, param := range n.Params {
			resolved, err := s.resolveTypeExpr(param)
			if err != nil {
				return nil, err
			}
			params = append(params, resolved)
		}
		ret := s.g.result.NamedTypes["void"]
		if n.Return != nil {
			resolved, err := s.resolveTypeExpr(n.Return)
			if err != nil {
				return nil, err
			}
			ret = resolved
		}
		refs := append([]ast.PermissionRef(nil), n.Permissions...)
		return &semantic.FuncType{
			Params:         params,
			Return:         ret,
			PermissionRefs: refs,
			Permissions:    permissionFamiliesFromTypeExprRefs(refs),
			Variadic:       n.Variadic,
		}, nil
	case *ast.ArrayType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		if ident, ok := n.Size.(*ast.Ident); ok {
			if bound, boundOK := s.typeMap[ident.Name]; boundOK {
				switch value := bound.(type) {
				case *semantic.ConstValueType:
					if value.Value.Kind == semantic.ConstInt {
						return &semantic.ArrayType{Elem: elem, Size: fmt.Sprintf("%d", value.Value.Int), HasConstSize: true, ConstSize: value.Value.Int}, nil
					}
				case *semantic.ConstParamType:
					return &semantic.ArrayType{Elem: elem, Size: ident.Name, ConstParam: ident.Name}, nil
				}
			}
		}
		size, err := s.evalConstIntExpr(n.Size)
		if err != nil {
			return nil, err
		}
		return &semantic.ArrayType{Elem: elem, Size: fmt.Sprintf("%d", size), HasConstSize: true, ConstSize: size}, nil
	case *ast.BuiltinTypeExpr:
		return s.resolveBuiltinSurfaceTypeExpr(n)
	case *ast.MutableType:
		return s.resolveTypeExpr(n.Elem)
	case *ast.TailType:
		elem, err := s.resolveTypeExpr(n.Elem)
		if err != nil {
			return nil, err
		}
		return &semantic.RefType{Elem: elem, State: semantic.RefStateNonNull, Storage: semantic.RefStorageAny}, nil
	case *ast.GenericType:
		if t, ok, err := s.resolveDynamicShapeType(n); ok || err != nil {
			return t, err
		}
		lookupName := n.Name
		if n.Name == "Builder" {
			if _, ok := s.g.result.NamedTypes["DArrayBuilder"]; ok {
				lookupName = "DArrayBuilder"
			}
		}
		if s.g != nil && s.g.result != nil && s.g.result.ResolvedTypeNames != nil {
			if canon, ok := s.g.result.ResolvedTypeNames[n]; ok {
				// Canonicalize so the generic instance name matches the analyzer's
				// (qualified) name used for inference + monomorphization keying.
				lookupName = canon
			}
		}
		base, ok := s.g.result.NamedTypes[lookupName]
		if !ok {
			return nil, fmt.Errorf("unknown type %q", n.Name)
		}
		if storeType, ok := base.(*semantic.PackedEnumStoreType); ok {
			args := make([]semantic.Type, 0, len(n.Args))
			for _, arg := range n.Args {
				resolved, err := s.resolveTypeExpr(arg)
				if err != nil {
					return nil, err
				}
				args = append(args, resolved)
			}
			if len(args) != 1 {
				return nil, fmt.Errorf("packed enum store type %q expects 1 state argument, got %d", n.Name, len(args))
			}
			return semantic.PackedEnumStoreWithState(storeType, args[0]), nil
		}
		if structType, ok := base.(*semantic.StructType); ok {
			params := structGenericParams(structType)
			if len(n.Args) != len(params) {
				return nil, fmt.Errorf("type %q expects %d arguments, got %d", n.Name, len(params), len(n.Args))
			}
			args := make([]semantic.Type, 0, len(n.Args))
			for i, arg := range n.Args {
				resolved, err := s.resolveGenericArgForParam(arg, params[i])
				if err != nil {
					return nil, err
				}
				args = append(args, resolved)
			}
			return semantic.DefaultAggregateStateType(&semantic.GenericInstanceType{Name: lookupName, Base: base, Args: args}), nil
		}
		if _, ok := base.(*semantic.OpaqueType); ok && len(n.Args) != 0 {
			return nil, fmt.Errorf("type %q expects 0 type arguments, got %d", n.Name, len(n.Args))
		}
		args := make([]semantic.Type, 0, len(n.Args))
		for _, arg := range n.Args {
			resolved, err := s.resolveTypeExpr(arg)
			if err != nil {
				return nil, err
			}
			args = append(args, resolved)
		}
		return semantic.DefaultAggregateStateType(&semantic.GenericInstanceType{Name: lookupName, Base: base, Args: args}), nil
	case *ast.GenericValueArgTypeExpr:
		value, err := s.evalConstIntExpr(n.Value)
		if err != nil {
			return nil, err
		}
		return &semantic.ConstValueType{Value: semantic.ConstValue{Kind: semantic.ConstInt, Int: value}}, nil
	default:
		return nil, fmt.Errorf("unsupported type expression %T", expr)
	}
}
func (s *functionState) resolveGenericArgForParam(expr ast.TypeExpr, param ast.GenericParam) (semantic.Type, error) {
	switch param.Kind {
	case ast.GenericParamValue:
		valueExpr, ok := expr.(*ast.GenericValueArgTypeExpr)
		if !ok {
			if named, namedOK := expr.(*ast.NamedType); namedOK {
				if s.typeMap != nil {
					if bound, boundOK := s.typeMap[named.Name]; boundOK {
						return bound, nil
					}
				}
				return &semantic.ConstParamType{Name: named.Name}, nil
			}
			return nil, fmt.Errorf("generic argument for value parameter %q must be a compile-time integer", param.Name)
		}
		value, err := s.evalConstIntExpr(valueExpr.Value)
		if err != nil {
			return nil, err
		}
		return &semantic.ConstValueType{Value: semantic.ConstValue{Kind: semantic.ConstInt, Int: value}}, nil
	case ast.GenericParamState:
		allowed := make(map[string]bool, len(param.StateCases))
		for _, name := range param.StateCases {
			allowed[name] = true
		}
		collect := func(names []string) (semantic.Type, error) {
			for _, name := range names {
				if !allowed[name] {
					return nil, fmt.Errorf("generic argument %q is not a declared state of %q", name, param.StateOwner)
				}
			}
			resolved := semantic.NewNamedStateTypeForBackend(param.StateOwner, param.StateCases, names)
			if resolved == nil {
				return nil, fmt.Errorf("named state argument for %q cannot be empty", param.StateOwner)
			}
			return resolved, nil
		}
		switch n := expr.(type) {
		case *ast.NamedType:
			return collect([]string{n.Name})
		case *ast.StateSetTypeExpr:
			return collect(n.Cases)
		default:
			resolved, err := s.resolveTypeExpr(expr)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("generic argument %q for named struct state parameter of %q must be a declared state name or union", resolved.String(), param.StateOwner)
		}
	case ast.GenericParamRegion:
		named, ok := expr.(*ast.NamedType)
		if !ok {
			return nil, fmt.Errorf("generic argument for region parameter %q must be a region name", param.Name)
		}
		if s.typeMap != nil {
			if bound, ok := s.typeMap[named.Name]; ok {
				switch bound := bound.(type) {
				case *semantic.RegionParamType:
					return bound, nil
				case *semantic.RegionValueType:
					return bound, nil
				}
			}
		}
		if binding, ok := s.lookupBinding(named.Name); ok && semantic.IsArenaValueOrRefType(binding.typ) {
			return &semantic.RegionValueType{Name: named.Name}, nil
		}
		return nil, fmt.Errorf("generic argument %q for region parameter %q must name a visible region, region parameter, or Arena value", named.Name, param.Name)
	default:
		return s.resolveTypeExpr(expr)
	}
}
func permissionFamiliesFromTypeExprRefs(refs []ast.PermissionRef) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		out = append(out, ref.Name)
	}
	return out
}
func (s *functionState) resolveErrorSetExpr(expr *ast.ErrorSetExpr) (semantic.Type, error) {
	if expr == nil || len(expr.Tags) == 0 {
		return nil, fmt.Errorf("error[...] requires at least one qualified error tag")
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		return nil, fmt.Errorf("error[Set.*, ...] is no longer supported; use error[Set, ...] or error[Set] instead")
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
	if expr.HasEllipsis {
		return s.resolveExpandedErrorFamilies(expr)
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
	t, ok := s.g.result.NamedTypes[tag.SetName]
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
func (s *functionState) resolveExpandedErrorFamilies(expr *ast.ErrorSetExpr) (semantic.Type, error) {
	familySets := map[string]*semantic.ErrorSetType{}
	fullFamilies := map[string]bool{}
	for _, tag := range expr.Tags {
		_, errSet, err := s.lookupDeclaredErrorSet(tag)
		if err != nil {
			return nil, err
		}
		if tag.Tag != "" && !errSet.HasQualifiedTag(tag.SetName, tag.Tag) {
			return nil, fmt.Errorf("error set %q has no tag %q", tag.SetName, tag.Tag)
		}
		familySets[tag.SetName] = errSet
		fullFamilies[tag.SetName] = true
	}
	return semantic.CanonicalizeErrorSetSelections(familySets, fullFamilies, nil), nil
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
		return &semantic.IDType{Tag: tag, Storage: s.g.result.NamedTypes["u32"]}, nil
	case "ptrid":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("ptrid expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		tag, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return &semantic.IDType{Tag: tag, Storage: s.g.result.NamedTypes["uintptr"]}, nil
	case "GuestVAddr", "HostPtr", "NativeMappedGuestPtr":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("%s expects 1 type argument, got %d", expr.Name, len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		space := "guest"
		if expr.Name == "HostPtr" {
			space = "host"
		} else if expr.Name == "NativeMappedGuestPtr" {
			space = "native_mapped_guest"
		}
		return &semantic.AddressSpaceType{Space: space, Elem: elem, Storage: s.g.result.NamedTypes["uintptr"]}, nil
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
		return &semantic.IDType{Tag: tag, Storage: s.g.result.NamedTypes["u32"]}, nil
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
			return &semantic.DArrayType{Elem: elem, Shape: &semantic.WildcardShape{}, SurfaceName: "darray"}, nil
		}
		return &semantic.DArrayType{Elem: elem, Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "darray"}, nil
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
		return resolveBackendDictType(keyType, valueType, "dict")
	case "str":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("str expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		resolved, err := s.resolveTypeExpr(&ast.ArrayType{
			Position: expr.Position,
			Elem:     &ast.NamedType{Position: expr.Position, Name: "u8"},
			Size:     expr.ValueArgs[0],
		})
		if err != nil {
			return nil, err
		}
		if arrayType, ok := resolved.(*semantic.ArrayType); ok {
			arrayType.SurfaceName = "str"
		}
		return resolved, nil
	case "string":
		return nil, legacyBuiltinReplacementError("string", "str")
	case "cstr":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			return nil, fmt.Errorf("cstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.DStrType{Shape: shapeFromValueExpr(expr.ValueArgs[0]), SurfaceName: "cstr"}, nil
	case "cstring":
		return nil, legacyBuiltinReplacementError("cstring", "cstr")
	case "view":
		if len(expr.TypeArgs) != 1 {
			return nil, fmt.Errorf("view expects 1 type argument, got %d", len(expr.TypeArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		viewType := &semantic.ViewType{Elem: elem}
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = exprSummary(expr.ValueArgs[0])
			viewType.End = exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return viewType, nil
	case "dview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("dview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		elem, err := s.resolveTypeExpr(expr.TypeArgs[0])
		if err != nil {
			return nil, err
		}
		return &semantic.DArrayViewType{Elem: elem, SurfaceName: "dview"}, nil
	case "packedview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("packedview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return s.resolvePackedVariantViewSurfaceTypeExpr(expr.TypeArgs[0])
	case "treeview":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 0 {
			return nil, fmt.Errorf("treeview expects 1 type argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return s.resolveTreeVariantViewSurfaceTypeExpr(expr.TypeArgs[0])
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			return nil, fmt.Errorf("sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
		}
		return &semantic.SViewType{Begin: exprSummary(expr.ValueArgs[0]), End: exprSummary(expr.ValueArgs[1])}, nil
	default:
		return nil, fmt.Errorf("unknown built-in type %q", expr.Name)
	}
}
func resolveBackendDictType(keyType semantic.Type, valueType semantic.Type, surfaceName string) (semantic.Type, error) {
	return &semantic.DictType{Key: keyType, Value: valueType, SurfaceName: surfaceName}, nil
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
	if semantic.SameType(cached, live) {
		return cached
	}
	strippedCached := semantic.StripAggregateStateType(cached)
	strippedLive := semantic.StripAggregateStateType(live)
	if cachedView, ok := strippedCached.(*semantic.TreeVariantViewType); ok && cachedView != nil && cachedView.Category != nil {
		if liveCategory, ok := strippedLive.(*semantic.TreeCategoryType); ok && liveCategory != nil && cachedView.Category.Name == liveCategory.Name {
			return cached
		}
	}
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
