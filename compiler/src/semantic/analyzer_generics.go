package semantic

import (
	"llcontext/src/ast"
)

func (a *Analyzer) resolveShapeArg(expr ast.TypeExpr) Shape {
	name, ok := shapeNameFromTypeExpr(expr)
	if !ok {
		a.errorf(expr.Pos(), "shape witness must be an identifier")
		return &NamedShape{Name: "?"}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) resolveShapeExpr(expr ast.Expr) Shape {
	name, ok := shapeNameFromValueExpr(expr)
	if !ok {
		return &NamedShape{Name: a.exprSummary(expr)}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) genericTypeAsArrayType(expr *ast.GenericType) (*ast.ArrayType, bool) {
	if len(expr.Args) != 1 {
		return nil, false
	}
	base, ok := a.namedTypes[expr.Name]
	if !ok {
		return nil, false
	}
	switch base.(type) {
	case *StructType, *OpaqueType, *PackedEnumStoreType, *TreeStoreType:
		return nil, false
	}
	sizeTypeExpr, ok := expr.Args[0].(*ast.NamedType)
	if !ok {
		return nil, false
	}
	return &ast.ArrayType{
		Position: expr.Position,
		Elem:     &ast.NamedType{Position: expr.Position, Name: expr.Name},
		Size:     &ast.Ident{Position: sizeTypeExpr.Position, Name: sizeTypeExpr.Name},
	}, true
}

func genericParamsForStructType(base *StructType) []ast.GenericParam {
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams)+1)
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	if len(base.NamedStateCases) != 0 {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), base.NamedStateCases...), StateOwner: base.Name})
	}
	return params
}

func genericParamsForFuncType(base *FuncType) []ast.GenericParam {
	if len(base.GenericParams) != 0 {
		return base.GenericParams
	}
	params := make([]ast.GenericParam, 0, len(base.TypeParams))
	for _, name := range base.TypeParams {
		params = append(params, ast.GenericParam{Kind: ast.GenericParamType, Name: name})
	}
	return params
}

func genericArgLabel(params []ast.GenericParam) string {
	if len(params) == 0 {
		return "generic arguments"
	}
	for _, param := range params {
		if param.Kind != ast.GenericParamType {
			return "generic arguments"
		}
	}
	return "type arguments"
}

func genericBindingsForParams(params []ast.GenericParam, args []Type) map[string]Type {
	if len(params) == 0 || len(args) == 0 {
		return nil
	}
	bindings := make(map[string]Type, len(params))
	for i, param := range params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		bindings[param.Name] = args[i]
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func genericBindingsForStructInstance(base *StructType, args []Type) map[string]Type {
	return genericBindingsForParams(genericParamsForStructType(base), args)
}

func (a *Analyzer) typeSatisfiesStaticInterface(candidate Type, iface *StaticInterface) bool {
	if a == nil || candidate == nil || iface == nil {
		return false
	}
	if typeParam, ok := candidate.(*TypeParamType); ok && typeParam != nil {
		boundIface, ok := a.lookupTypeParamInterface(typeParam.Name)
		return ok && boundIface != nil && boundIface.Name == iface.Name
	}
	_, ok := LookupStaticImpl(a.staticImpls, iface.Name, candidate)
	return ok
}

func (a *Analyzer) resolveGenericArgForParam(expr ast.TypeExpr, param ast.GenericParam) Type {
	switch param.Kind {
	case ast.GenericParamState:
		return a.resolveNamedStateGenericArg(expr, param)
	case ast.GenericParamRefStorage:
		switch n := expr.(type) {
		case *ast.NamedType:
			if t, ok := a.lookupRefStorageParam(n.Name); ok {
				return t
			}
		}
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStorageParamType); ok {
			return resolved
		}
		if _, ok := resolved.(*RefStorageValueType); ok {
			return resolved
		}
		a.errorf(expr.Pos(), "generic argument %q for refstorage parameter %q must be a refstorage literal or parameter", resolved, param.Name)
		return invalidType
	case ast.GenericParamRefState:
		switch n := expr.(type) {
		case *ast.NamedType:
			if t, ok := a.lookupRefStateParam(n.Name); ok {
				return t
			}
		}
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStateParamType); ok {
			return resolved
		}
		if _, ok := resolved.(*RefStateValueType); ok {
			return resolved
		}
		a.errorf(expr.Pos(), "generic argument %q for refstate parameter %q must be a refstate literal or parameter", resolved, param.Name)
		return invalidType
	default:
		resolved := a.resolveType(expr)
		if _, ok := resolved.(*RefStorageParamType); ok {
			a.errorf(expr.Pos(), "refstorage parameter %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStorageValueType); ok {
			a.errorf(expr.Pos(), "refstorage literal %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStateParamType); ok {
			a.errorf(expr.Pos(), "refstate parameter %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if _, ok := resolved.(*RefStateValueType); ok {
			a.errorf(expr.Pos(), "refstate literal %q cannot be used as a type argument", resolved)
			return invalidType
		}
		if param.InterfaceBound != "" {
			iface := a.staticInterfaces[param.InterfaceBound]
			if iface == nil {
				var ok bool
				iface, _, ok = a.lookupVisibleStaticInterface(param.InterfaceBound)
				if !ok || iface == nil {
					a.errorf(expr.Pos(), "%s", UnknownInterfaceMessage(param.InterfaceBound))
					return invalidType
				}
			}
			if !a.typeSatisfiesStaticInterface(resolved, iface) {
				a.errorf(expr.Pos(), interfaceConformanceFactMessage(resolved.String(), iface.Name, "type argument"))
				return invalidType
			}
		}
		return resolved
	}
}

func (a *Analyzer) resolveNamedStateGenericArg(expr ast.TypeExpr, param ast.GenericParam) Type {
	allowed := make(map[string]bool, len(param.StateCases))
	for _, name := range param.StateCases {
		allowed[name] = true
	}
	collect := func(names []string) Type {
		for _, name := range names {
			if !allowed[name] {
				a.errorf(expr.Pos(), "generic argument %q is not a declared state of %q", name, param.StateOwner)
				return invalidType
			}
		}
		resolved := newNamedStateType(param.StateOwner, param.StateCases, names)
		if resolved == nil {
			a.errorf(expr.Pos(), "named state argument for %q cannot be empty", param.StateOwner)
			return invalidType
		}
		return resolved
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		return collect([]string{n.Name})
	case *ast.StateSetTypeExpr:
		return collect(n.Cases)
	default:
		resolved := a.resolveType(expr)
		a.errorf(expr.Pos(), "generic argument %q for named struct state parameter of %q must be a declared state name or union", resolved, param.StateOwner)
		return invalidType
	}
}
