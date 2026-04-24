package semantic

import "llcontext/src/ast"

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

func (a *Analyzer) analyzeNestedStmtBodyWithExprBindings(bindingScopes *[]map[string]ast.Expr, bindings map[string]ast.Expr, tempStmts []ast.Stmt, body []ast.Stmt) {
	if a == nil || bindingScopes == nil {
		return
	}
	scope := NewScope(a.currentScope)
	savedScope := a.currentScope
	a.currentScope = scope
	for _, tempStmt := range tempStmts {
		a.analyzeStmt(tempStmt)
	}
	a.currentScope = savedScope
	savedBindingScopes := *bindingScopes
	*bindingScopes = pushExprBindingScope(savedBindingScopes, bindings)
	a.currentScope = scope
	a.withLocalParamPackFrame(func() {
		for _, bodyStmt := range body {
			a.analyzeStmt(bodyStmt)
		}
	})
	a.currentScope = savedScope
	*bindingScopes = savedBindingScopes
}
