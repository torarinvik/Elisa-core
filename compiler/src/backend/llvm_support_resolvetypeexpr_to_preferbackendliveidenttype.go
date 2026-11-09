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
	case *ast.RefinementTypeExpr:
		// docs/85: refinement types are representation-erased — codegen sees the base type.
		return s.resolveTypeExpr(n.Base)
	case *ast.WhereRefinementTypeExpr:
		// docs/95/109: anonymous and named `refine` aliases carry proof
		// metadata only. Semantic analysis has already discharged the
		// predicate; native layout and ABI are exactly the base type.
		return s.resolveTypeExpr(n.Base)
	case *ast.NamedType:
		switch n.Name {
		case "cstr":
			return &semantic.CStrType{Shape: &semantic.WildcardShape{}, SurfaceName: "cstr", Region: substituteRegionName(n.Region, s.typeMap)}, nil
		case "sview":
			return &semantic.SViewType{Region: substituteRegionName(n.Region, s.typeMap)}, nil
		case "dstr":
			return &semantic.DArrayType{Elem: s.g.namedType("u8"), Shape: &semantic.WildcardShape{}, SurfaceName: "dstr", Region: substituteRegionName(n.Region, s.typeMap)}, nil
		}
		if bound, ok := s.typeMap[n.Name]; ok {
			return bound, nil
		}
		if s.g != nil && s.g.result != nil {
			if bound, ok := s.g.semanticReadView().LookupContextualTypeBindings(n); ok {
				return substituteType(bound, s.typeMap, s.g.result.StaticImpls), nil
			}
		}
		// Prefer the analyzer's recorded resolution (handles namespace / using /
		// import qualification that the bare-name lookup below cannot see).
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnResolvedTypeNamesMap() {
			if canonical, ok := s.g.semanticReadView().LookupResolvedTypeNames(n); ok {
				if t, ok := s.g.lookupNamedType(canonical); ok {
					return semantic.DefaultStatefulType(t), nil
				}
			}
		}
		if t, ok := s.g.lookupNamedType(n.Name); ok {
			return semantic.DefaultStatefulType(t), nil
		}
		if t, handled, err := s.resolveStaticAssociatedType(n.Name); handled {
			if err != nil {
				return nil, err
			}
			return t, nil
		}
		// A bare dotted packed-enum-variant name like `Expr.Lit` is a refined packed witness — the
		// bare-form analogue of `packedview[Expr.Lit]`, matching the bare tree-variant surface.
		if t, ok := s.resolveBarePackedVariantWitnessType(n.Name); ok {
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
		return &semantic.RefType{Elem: elem, State: semantic.RefState(n.State), Storage: semantic.RefStorage(n.Storage), Region: substituteRegionName(n.Region, s.typeMap), ExplicitStorage: n.Explicit}, nil
	case *ast.FuncTypeExpr:
		params := make([]semantic.Type, 0, len(n.Params))
		for _, param := range n.Params {
			resolved, err := s.resolveTypeExpr(param)
			if err != nil {
				return nil, err
			}
			params = append(params, resolved)
		}
		ret := s.g.namedType("void")
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
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().HasOwnResolvedTypeNamesMap() {
			if canon, ok := s.g.semanticReadView().LookupResolvedTypeNames(n); ok {
				// Canonicalize so the generic instance name matches the analyzer's
				// (qualified) name used for inference + monomorphization keying.
				lookupName = canon
			}
		}
		base, ok := s.g.lookupNamedType(lookupName)
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
			return semantic.DefaultAggregateStateType(&semantic.GenericInstanceType{Name: lookupName, Base: base, Args: args, Region: substituteRegionName(n.Region, s.typeMap)}), nil
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
		return semantic.DefaultAggregateStateType(&semantic.GenericInstanceType{Name: lookupName, Base: base, Args: args, Region: substituteRegionName(n.Region, s.typeMap)}), nil
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
		// A polymorphic function body may contain a local annotation such as
		// `result: ParseResult[r]`. Its region parameter is part of the active
		// function signature, not a value binding and not necessarily a concrete
		// monomorphization entry in typeMap. Preserve the binder so backend type
		// resolution agrees with the semantic analyzer.
		if s.fnType != nil {
			for _, regionParam := range s.fnType.RegionParams {
				if regionParam == named.Name {
					return &semantic.RegionParamType{Name: named.Name}, nil
				}
			}
		}
		if binding, ok := s.lookupBinding(named.Name); ok && semantic.IsArenaValueOrRefType(binding.typ) {
			return &semantic.RegionValueType{Name: named.Name}, nil
		}
		if s.g != nil && s.g.result.IsProgramRegion(named.Name) {
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
