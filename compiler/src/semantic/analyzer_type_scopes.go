package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) withTypeParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &TypeParamType{Name: name}
		}
	}
	a.typeParamScopes = append(a.typeParamScopes, bindings)
	fn()
	a.typeParamScopes = a.typeParamScopes[:len(a.typeParamScopes)-1]
}

func (a *Analyzer) withGenericParams(params []ast.GenericParam, args []Type, fn func()) {
	if len(params) == 0 {
		fn()
		return
	}
	typeNames := make([]string, 0)
	typeArgs := make([]Type, 0)
	typeInterfaces := make(map[string]*StaticInterface)
	constNames := make([]string, 0)
	constArgs := make([]Type, 0)
	errorSetNames := make([]string, 0)
	for i, param := range params {
		var arg Type
		if i < len(args) {
			arg = args[i]
		}
		switch param.Kind {
		case ast.GenericParamType:
			typeNames = append(typeNames, param.Name)
			typeArgs = append(typeArgs, arg)
			if param.InterfaceBound != "" {
				iface, _, ok := a.lookupVisibleStaticInterface(param.InterfaceBound)
				if !ok || iface == nil {
					a.errorf(param.Position, "%s", UnknownInterfaceMessage(param.InterfaceBound))
				} else {
					typeInterfaces[param.Name] = iface
				}
			}
		case ast.GenericParamErrorSet:
			errorSetNames = append(errorSetNames, param.Name)
		case ast.GenericParamValue:
			constNames = append(constNames, param.Name)
			if arg != nil {
				constArgs = append(constArgs, arg)
			} else {
				constArgs = append(constArgs, &ConstParamType{Name: param.Name, ValueType: a.resolveType(&ast.NamedType{Position: param.Position, Name: param.InterfaceBound})})
			}
		}
	}
	a.withTypeParamInterfaces(typeInterfaces, func() {
		a.withTypeParams(typeNames, typeArgs, func() {
			a.withErrorSetParams(errorSetNames, func() {
				a.withConstParams(constNames, constArgs, fn)
			})
		})
	})
}

func (a *Analyzer) withConstParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &ConstParamType{Name: name}
		}
	}
	a.constParamScopes = append(a.constParamScopes, bindings)
	fn()
	a.constParamScopes = a.constParamScopes[:len(a.constParamScopes)-1]
}

func (a *Analyzer) lookupConstParam(name string) (Type, bool) {
	for i := len(a.constParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.constParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupTypeParam(name string) (Type, bool) {
	for i := len(a.typeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.typeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupShapeParam(name string) (Shape, bool) {
	for i := len(a.shapeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.shapeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) withPermissionParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]bool, len(names))
	for _, name := range names {
		bindings[name] = true
	}
	a.permissionParamScopes = append(a.permissionParamScopes, bindings)
	fn()
	a.permissionParamScopes = a.permissionParamScopes[:len(a.permissionParamScopes)-1]
}

func (a *Analyzer) lookupPermissionParam(name string) bool {
	for i := len(a.permissionParamScopes) - 1; i >= 0; i-- {
		if a.permissionParamScopes[i][name] {
			return true
		}
	}
	return false
}

func (a *Analyzer) withErrorSetParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]bool, len(names))
	for _, name := range names {
		bindings[name] = true
	}
	a.errorSetParamScopes = append(a.errorSetParamScopes, bindings)
	fn()
	a.errorSetParamScopes = a.errorSetParamScopes[:len(a.errorSetParamScopes)-1]
}

func (a *Analyzer) lookupErrorSetParam(name string) bool {
	for i := len(a.errorSetParamScopes) - 1; i >= 0; i-- {
		if a.errorSetParamScopes[i][name] {
			return true
		}
	}
	return false
}

// errorSetParamNames extracts the error-set generic parameter names from a
// generic-param list (error-set params ride the GenericParams slice).
func errorSetParamNames(params []ast.GenericParam) []string {
	var out []string
	for _, p := range params {
		if p.Kind == ast.GenericParamErrorSet {
			out = append(out, p.Name)
		}
	}
	return out
}

func (a *Analyzer) permissionParamsInRefs(refs []ast.PermissionRef) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Member != "" || !a.lookupPermissionParam(ref.Name) || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		out = append(out, ref.Name)
	}
	return out
}

func (a *Analyzer) withRegionParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]bool, len(names))
	for _, name := range names {
		bindings[name] = true
	}
	a.regionParamScopes = append(a.regionParamScopes, bindings)
	fn()
	a.regionParamScopes = a.regionParamScopes[:len(a.regionParamScopes)-1]
}

func (a *Analyzer) lookupRegionParam(name string) bool {
	for i := len(a.regionParamScopes) - 1; i >= 0; i-- {
		if a.regionParamScopes[i][name] {
			return true
		}
	}
	return false
}
