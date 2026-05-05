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

func (a *Analyzer) withRefStorageParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &RefStorageParamType{Name: name}
		}
	}
	a.refStorageParamScopes = append(a.refStorageParamScopes, bindings)
	fn()
	a.refStorageParamScopes = a.refStorageParamScopes[:len(a.refStorageParamScopes)-1]
}

func (a *Analyzer) withRefStateParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &RefStateParamType{Name: name}
		}
	}
	a.refStateParamScopes = append(a.refStateParamScopes, bindings)
	fn()
	a.refStateParamScopes = a.refStateParamScopes[:len(a.refStateParamScopes)-1]
}

func (a *Analyzer) withGenericParams(params []ast.GenericParam, args []Type, fn func()) {
	if len(params) == 0 {
		fn()
		return
	}
	typeNames := make([]string, 0)
	typeArgs := make([]Type, 0)
	typeInterfaces := make(map[string]*StaticInterface)
	refStorageNames := make([]string, 0)
	refStorageArgs := make([]Type, 0)
	refStateNames := make([]string, 0)
	refStateArgs := make([]Type, 0)
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
		case ast.GenericParamRefStorage:
			refStorageNames = append(refStorageNames, param.Name)
			refStorageArgs = append(refStorageArgs, arg)
		case ast.GenericParamRefState:
			refStateNames = append(refStateNames, param.Name)
			refStateArgs = append(refStateArgs, arg)
		}
	}
	a.withTypeParamInterfaces(typeInterfaces, func() {
		a.withTypeParams(typeNames, typeArgs, func() {
			a.withRefStorageParams(refStorageNames, refStorageArgs, func() {
				a.withRefStateParams(refStateNames, refStateArgs, fn)
			})
		})
	})
}

func (a *Analyzer) lookupTypeParam(name string) (Type, bool) {
	for i := len(a.typeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.typeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupRefStorageParam(name string) (Type, bool) {
	for i := len(a.refStorageParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.refStorageParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupRefStateParam(name string) (Type, bool) {
	for i := len(a.refStateParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.refStateParamScopes[i][name]; ok {
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
