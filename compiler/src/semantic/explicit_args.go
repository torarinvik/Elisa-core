package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type callArgSource int

const (
	callArgSourceNone callArgSource = iota
	callArgSourceForward
	callArgSourceExplicit
)

func orderedCallArgItems(expr *ast.CallExpr) []ast.CallArgItem {
	if expr == nil {
		return nil
	}
	if len(expr.ArgItemOrder) != 0 {
		return append([]ast.CallArgItem(nil), expr.ArgItemOrder...)
	}
	items := make([]ast.CallArgItem, 0, len(expr.Args))
	for i, arg := range expr.Args {
		pos := lexer.Pos{}
		if arg != nil {
			pos = arg.Pos()
		}
		items = append(items, ast.CallArgItem{Position: pos, ArgIndex: i})
	}
	return items
}

func isExplicitValueSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolConst, SymbolGlobal, SymbolFunc, SymbolExternFunc, SymbolExternVar, SymbolParam, SymbolLocal:
		return true
	default:
		return false
	}
}

func (a *Analyzer) lookupVisibleValueExpr(name string) (ast.Expr, bool) {
	if a == nil || name == "" {
		return nil, false
	}
	for scope := a.currentScope; scope != nil; scope = scope.Parent {
		if scope == a.globalScope {
			break
		}
		sym, ok := scope.Symbols[name]
		if !ok || sym == nil {
			continue
		}
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if !isExplicitValueSymbolKind(root.Kind) {
			continue
		}
		return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
	}
	if sym, _, ok := a.lookupVisibleGlobal(name); ok && sym != nil {
		root := symbolAliasRoot(sym)
		if root == nil {
			root = sym
		}
		if isExplicitValueSymbolKind(root.Kind) {
			return &ast.Ident{Position: lexer.Pos{}, Name: name}, true
		}
	}
	return nil, false
}

func (a *Analyzer) resolveSameNameExplicitValueExpr(name string, pos lexer.Pos, context string) ast.Expr {
	if expr, ok := a.lookupVisibleValueExpr(name); ok && expr != nil {
		return expr
	}
	a.errorf(pos, "no value named %q for shorthand %s", name, context)
	return &ast.Ident{Position: pos, Name: name}
}

func (a *Analyzer) resolveCallArgValueExpr(expr *ast.CallExpr, index int, context string) ast.Expr {
	if expr == nil || index < 0 || index >= len(expr.Args) {
		return nil
	}
	argExpr := expr.Args[index]
	if index >= len(expr.ArgShorthand) || !expr.ArgShorthand[index] {
		return argExpr
	}
	name := expr.ArgName(index)
	if name == "" {
		return argExpr
	}
	return a.resolveSameNameExplicitValueExpr(name, argExpr.Pos(), context)
}
