package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

// Protocol parameter annotations are anonymous, independently inferred generic
// parameters. Normalize the declaration itself so analysis and specialization see
// precisely the same signature as the explicit [P: Protocol](value: P) form.
func (a *Analyzer) lowerProtocolParameters(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.FuncDecl:
				a.lowerFunctionProtocolParameters(n)
			case *ast.ImplDecl:
				for _, member := range n.Members {
					if fn, ok := member.(*ast.FuncDecl); ok {
						a.lowerFunctionProtocolParameters(fn)
					}
				}
			}
		})
	}
}

func (a *Analyzer) lowerFunctionProtocolParameters(fn *ast.FuncDecl) {
	used := map[string]bool{}
	for _, name := range fn.TypeParams {
		used[name] = true
	}
	for _, p := range fn.Params {
		used[p.Name] = true
	}
	for i := range fn.Params {
		var lower func(ast.TypeExpr) ast.TypeExpr
		lower = func(t ast.TypeExpr) ast.TypeExpr {
			switch n := t.(type) {
			case *ast.MutableType:
				copy := *n
				copy.Elem = lower(n.Elem)
				return &copy
			case *ast.RefType:
				copy := *n
				copy.Elem = lower(n.Elem)
				return &copy
			case *ast.NamedType:
				shadowed := false
				for _, typeName := range fn.TypeParams {
					if typeName == n.Name {
						shadowed = true
					}
				}
				if shadowed {
					return t
				}
				if _, _, ok := a.lookupVisibleType(n.Name); ok {
					return t
				}
				iface, _, ok := a.lookupVisibleStaticInterface(n.Name)
				if !ok || iface == nil {
					return t
				}
				ordinal := i
				name := fmt.Sprintf("__protocol_arg_%d", ordinal)
				for used[name] {
					ordinal++
					name = fmt.Sprintf("__protocol_arg_%d", ordinal)
				}
				used[name] = true
				fn.TypeParams = append(fn.TypeParams, name)
				fn.GenericParams = append(fn.GenericParams, ast.GenericParam{Position: n.Position, Kind: ast.GenericParamType, Name: name, InterfaceBound: iface.Name})
				return &ast.NamedType{Position: n.Position, Name: name, Region: n.Region}
			}
			return t
		}
		fn.Params[i].Type = lower(fn.Params[i].Type)
	}
}
