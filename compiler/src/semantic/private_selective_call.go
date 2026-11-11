package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// Explain a failed direct call when its selective import has a matching private
// signature. The selected callable and its argument diagnostics stay intact.
func (a *Analyzer) reportPrivateSelectiveCallCandidate(call *ast.CallExpr, ordered []ast.Expr) {
	callee, ok := call.Func.(*ast.Ident)
	if !ok || len(call.Args) > len(ordered) || call.HasArgForward || call.NamedArgCount() != 0 || strings.Contains(callee.Name, ".") {
		return
	}
	target := a.importAliases[callee.Name]
	if target == "" {
		return
	}
	if _, local := a.lookupLocalSymbol(callee.Name); local {
		return
	}
	for _, candidate := range a.ufcsFunctionsByName[target] {
		if candidate == nil || a.globalNameIsVisible(candidate, candidate.Name) {
			continue
		}
		signature, ok := candidate.Type.(*FuncType)
		if !ok || !a.matchesPrivateSelectiveSignature(signature, ordered[:len(call.Args)]) {
			continue
		}
		name := target
		if module := a.privateModuleOnPath(target); module != "" {
			name = module
		}
		a.errorf(callee.Pos(), "%s", PrivateNameMessage(name, privateOwnerNamespace(name)))
		return
	}
}

// Match only from types already checked at this call site. Context-dependent
// arguments and signatures that require bound or region inference keep their
// original diagnostics.
func (a *Analyzer) matchesPrivateSelectiveSignature(signature *FuncType, args []ast.Expr) bool {
	count := funcTypeExplicitParamCount(signature)
	if signature.Static || signature.Variadic || len(args) > count || len(signature.RegionParams) != 0 || len(signature.PermissionParams) != 0 || len(signature.ShapeParams) != 0 {
		return false
	}
	for i := len(args); i < count; i++ {
		if !funcTypeExplicitParamHasDefault(signature, i) {
			return false
		}
	}
	for _, param := range signature.GenericParams {
		if param.Kind != ast.GenericParamType || param.InterfaceBound != "" || param.StateOwner != "" || len(param.StateCases) != 0 {
			return false
		}
	}
	var bindings map[string]Type
	for i, arg := range args {
		actual := a.exprType(arg)
		if actual == nil || IsInvalidType(actual) {
			return false
		}
		expected := signature.Params[i]
		if param, ok := expected.(*TypeParamType); ok {
			if bindings == nil {
				bindings = make(map[string]Type)
			}
			if previous, bound := bindings[param.Name]; bound && !SameType(previous, actual) {
				return false
			}
			bindings[param.Name] = actual
		} else if !AssignableTo(expected, actual) {
			return false
		}
	}
	return len(bindings) == len(signature.TypeParams)
}
