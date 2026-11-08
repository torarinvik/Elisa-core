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
	if params, ok := cloneOrdinaryExplicitParams(fn.Params); ok {
		return params
	}
	return explicitParamDeclsFromSpecs(a.expandExplicitParamSpecs(fn.Params, fn.Name))
}

// Most declarations have a short list of named, unique parameters. Copy that
// list directly without first allocating the larger intermediate spec list.
// Malformed and unusually long lists retain the diagnostic-producing path.
func cloneOrdinaryExplicitParams(params []ast.ParamDecl) ([]ast.ParamDecl, bool) {
	if !ordinaryExplicitParams(params) {
		return nil, false
	}
	return append([]ast.ParamDecl(nil), params...), true
}

func ordinaryExplicitParams(params []ast.ParamDecl) bool {
	if len(params) > 16 {
		return false
	}
	for i := range params {
		if params[i].Name == "" {
			return false
		}
		for j := 0; j < i; j++ {
			if params[j].Name == params[i].Name {
				return false
			}
		}
	}
	return true
}

// readOnlyExpandedFuncDeclParams borrows the declaration's parameters only
// for immediate, read-only inspection. Malformed lists still use expansion so
// duplicate diagnostics and filtering remain identical.
func (a *Analyzer) readOnlyExpandedFuncDeclParams(fn *ast.FuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	if ordinaryExplicitParams(fn.Params) {
		return fn.Params
	}
	return a.expandedFuncDeclParams(fn)
}

// expandedFuncDeclParamAt is expandedFuncDeclParams(fn)[i] without copying the list
// when expansion is the identity (every name present and unique), the usual case;
// region inference asks this for every forwarded argument of every candidate.
func (a *Analyzer) expandedFuncDeclParamAt(fn *ast.FuncDecl, i int) (ast.ParamDecl, bool) {
	if fn == nil || i < 0 {
		return ast.ParamDecl{}, false
	}
	for j := range fn.Params {
		name := fn.Params[j].Name
		duplicate := name == ""
		for k := 0; k < j && !duplicate; k++ {
			duplicate = fn.Params[k].Name == name
		}
		if duplicate {
			params := a.expandedFuncDeclParams(fn)
			if i >= len(params) {
				return ast.ParamDecl{}, false
			}
			return params[i], true
		}
	}
	if i >= len(fn.Params) {
		return ast.ParamDecl{}, false
	}
	return fn.Params[i], true
}

func (a *Analyzer) expandedExternFuncDeclParams(fn *ast.ExternFuncDecl) []ast.ParamDecl {
	if fn == nil {
		return nil
	}
	if params, ok := cloneOrdinaryExplicitParams(fn.Params); ok {
		return params
	}
	return explicitParamDeclsFromSpecs(a.expandExplicitParamSpecs(fn.Params, fn.Name))
}
