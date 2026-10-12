package semantic

import (
	"encoding/hex"
	"strings"

	"elisacore/src/ast"
)

// Only expression-only classifiers qualify: an immutable view binding does
// not by itself guarantee that its backing bytes survive mutations or calls.
func pureClassifierExpr(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Ident, *ast.StringLit, *ast.BoolLit, *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return pureClassifierExpr(n.Inner)
	case *ast.BinaryExpr:
		return pureClassifierExpr(n.Left) && pureClassifierExpr(n.Right)
	case *ast.MatchExpr:
		if n.Store != nil || !pureClassifierExpr(n.Value) {
			return false
		}
		for _, arm := range n.Arms {
			if arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !pureClassifierExpr(tail.Expr) {
				return false
			}
		}
		return true
	}
	return false
}

func pureClassifierBody(body []ast.Stmt) bool {
	if len(body) != 1 {
		return false
	}
	switch n := body[0].(type) {
	case *ast.ReturnStmt:
		return pureClassifierExpr(n.Value)
	case *ast.CanStmt:
		return n.HandlerName == "" && len(n.HandlerArgs) == 0 && pureClassifierBody(n.Body)
	}
	return false
}

func (tr *smtTranslator) classifierStringParameter(expr ast.Expr, env map[string]string) (*ast.Ident, bool) {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || tr.a == nil || tr.a.currentScope == nil || tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return nil, false
	}
	if !pureClassifierBody(tr.a.currentFuncDecl.Body) {
		return nil, false
	}
	if _, bound := env[id.Name]; bound {
		return nil, false
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return nil, false
	}
	paramIndex := -1
	for index, param := range tr.a.currentFuncDecl.Params {
		if param.Name == id.Name {
			if tr.a.paramIsMutable(param) {
				return nil, false
			}
			paramIndex = index
			break
		}
	}
	if paramIndex < 0 {
		return nil, false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Mutable || sym.Kind != SymbolParam || sym.Node != tr.a.currentFuncDecl || sym.ParamIndex != paramIndex || !isStringViewType(sym.Type) {
		return nil, false
	}
	return id, true
}

// Independent atoms overapproximate string equality. No exclusivity or
// equality between different literals is assumed, so this can miss proofs
// but cannot establish them by inventing string relationships.
func (tr *smtTranslator) classifierStringEquality(left, right ast.Expr, env map[string]string) (string, bool) {
	left = stripOptimizationParens(left)
	right = stripOptimizationParens(right)
	if _, ok := right.(*ast.StringLit); !ok {
		if _, ok := left.(*ast.StringLit); ok {
			left, right = right, left
		} else {
			return "", false
		}
	}
	lit, ok := right.(*ast.StringLit)
	if !ok || strings.ContainsRune(lit.Value, 0) {
		return "", false
	}
	id, ok := tr.classifierStringParameter(left, env)
	if !ok {
		return "", false
	}
	name := "str_eq_" + hex.EncodeToString([]byte(id.Name)) + "_" + hex.EncodeToString([]byte(lit.Value))
	if tr.boolDecls == nil {
		tr.boolDecls = map[string]bool{}
	}
	tr.boolDecls[name] = true
	return name, true
}

func (tr *smtTranslator) booleanMatchTerm(n *ast.MatchExpr, env map[string]string) (string, bool) {
	if n == nil || n.Store != nil || len(n.Arms) == 0 {
		return "", false
	}
	if _, ok := tr.classifierStringParameter(n.Value, env); !ok {
		return "", false
	}
	if _, ok := n.Arms[len(n.Arms)-1].Pattern.(*ast.MatchWildcardPattern); !ok {
		return "", false
	}
	// Validate the full shape before creating any SMT symbols. Restrict the arm
	// values to literals so translation cannot duplicate calls or mutable reads.
	for index, arm := range n.Arms {
		if arm.Guard != nil || len(arm.Body) != 1 {
			return "", false
		}
		tail, ok := arm.Body[0].(*ast.ExprStmt)
		if !ok {
			return "", false
		}
		if _, ok := stripOptimizationParens(tail.Expr).(*ast.BoolLit); !ok {
			return "", false
		}
		if index == len(n.Arms)-1 {
			continue
		}
		pattern, ok := arm.Pattern.(*ast.MatchStringLiteralPattern)
		if !ok || strings.ContainsRune(pattern.Value, 0) {
			return "", false
		}
	}
	result := ""
	for index := len(n.Arms) - 1; index >= 0; index-- {
		arm := n.Arms[index]
		literal := stripOptimizationParens(arm.Body[0].(*ast.ExprStmt).Expr).(*ast.BoolLit)
		branch := "false"
		if literal.Value {
			branch = "true"
		}
		if index == len(n.Arms)-1 {
			result = branch
			continue
		}
		pattern := arm.Pattern.(*ast.MatchStringLiteralPattern)
		guard, ok := tr.classifierStringEquality(n.Value, &ast.StringLit{Value: pattern.Value}, env)
		if !ok {
			return "", false
		}
		result = "(ite " + guard + " " + branch + " " + result + ")"
	}
	return result, true
}
