package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// recordForRangeSMTFact gives the body of `for i in start..<end:` the SMT path fact
// `start <= i and i < end`.
//
// The body is analyzed once and the fact is assumed at the top of EVERY iteration, while `start`
// and `end` are evaluated once at loop entry. The fact is therefore recorded only when both bounds
// are loop-invariant by construction (see forRangeBoundStable): built from constants, literals,
// immutable bindings and `.count`/`.len` of immutable non-`mutable` bindings — the same stability
// stage0 already assumes for a `requires` fact over such a length. The loop variable itself is an
// immutable per-iteration binding. Only the plain ascending form (no step, `..<`) is modeled: the
// body runs only when start < end, and every visited value lies in [start, end).
func (a *Analyzer) recordForRangeSMTFact(stmt *ast.ForStmt, loopScope *Scope, loopType Type) {
	if a == nil || stmt == nil || loopScope == nil || stmt.Reverse || stmt.Step != nil || stmt.Op != lexer.TOKEN_RANGE_LT {
		return
	}
	if _, _, ok := smtIntWidthSign(loopType); !ok {
		return
	}
	if !a.forRangeBoundStable(stmt.Start, 0) || !a.forRangeBoundStable(stmt.End, 0) {
		return
	}
	lowerIndex := &ast.Ident{Position: stmt.Position, Name: stmt.Name}
	upperIndex := &ast.Ident{Position: stmt.Position, Name: stmt.Name}
	a.exprTypes[lowerIndex] = loopType
	a.exprTypes[upperIndex] = loopType
	lower := &ast.BinaryExpr{Position: stmt.Position, Op: lexer.TOKEN_LTEQ, Left: stmt.Start, Right: lowerIndex}
	upper := &ast.BinaryExpr{Position: stmt.Position, Op: lexer.TOKEN_LT, Left: upperIndex, Right: stmt.End}
	for _, fact := range []*ast.BinaryExpr{lower, upper} {
		a.exprTypes[fact] = &BuiltinType{Name: "bool"}
		a.recordSMTAssertFactInScope(loopScope, fact)
	}
}

// forRangeBoundStable reports whether a range bound denotes the same value at every iteration:
// integer literals and constants, immutable non-`mutable`-ref bindings, `.count`/`.len` of such a
// binding, value-preserving casts of a stable bound, and +/- of stable bounds. Anything else
// (calls, field reads of structs, mutable roots) is rejected.
func (a *Analyzer) forRangeBoundStable(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 16 || a.currentScope == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit:
		return true
	case *ast.ParenExpr:
		return a.forRangeBoundStable(n.Inner, depth+1)
	case *ast.Ident:
		if _, ok := a.constIntValue(n); ok {
			return true
		}
		sym, found := a.currentScope.Lookup(n.Name)
		if !found || sym == nil {
			return false
		}
		if sym.Kind != SymbolLocal && sym.Kind != SymbolParam {
			return false
		}
		return !symbolRootMutable(sym)
	case *ast.FieldExpr:
		if n.Field != "count" && n.Field != "len" {
			return false
		}
		id, ok := stripOptimizationParens(n.Object).(*ast.Ident)
		if !ok || id == nil {
			return false
		}
		sym, found := a.currentScope.Lookup(id.Name)
		if !found || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam) {
			return false
		}
		return !symbolRootMutable(sym)
	case *ast.CastExpr:
		return a.forRangeBoundStable(n.Operand, depth+1)
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_PLUS && n.Op != lexer.TOKEN_MINUS {
			return false
		}
		return a.forRangeBoundStable(n.Left, depth+1) && a.forRangeBoundStable(n.Right, depth+1)
	}
	return false
}
