package parserdesugar

import (
	"elisacore/src/ast"
	"strconv"
)

func DStrStringLiteralInit(typ ast.TypeExpr, value ast.Expr) ast.Expr {
	if value == nil || !typeExprIsRegionlessDStr(typ) {
		return value
	}
	lit, ok := value.(*ast.StringLit)
	if !ok {
		return value
	}
	elems := make([]ast.Expr, 0, len(lit.Value))
	for i := 0; i < len(lit.Value); i++ {
		elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
	}
	return &ast.ListLitExpr{Position: lit.Position, Elems: elems}
}

// desugarDStrReturnLiterals rewrites every `return "..."` in a function body whose declared return
// type is a region-less `dstr` into the equivalent byte-list literal (`return [97, ...]`). Like the
// var-decl desugar, this must run at parse time: the region-polymorphic-return classification is a
// syntactic pre-pass that keys on a directly-returned list literal, so the StringLit must already be
// a list literal before it runs. Recurses through statement-level control flow (returns anywhere in
// the body still return from this function) but not into expressions, so a nested lambda's own
// returns — governed by a different return type — are untouched.
func DStrReturnLiterals(body []ast.Stmt, retType ast.TypeExpr) {
	if !typeExprIsRegionlessDStr(retType) {
		return
	}
	rewriteDStrReturnsIn(body)
}

func rewriteDStrReturnsIn(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if lit, ok := s.Value.(*ast.StringLit); ok {
				elems := make([]ast.Expr, 0, len(lit.Value))
				for i := 0; i < len(lit.Value); i++ {
					elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
				}
				s.Value = &ast.ListLitExpr{Position: lit.Position, Elems: elems}
			}
		case *ast.CanStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.RegionStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.InStoreStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.ScopeStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.IfStmt:
			rewriteDStrReturnsIn(s.Then)
			rewriteDStrReturnsIn(s.Else)
		case *ast.ForStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.WhileStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				rewriteDStrReturnsIn(arm.Body)
			}
		}
	}
}

// typeExprIsRegionlessDStr recognizes an owned `dstr` type, optionally mutable.
// The literal desugaring also applies when the named type has a region annotation.
func typeExprIsRegionlessDStr(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	nt, ok := typ.(*ast.NamedType)
	return ok && nt.Name == "dstr"
}
