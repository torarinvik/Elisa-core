package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "cstr":
			return &DStrType{Shape: &WildcardShape{}, SurfaceName: "cstr"}
		case "sview":
			return &SViewType{}
		case "dstr":
			// dstr is an owned dynamic string: the u8 specialization of darray
			// (docs/26). It shares darray's {ptr, count, capacity} representation
			// and every darray operation; the distinct name carries string intent.
			return &DArrayType{Elem: a.namedTypes["u8"], Shape: &WildcardShape{}, SurfaceName: "dstr"}
		case "DStr":
			a.errorLegacyBuiltinReplacement(n.Pos(), "DStr", "cstr")
			return invalidType
		case "cstring":
			a.errorLegacyBuiltinReplacement(n.Pos(), "cstring", "cstr")
			return invalidType
		}
		a.maybeRejectRuntimeCarrierTypeUse(n.Pos(), n.Name)
		if t, ok := a.lookupTypeParam(n.Name); ok {
			return t
		}
		if t, ok := a.lookupInterfaceAssocType(n.Name); ok {
			return t
		}
		if t, canonical, ok := a.lookupVisibleType(n.Name); ok {
			// Record the resolved canonical (possibly namespace/using/import-qualified)
			// name so the backend and interpreter consume the analyzer's resolution
			// instead of re-resolving the bare name without namespace context.
			if a.resolvedTypeNames != nil && canonical != "" && canonical != n.Name {
				a.resolvedTypeNames[n] = canonical
			}
			return DefaultStatefulType(t)
		}
		if t, ok := a.resolveProjectedAssociatedType(n); ok {
			return t
		}
		if t, ok := a.resolveNamedVariantWitnessType(n); ok {
			return t
		}
		a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
		return invalidType
	case *ast.StateSetTypeExpr:
		a.errorf(n.Pos(), "state unions like %q are only valid as named struct state arguments", strings.Join(n.Cases, " | "))
		return invalidType
	case *ast.AggregateStateTypeExpr:
		baseType := StripAggregateStateType(a.resolveType(n.Base))
		count := AggregateStateParamCount(baseType)
		if count == 0 {
			a.errorf(n.Pos(), "type %q does not declare an aggregate state parameter", baseType)
			return invalidType
		}
		states := astAggregateStates(n)
		if len(states) != count {
			a.errorf(n.Pos(), "type %q expects %d aggregate state arguments, got %d", baseType, count, len(states))
			return invalidType
		}
		return cloneAggregateStateWithBase(baseType, states)
	case *ast.RefStorageLiteralTypeExpr:
		return &RefStorageValueType{Storage: RefStorage(n.Storage)}
	case *ast.ErrorSetExpr:
		return a.resolveErrorSetExpr(n)
	case *ast.ErrorUnionTypeExpr:
		valueType := a.resolveType(n.Value)
		errorType := a.resolveType(n.Errors)
		errSet, ok := errorType.(*ErrorSetType)
		if !ok {
			a.errorf(n.Pos(), "error union expects an error set on the right-hand side, got %s", errorType)
			return invalidType
		}
		return &ErrorUnionType{Value: valueType, Errors: errSet}
	case *ast.OptionalTypeExpr:
		valueType := a.resolveType(n.Value)
		if isVoidType(valueType) {
			a.errorf(n.Pos(), "value optionals cannot wrap void")
			return invalidType
		}
		if _, ok := valueType.(*RefType); ok {
			a.errorf(n.Pos(), "value optionals cannot wrap references; use &? instead of %s?", valueType)
			return invalidType
		}
		return &OptionalType{Value: valueType}
	case *ast.TupleTypeExpr:
		fields := make([]TupleField, 0, len(n.Fields))
		for _, field := range n.Fields {
			fields = append(fields, TupleField{Name: field.Name, Type: a.resolveType(field.Type)})
		}
		return &TupleType{Fields: fields}
	case *ast.RefType:
		region := n.Region
		if region != "" && !a.regionQualifierDefined(region) {
			a.errorf(n.Pos(), "unknown region qualifier %q", region)
		}
		elemType := a.resolveType(n.Elem)
		if a.containsAffineHandleValues(elemType, map[string]bool{}) && !isBorrowableAffineOwnerType(elemType) {
			a.errorf(n.Pos(), "references to values containing linear handles are not supported; got %s&", elemType)
		}
		return &RefType{Elem: elemType, State: RefState(n.State), Storage: RefStorage(n.Storage), Region: region, ExplicitStorage: n.Explicit}
	case *ast.ArrayType:
		return a.resolveArrayType(n)
	case *ast.BuiltinTypeExpr:
		return a.resolveBuiltinSurfaceType(n)
	case *ast.FuncTypeExpr:
		ptypes := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			ptypes = append(ptypes, a.resolveType(param))
		}
		expandedImplicitParams, implicitNames := a.expandImplicitParamDecls(nil, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, "func")
		for _, param := range expandedImplicitParams {
			ptypes = append(ptypes, a.resolveType(param.Type))
		}
		retExpr, permissionRefs := a.expandSignatureEffects(n.Return, n.Permissions, n.EffectAlias, n.EffectAliasPos, n.Effects)
		resolvedPermissionRefs := a.resolvePermissionRefs(permissionRefs, true)
		permissions := a.resolvePermissionFamilies(permissionRefs, true)
		retType := a.namedTypes["void"]
		if retExpr != nil {
			retType = a.resolveType(retExpr)
		}
		return &FuncType{
			Name:                      "func",
			UsedPermissionParams:      append([]string(nil), a.permissionParamsInRefs(permissionRefs)...),
			DeclaredPermissionRefs:    append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			DeclaredPermissions:       append([]string(nil), permissions...),
			PermissionRefs:            append([]ast.PermissionRef(nil), resolvedPermissionRefs...),
			Permissions:               permissions,
			Params:                    ptypes,
			ExplicitParamCount:        len(n.Params),
			ExplicitParamNames:        nil,
			ExplicitParamDefaultExprs: make([]ast.Expr, len(n.Params)),
			ExplicitParamHasDefault:   make([]bool, len(n.Params)),
			ImplicitParamNames:        implicitNames,
			Return:                    retType,
			Variadic:                  n.Variadic,
		}
	case *ast.MutableType:
		elemType := a.resolveType(n.Elem)
		if ref, ok := elemType.(*RefType); ok {
			cloned := cloneRefType(ref)
			cloned.Mutable = true
			return cloned
		}
		return elemType
	case *ast.OwnedType:
		// `owned T` resolves to T; the affine must-consume obligation is recorded
		// on the binding/parameter (registerOwnedStoreOwner), not on the value's
		// type, so plain `T` values keep their existing semantics untouched.
		return a.resolveType(n.Elem)
	case *ast.TailType:
		elemType := a.resolveType(n.Elem)
		if a.containsAffineHandleValues(elemType, map[string]bool{}) && !isBorrowableAffineOwnerType(elemType) {
			a.errorf(n.Pos(), "references to values containing linear handles are not supported; got %s&", elemType)
		}
		return &RefType{Elem: elemType, State: RefStateNonNull, Storage: RefStorageAny}
	case *ast.GenericType:
		if shaped, ok := a.resolveDynamicShapeType(n); ok {
			return shaped
		}
		if arrayExpr, ok := a.genericTypeAsArrayType(n); ok {
			return a.resolveArrayType(arrayExpr)
		}
		a.maybeRejectRuntimeCarrierTypeUse(n.Pos(), n.Name)
		args := make([]Type, 0, len(n.Args))
		surfaceName := n.Name
		lookupName := n.Name
		if n.Name == "Builder" {
			if _, _, ok := a.lookupVisibleType("DArrayBuilder"); ok {
				lookupName = "DArrayBuilder"
			}
		}
		base, canonical, ok := a.lookupVisibleType(lookupName)
		if !ok {
			a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
			return invalidType
		}
		if a.resolvedTypeNames != nil && canonical != "" && canonical != lookupName {
			a.resolvedTypeNames[n] = canonical
		}
		// Use the canonical (namespace-qualified) name for the generic instance so a
		// reference spelled bare inside the module and one spelled qualified/imported
		// resolve to the SAME instance type (required for inference + monomorphization
		// to unify). For top-level types canonical == lookupName, so this is a no-op.
		if canonical != "" {
			lookupName = canonical
		}
		switch base := base.(type) {
		case *PackedEnumStoreType:
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "packed enum store type %q expects 1 state argument, got %d", n.Name, len(args))
				return invalidType
			}
			args = append(args, a.resolveType(n.Args[0]))
			return PackedEnumStoreWithState(base, args[0])
		case *TreeStoreType:
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "tree store type %q expects 1 state argument, got %d", n.Name, len(args))
				return invalidType
			}
			args = append(args, a.resolveType(n.Args[0]))
			return TreeStoreWithState(base, args[0])
		case *StructType:
			params := genericParamsForStructType(base)
			if len(n.Args) != len(params) {
				a.errorf(n.Pos(), "type %q expects %d %s, got %d", n.Name, len(params), genericArgLabel(params), len(n.Args))
				return invalidType
			}
			for i, arg := range n.Args {
				args = append(args, a.resolveGenericArgForParam(arg, params[i]))
			}
			if base.Builtin && base.Name == "atomic" && len(args) == 1 {
				if !a.typeStructurallyAtomicSafe(args[0], map[string]bool{}) {
					a.errorf(n.Pos(), "atomic payload type must satisfy atomic_safe(T), got %s", args[0])
					return invalidType
				}
			}
			if lookupName == "Flags" && len(args) == 1 {
				flagArg := StripAggregateStateType(args[0])
				if _, ok := flagArg.(*TypeParamType); !ok {
					if _, ok := flagArg.(*ConstEnumType); !ok {
						a.errorf(n.Pos(), "Flags[T] expects a const enum type argument, got %s", args[0])
						return invalidType
					}
				}
			}
			return DefaultAggregateStateType(&GenericInstanceType{Name: lookupName, Base: base, Args: args})
		case *OpaqueType:
			if len(n.Args) != 0 {
				a.errorf(n.Pos(), "type %q expects 0 type arguments, got %d", n.Name, len(n.Args))
				return invalidType
			}
			return &GenericInstanceType{Name: surfaceName, Base: base, Args: args}
		default:
			a.errorf(n.Pos(), "type %q cannot be used with generic arguments", n.Name)
			return invalidType
		}
	case *ast.GenericValueArgTypeExpr:
		value, ok := a.evalConstExpr(n.Value)
		if !ok {
			a.errorf(n.Pos(), "generic value argument must be compile-time constant")
			return invalidType
		}
		return &ConstValueType{Value: value}
	default:
		return invalidType
	}
}

func (a *Analyzer) resolveErrorSetExpr(expr *ast.ErrorSetExpr) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.Tags) == 0 {
		a.errorf(expr.Pos(), "error[...] requires at least one qualified error tag")
		return invalidType
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		a.errorf(expr.Pos(), "error[Set.*, ...] is no longer supported; use error[Set, ...] or error[Set] instead")
		return invalidType
	}
	if containsWildcardErrorTag(expr.Tags) {
		if len(expr.Tags) != 1 {
			a.errorf(expr.Pos(), "error[Set.*] cannot be mixed with explicit tags")
			return invalidType
		}
		a.errorf(expr.Pos(), "error[Set.*] is no longer supported; use error[Set] instead")
		return invalidType
	}
	if len(expr.Tags) == 1 && expr.Tags[0].Tag == "" {
		if !expr.HasEllipsis && a.lookupErrorSetParam(expr.Tags[0].SetName) {
			// `error[R]` where R is a generic error-set parameter: an opaque
			// placeholder, bound to a concrete set at each call site.
			return &ErrorSetType{Name: expr.Tags[0].SetName, Param: true}
		}
		_, errSet := a.lookupDeclaredErrorSet(expr.Tags[0])
		if errSet == nil {
			return invalidType
		}
		return errSet
	}
	if expr.HasEllipsis {
		return a.resolveExpandedErrorFamilies(expr)
	}

	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := SplitErrorTagName(qualifiedTag)
					a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
					return invalidType
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
			return invalidType
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags)
}

func (a *Analyzer) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *ErrorSetType) {
	t, ok := a.namedTypes[tag.SetName]
	if !ok {
		var resolved Type
		resolved, _, ok = a.lookupVisibleType(tag.SetName)
		if ok {
			t = resolved
		}
	}
	if !ok {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return "", nil
	}
	errSet, ok := t.(*ErrorSetType)
	if !ok {
		a.errorf(tag.Position, "%q is not an error set", tag.SetName)
		return "", nil
	}
	return tag.SetName, errSet
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

func (a *Analyzer) resolveExpandedErrorFamilies(expr *ast.ErrorSetExpr) Type {
	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		if tag.Tag != "" && !errSet.HasQualifiedTag(tag.SetName, tag.Tag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		familySets[tag.SetName] = errSet
		fullFamilies[tag.SetName] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, nil)
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
