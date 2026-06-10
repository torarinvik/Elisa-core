package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type explicitParamSpec struct {
	Decl             ast.ParamDecl
	ResolvedType     Type
	HasResolvedType  bool
	DefaultNamespace string
	DefaultUsings    []string
}

func (a *Analyzer) expandExplicitParamSpecs(params []ast.ParamDecl, ownerName string) []explicitParamSpec {
	if len(params) == 0 {
		return nil
	}
	out := make([]explicitParamSpec, 0, len(params))
	seen := map[string]bool{}
	appendSpec := func(spec explicitParamSpec, pos lexer.Pos) {
		if spec.Decl.Name == "" {
			return
		}
		if seen[spec.Decl.Name] {
			a.errorf(pos, "duplicate explicit parameter %q on %q", spec.Decl.Name, ownerName)
			return
		}
		seen[spec.Decl.Name] = true
		out = append(out, spec)
	}
	for _, param := range params {
		appendSpec(explicitParamSpec{Decl: param}, param.Position)
	}
	return out
}

func explicitParamDeclsFromSpecs(specs []explicitParamSpec) []ast.ParamDecl {
	if len(specs) == 0 {
		return nil
	}
	decls := make([]ast.ParamDecl, 0, len(specs))
	for _, spec := range specs {
		decls = append(decls, spec.Decl)
	}
	return decls
}

func (a *Analyzer) expandedFuncDeclParams(fn *ast.FuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	return explicitParamDeclsFromSpecs(a.expandExplicitParamSpecs(fn.Params, fn.Name))
}

func (a *Analyzer) expandedExternFuncDeclParams(fn *ast.ExternFuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	return explicitParamDeclsFromSpecs(a.expandExplicitParamSpecs(fn.Params, fn.Name))
}
