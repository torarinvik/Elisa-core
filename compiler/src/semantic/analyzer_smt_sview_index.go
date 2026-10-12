package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A view header does not make its backing immutable. Reuse a byte-array symbol
// only within an expression-only function that cannot modify or escape it.
func (tr *smtTranslator) immutableSviewArrayParameter(id *ast.Ident, env map[string]string) bool {
	if tr.a == nil || tr.a.currentScope == nil || tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 {
		return false
	}
	if _, bound := env[id.Name]; bound {
		return false
	}
	if _, bound := env["\x00string:"+id.Name]; bound {
		return false
	}
	if !tr.a.immutableSviewParameter(id) {
		return false
	}
	body := tr.a.currentFuncDecl.Body
	if len(body) != 1 {
		return false
	}
	ret, ok := body[0].(*ast.ReturnStmt)
	return ok && tr.a.pureSviewIndexExpression(ret.Value, 0)
}

func (a *Analyzer) immutableSviewParameter(id *ast.Ident) bool {
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl || symbolRootMutable(sym) {
		return false
	}
	if _, exact := sym.Type.(*SViewType); !exact {
		return false
	}
	for index, param := range a.currentFuncDecl.Params {
		if param.Name == id.Name {
			return sym.ParamIndex == index && !a.paramIsMutable(param)
		}
	}
	return false
}

func (a *Analyzer) pureSviewIndexExpression(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 64 {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit:
		return true
	case *ast.ParenExpr:
		return a.pureSviewIndexExpression(n.Inner, depth+1)
	case *ast.Ident:
		sym, found := a.currentScope.Lookup(n.Name)
		if !found || sym == nil || sym.Kind != SymbolParam || sym.Node != a.currentFuncDecl || symbolRootMutable(sym) {
			return false
		}
		if IsBoolType(sym.Type) {
			return true
		}
		_, _, integer := smtIntWidthSign(sym.Type)
		return integer
	case *ast.UnaryExpr:
		if n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_NOT, lexer.TOKEN_MINUS, lexer.TOKEN_PLUS:
			return a.pureSviewIndexExpression(n.Operand, depth+1)
		}
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && a.pureSviewIndexExpression(n.Left, depth+1) && a.pureSviewIndexExpression(n.Right, depth+1)
	case *ast.FieldExpr:
		id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		return ok && (n.Field == "len" || n.Field == "count") && a.immutableSviewParameter(id)
	case *ast.IndexExpr:
		id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		return ok && n.Index2 == nil && n.Fallback == nil && !n.LegacyElseFallback && n.AsBuiltinDictGet == nil && n.AsSpecialize == nil && n.AsOverlayCall == nil && a.immutableSviewParameter(id) && a.pureSviewIndexExpression(n.Index, depth+1)
	}
	return false
}
