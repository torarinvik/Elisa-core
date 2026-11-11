package semantic

import (
	"elisacore/src/ast"
	"strings"
)

func (a *Analyzer) resolvedTypeName(expr ast.TypeExpr) string {
	if a == nil || expr == nil {
		return ""
	}
	if name := a.resolvedTypeNames[expr]; name != "" {
		return name
	}
	if a.parallel != nil {
		return a.parallel.resolvedTypeNamesBase[expr]
	}
	return ""
}

func (a *Analyzer) recordResolvedTypeName(expr ast.TypeExpr, name string) {
	if a == nil || expr == nil || name == "" {
		return
	}
	if a.resolvedTypeNames == nil {
		a.resolvedTypeNames = make(map[ast.TypeExpr]string)
	}
	a.resolvedTypeNames[expr] = name
}

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
		if a.rejectDotModulePath(n.Pos(), n.Spelling) {
			return invalidType
		}
		switch n.Name {
		case "cstr":
			return &CStrType{Shape: &WildcardShape{}, SurfaceName: "cstr", Region: n.Region}
		case "sview":
			return &SViewType{Region: n.Region}
		case "dstr":
			// dstr is an owned dynamic string: the u8 specialization of darray
			// (docs/26). It shares darray's {ptr, count, capacity} representation
			// and every darray operation; the distinct name carries string intent.
			return &DArrayType{Elem: a.namedTypes["u8"], Shape: &WildcardShape{}, SurfaceName: "dstr"}
		}
		a.maybeRejectRuntimeCarrierTypeUse(n.Pos(), n.Name)
		if t, ok := a.lookupTypeParamAt(n); ok {
			return t
		}
		if t, ok := a.lookupInterfaceAssocTypeAt(n); ok {
			if a.contextualTypeBindings != nil {
				a.contextualTypeBindings[n] = t
			}
			return t
		}
		if t, canonical, ok := a.lookupVisibleType(n.Name); ok {
			// Record the resolved canonical (possibly namespace/using/import-qualified)
			// name so the backend and interpreter consume the analyzer's resolution
			// instead of re-resolving the bare name without namespace context.
			if a.resolvedTypeNames != nil && canonical != "" && canonical != n.Name {
				a.recordResolvedTypeName(n, canonical)
			}
			if n.Region != "" {
				a.errorf(n.Pos(), "region annotation `@%s` is only valid on a function return type; named values do not carry an independent region", n.Region)
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
			// The name AS WRITTEN: the internal key flattens every `::` to a `.`, so a
			// qualified type was quoted back in a spelling the user never typed (and
			// which no longer parses).
			if iface, _, ok := a.lookupVisibleStaticInterface(n.Name); ok && iface != nil {
				a.errorf(n.Pos(), "protocol %q is a constraint, not a concrete value type; use it as a function parameter constraint or use a concrete implementing type", n.SourceSpelling())
			} else {
				a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.SourceSpelling()))
			}
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
		tuple := &TupleType{Fields: fields}
		if n.Region == "" {
			return tuple
		}
		if !a.regionQualifierDefined(n.Region) {
			a.errorf(n.Pos(), "unknown region qualifier %q", n.Region)
			return tuple
		}
		stamped, carriesRegion, conflicts := stampAggregateRegion(tuple, n.Region)
		if conflicts {
			a.errorf(n.Pos(), "tuple region annotation @%s conflicts with a field tied to a different region", n.Region)
			return tuple
		}
		if !carriesRegion {
			a.errorf(n.Pos(), "tuple region annotation @%s requires at least one region-carrying field", n.Region)
			return tuple
		}
		return stamped
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
		permissions := permissionFamiliesFromRefs(resolvedPermissionRefs)
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
			// Carry the `lmut T` linear-mutable bit onto the resolved reference so the linear
			// checker can spot it at every call site (codegen is unchanged — same mutable ref).
			if n.Linear {
				cloned.Linear = true
			}
			return cloned
		}
		// `mutable view[T]` is a writable borrow (like `&mut [T]`); mark the view mutable so
		// indexed writes and mutable-ref iteration through it are permitted.
		if view, ok := elemType.(*ViewType); ok {
			cloned := *view
			cloned.Mutable = true
			return &cloned
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
		if a.rejectDotModulePath(n.Pos(), n.Spelling) {
			return invalidType
		}
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
				a.errorf(n.Pos(), "%s", UnknownTypeMessage(n.SourceSpelling()))
			}
			return invalidType
		}
		if a.resolvedTypeNames != nil && canonical != "" && canonical != lookupName {
			a.recordResolvedTypeName(n, canonical)
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
			if (base.Name == "atomic" || base.Name == "AtomicSlot") && len(args) == 1 {
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
	t := analyzeConstantWherePredicate(a, n.Predicate)
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
