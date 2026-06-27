package semantic

import (
	"elisacore/src/ast"
	"sort"
	"strings"
)

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.RefinementTypeExpr:
		// docs/85: a refinement type is REPRESENTATION-ERASED — it resolves to its base type. The
		// predicates are validated here (each must name a law whose subject accepts the base type);
		// discharge (boundary check / static proof) is handled where the binding is processed.
		base := a.resolveType(n.Base)
		a.validateRefinementPreds(n, base)
		return base
	case *ast.WhereRefinementTypeExpr:
		base := a.resolveType(n.Base)
		a.validateConstantWhereRefinementPredicate(n)
		return base
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
		if a.rejectRefineAliasAsOrdinaryType(n.Pos(), n.Name) {
			return invalidType
		}
		if qualified, owner, ok := a.inaccessiblePrivateName(n.Name); ok {
			a.errorf(n.Pos(), "%s", PrivateNameMessage(qualified, owner))
		} else {
			a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
		}
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
		// "Region belongs to the container, not the reference": a `@r` on a
		// reference-to-container (`darray[T]& @r`) is pushed down onto the Elem so
		// it matches where the argument side carries it (`&v` over `darray[T] @a`
		// puts the region on the inner darray). This lets the region parameter
		// unify through the ref and lets the callee's growth resolve the threaded
		// arena. Refs to non-container values (`Arena&`) keep the region on the ref.
		if stamped, ok := stampContainerRegion(elemType, region); ok {
			return &RefType{Elem: stamped, State: RefState(n.State), Storage: RefStorage(n.Storage), Region: "", ExplicitStorage: n.Explicit}
		}
		return &RefType{Elem: elemType, State: RefState(n.State), Storage: RefStorage(n.Storage), Region: region, ExplicitStorage: n.Explicit}
	case *ast.ArrayType:
		return a.resolveArrayType(n)
	case *ast.BuiltinTypeExpr:
		resolved := a.resolveBuiltinSurfaceType(n)
		// `@r` is only meaningful on types that actually carry a region (containers and
		// region-parameterized generics). On a scalar builtin like `i32 @r` the region
		// was silently dropped; reject it so the notation means one thing everywhere.
		if n.Region != "" && !IsInvalidType(resolved) {
			if !typeCanCarryRegion(resolved) {
				a.errorf(n.Pos(), "region annotation `@%s` applies to containers, references, and region-parameterized types; %s cannot carry a region", n.Region, resolved)
			} else if !a.regionQualifierDefined(n.Region) {
				// An unknown region name silently bypassed the nested-region escape check
				// (`darray[u8] @misnamed = inner_value` was accepted where `@outer` is
				// rejected). Diagnose it like RefType already does.
				a.errorf(n.Pos(), "unknown region qualifier %q", n.Region)
			}
		}
		return resolved
	case *ast.FuncTypeExpr:
		ptypes := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			ptypes = append(ptypes, a.resolveType(param))
		}
		retExpr := n.Return
		permissionRefs := n.Permissions
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
		// Parametric refinement alias (`type SlotIndex[cap] = u32 is InRange[0, cap]`): the alias is NOT
		// in namedTypes, but its base type IS concrete. Resolve to the base so callers see a concrete type.
		if rt := a.lookupParametricAliasRefinement(n.Name, n.Args); rt != nil {
			return a.resolveType(rt.Base)
		}
		args := make([]Type, 0, len(n.Args))
		surfaceName := n.Name
		lookupName := n.Name
		base, canonical, ok := a.lookupVisibleType(lookupName)
		if !ok {
			if a.rejectRefineAliasAsOrdinaryType(n.Pos(), lookupName) {
				return invalidType
			}
			if qualified, owner, privateHit := a.inaccessiblePrivateName(lookupName); privateHit {
				a.errorf(n.Pos(), "%s", PrivateNameMessage(qualified, owner))
			} else {
				a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.Name))
			}
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
		// A `@r` provenance on a generic instance (e.g. `Box[i64] @r`) must name a region
		// that actually exists; an unknown name silently bypassed the escape check.
		if n.Region != "" && !a.regionQualifierDefined(n.Region) {
			a.errorf(n.Pos(), "unknown region qualifier %q", n.Region)
		}
		switch base := base.(type) {
		case *PackedEnumStoreType:
			if len(n.Args) != 1 {
				a.errorf(n.Pos(), "packed enum store type %q expects 1 state argument, got %d", n.Name, len(args))
				return invalidType
			}
			args = append(args, a.resolveType(n.Args[0]))
			return PackedEnumStoreWithState(base, args[0])
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
			return DefaultAggregateStateType(&GenericInstanceType{Name: lookupName, Base: base, Args: args, Region: n.Region})
		case *OpaqueType:
			if len(n.Args) != 0 {
				a.errorf(n.Pos(), "type %q expects 0 type arguments, got %d", n.Name, len(n.Args))
				return invalidType
			}
			return &GenericInstanceType{Name: surfaceName, Base: base, Args: args, Region: n.Region}
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

func (a *Analyzer) validateConstantWhereRefinementPredicate(n *ast.WhereRefinementTypeExpr) {
	if a == nil || n == nil || n.Predicate == nil || exprContainsIdent(n.Predicate) {
		return
	}
	t := a.analyzeExpr(n.Predicate)
	if t != nil && !IsBoolType(t) {
		// Emit the base type so the user knows what subject the predicate applies to.
		baseDesc := "unknown"
		if n.Base != nil {
			if bt := a.resolveType(n.Base); bt != nil {
				baseDesc = typeString(bt)
			}
		}
		a.errorf(n.Predicate.Pos(),
			"where refinement predicate must be bool, got %s (subject type is %s); write a bool expression such as `n where n > 0`",
			typeString(t), baseDesc)
	}
}

func exprContainsIdent(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return false
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return exprContainsIdent(n.Inner)
	case *ast.UnaryExpr:
		return exprContainsIdent(n.Operand)
	case *ast.BinaryExpr:
		return exprContainsIdent(n.Left) || exprContainsIdent(n.Right)
	case *ast.FieldExpr:
		return exprContainsIdent(n.Object)
	case *ast.IndexExpr:
		return exprContainsIdent(n.Object) || exprContainsIdent(n.Index) || exprContainsIdent(n.Index2) || exprContainsIdent(n.Fallback)
	case *ast.SliceExpr:
		return exprContainsIdent(n.Object) || exprContainsIdent(n.Start) || exprContainsIdent(n.End)
	case *ast.CallExpr:
		if exprContainsIdent(n.Func) {
			return true
		}
		for _, arg := range n.Args {
			if exprContainsIdent(arg) {
				return true
			}
		}
	}
	return false
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
		a.errorf(expr.Pos(), "error[Set.*, ...] is no longer supported; use error[Set] instead")
		return invalidType
	}
	if expr.HasEllipsis {
		// `error[Tags, ...]` used to round the listed selections up to their whole
		// declared families — a CLOSED set, despite reading as "open to more". A bare
		// set name already means the whole family, so the suffix added nothing but
		// confusion; it is no longer supported.
		a.errorf(expr.Pos(), "the `, ...` suffix in error[...] is no longer supported; name the full sets directly (a bare set name already means the whole family)")
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
	// Partition: bare names that resolve to in-scope error-set generic params
	// become the set's Params component; everything else is the concrete part.
	// `error[R]` is the pure opaque placeholder; `error[R, Timeout]` carries
	// both; `error[R, S]` unions two params.
	var paramNames []string
	concreteExprTags := make([]ast.ErrorTagExpr, 0, len(expr.Tags))
	seenParams := map[string]bool{}
	for _, tag := range expr.Tags {
		if tag.Tag == "" && !tag.Family && a.lookupErrorSetParam(tag.SetName) {
			if seenParams[tag.SetName] {
				a.errorf(tag.Position, "duplicate error-set parameter %q in error[...]", tag.SetName)
				return invalidType
			}
			seenParams[tag.SetName] = true
			paramNames = append(paramNames, tag.SetName)
			continue
		}
		concreteExprTags = append(concreteExprTags, tag)
	}
	if len(paramNames) != 0 {
		if len(concreteExprTags) == 0 {
			if len(paramNames) == 1 {
				// `error[R]`: an opaque placeholder, bound at each call site.
				return &ErrorSetType{Name: paramNames[0], Params: paramNames}
			}
			return &ErrorSetType{Name: errorSetNameWithParams("", 0, paramNames), Params: paramNames}
		}
		concrete := a.resolveErrorSetExpr(&ast.ErrorSetExpr{Position: expr.Position, Tags: concreteExprTags})
		concreteSet, ok := concrete.(*ErrorSetType)
		if !ok || concreteSet == nil {
			return invalidType
		}
		if concreteSet.HasParams() {
			a.errorf(expr.Pos(), "internal error: nested error-set params in error[...]")
			return invalidType
		}
		mixed := &ErrorSetType{
			Tags:     append([]string(nil), concreteSet.Tags...),
			Payloads: concreteSet.Payloads,
			Params:   paramNames,
		}
		mixed.Name = errorSetNameWithParams(concreteSet.Name, len(mixed.Tags), paramNames)
		return mixed
	}
	// Normalize: a bare `Name` that is not a declared set resolves as a single
	// variant searched across all error sets (family-qualified on conflict). An
	// explicit `*Set` stays a whole family. Qualified `Set.Tag` is untouched.
	tags := make([]ast.ErrorTagExpr, 0, len(expr.Tags))
	for _, tag := range expr.Tags {
		normalized, ok := a.normalizeErrorTag(tag)
		if !ok {
			return invalidType
		}
		tags = append(tags, normalized)
	}
	if len(tags) == 1 && tags[0].Tag == "" {
		_, errSet := a.lookupDeclaredErrorSet(tags[0])
		if errSet == nil {
			return invalidType
		}
		return errSet
	}
	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range tags {
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

// errorSetTypeQuiet looks up name as a declared error set without reporting an
// error if it is missing or not an error set.
func (a *Analyzer) errorSetTypeQuiet(name string) (*ErrorSetType, bool) {
	t, ok := a.namedTypes[name]
	if !ok {
		if resolved, _, found := a.lookupVisibleType(name); found {
			t = resolved
			ok = true
		}
	}
	if !ok {
		return nil, false
	}
	errSet, ok := t.(*ErrorSetType)
	return errSet, ok
}

// findErrorVariantOwners returns the canonical names of declared error sets that
// contain a variant with the given short name, sorted for stable diagnostics.
func (a *Analyzer) findErrorVariantOwners(shortName string) []string {
	seen := map[string]bool{}
	var owners []string
	for _, t := range a.namedTypes {
		errSet, ok := t.(*ErrorSetType)
		if !ok || errSet == nil || errSet.HasParams() {
			continue
		}
		for _, qtag := range errSet.Tags {
			if ErrorTagShortName(qtag) == shortName {
				if !seen[errSet.Name] {
					seen[errSet.Name] = true
					owners = append(owners, errSet.Name)
				}
				break
			}
		}
	}
	sort.Strings(owners)
	return owners
}

// normalizeErrorTag canonicalizes one error[...] item: qualified `Set.Tag` and
// explicit `*Set` pass through; a bare `Name` is a whole family if Name is a
// declared set, otherwise the single variant `Name` searched across all sets
// (an ambiguous bare variant must be qualified `Set.Name`).
func (a *Analyzer) normalizeErrorTag(tag ast.ErrorTagExpr) (ast.ErrorTagExpr, bool) {
	if tag.Tag != "" {
		return tag, true
	}
	if _, ok := a.errorSetTypeQuiet(tag.SetName); ok {
		return tag, true
	}
	if tag.Family {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return tag, false
	}
	owners := a.findErrorVariantOwners(tag.SetName)
	switch len(owners) {
	case 0:
		a.errorf(tag.Position, "unknown error set or variant %q", tag.SetName)
		return tag, false
	case 1:
		return ast.ErrorTagExpr{Position: tag.Position, SetName: owners[0], Tag: tag.SetName}, true
	default:
		a.errorf(tag.Position, "error variant %q is ambiguous across sets %s; qualify it as %s.%s", tag.SetName, strings.Join(owners, ", "), owners[0], tag.SetName)
		return tag, false
	}
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
