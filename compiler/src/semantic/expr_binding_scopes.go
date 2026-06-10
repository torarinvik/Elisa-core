package semantic

import "elisacore/src/ast"

func cloneExprBindings(src map[string]ast.Expr) map[string]ast.Expr {
	if len(src) == 0 {
		return map[string]ast.Expr{}
	}
	out := make(map[string]ast.Expr, len(src))
	for name, expr := range src {
		out[name] = expr
	}
	return out
}

func combineExprBindingScopes(scopes []map[string]ast.Expr) map[string]ast.Expr {
	merged := map[string]ast.Expr{}
	for _, scope := range scopes {
		for name, expr := range scope {
			merged[name] = expr
		}
	}
	return merged
}

func pushExprBindingScope(scopes []map[string]ast.Expr, bindings map[string]ast.Expr) []map[string]ast.Expr {
	if len(bindings) == 0 {
		return append([]map[string]ast.Expr(nil), scopes...)
	}
	return append(append([]map[string]ast.Expr(nil), scopes...), cloneExprBindings(bindings))
}
