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
	if !ok || len(p.Args) != 0 || !a.constEnumPatternNamesEnum(p.EnumName, enum) {
		return nil
	}
	member, ok := enum.Member(p.Variant)
	if !ok {
		return nil
	}
	return &ast.BinaryExpr{Position: p.Pos(), Op: lexer.TOKEN_EQEQ, Left: value, Right: &ast.IntLit{Position: p.Pos(), Value: strconv.FormatInt(member.Value, 10)}}
}

// constEnumPatternNamesEnum reports whether a pattern's (possibly module-qualified) enum name denotes
// `enum`. A spelling comparison would miss `Module::Kind.Member` patterns, dropping their path facts.
func (a *Analyzer) constEnumPatternNamesEnum(name string, enum *ConstEnumType) bool {
	if name == enum.Name {
		return true
	}
	if name == "" {
		return false
	}
	base, _, ok := a.lookupVisibleType(name)
	if !ok {
		return false
	}
	resolved, ok := base.(*ConstEnumType)
	return ok && resolved == enum
}
