package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
)

// Only an immutable scalar subject can retain a match fact through the arm body.
func (a *Analyzer) immutableConstEnumPatternCondition(value ast.Expr, pattern ast.MatchPattern, enum *ConstEnumType) ast.Expr {
	id, ok := stripOptimizationParens(value).(*ast.Ident)
	if !ok {
		return nil
	}
	symbol, ok := a.currentScope.Lookup(id.Name)
	if !ok || symbol.Mutable {
		return nil
	}
	p, ok := pattern.(*ast.MatchVariantPattern)
	if !ok || p.EnumName != enum.Name || len(p.Args) != 0 {
		return nil
	}
	member, ok := enum.Member(p.Variant)
	if !ok {
		return nil
	}
	return &ast.BinaryExpr{Position: p.Pos(), Op: lexer.TOKEN_EQEQ, Left: value, Right: &ast.IntLit{Position: p.Pos(), Value: strconv.FormatInt(member.Value, 10)}}
}
