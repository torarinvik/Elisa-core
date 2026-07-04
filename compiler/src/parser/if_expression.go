package parser

import (
	"elisacore/src/ast"
)

// docs/119 §4 — `if`/`match` as multi-line expressions. When the tail statement of
// a block expression is an `if`/`elif`/`else` (or a `match`), it is the block's
// value: each branch/arm is itself a block expression and their tails unify to one
// type. There is no dedicated `IfExpr` node — an `if` value nests into TernaryExpr
// (`then if cond else alt`), so branch-type unification (E6) and codegen reuse the
// already-tested ternary path; a `match` value maps onto MatchExpr (same arm shape
// as MatchStmt, so exhaustiveness/payload-destructuring come for free).
//
// E7: an `if` used as a value MUST have a final `else` (no value on the false path).

// tailStmtToExpr converts a would-be-tail statement into a value expression when it
// is an `if`/`match` (docs/119 §4). ok=false means "not a value-yielding tail form"
// — the caller keeps its existing "block needs a final expression" diagnostic.
func (p *Parser) tailStmtToExpr(s ast.Stmt) (ast.Expr, bool) {
	switch n := s.(type) {
	case *ast.ExprStmt:
		if n == nil || n.Expr == nil {
			return nil, false
		}
		return n.Expr, true
	case *ast.IfStmt:
		return p.ifStmtToExpr(n)
	case *ast.MatchStmt:
		// docs/119 §4: a block-tail `match` becomes the block's value. Its arm bodies were
		// parsed as statements, so recursively convert each arm's tail into a value
		// expression (an inner `match`/`if` arm yields, composing nested match-expressions).
		return &ast.MatchExpr{Position: n.Position, Value: n.Value, Store: n.Store, Arms: p.matchArmsToValueArms(n.Arms)}, true
	}
	return nil, false
}

// matchArmsToValueArms rewrites each arm body so its tail statement yields a value
// (docs/119 §4). An arm whose tail is already an expression is unchanged; an arm whose
// tail is a nested `match`/`if` is converted so the arm composes as a value. Arms that
// cannot yield are left as-is — analyzeMatchExprArmBody reports the precise diagnostic.
func (p *Parser) matchArmsToValueArms(arms []ast.MatchArm) []ast.MatchArm {
	out := make([]ast.MatchArm, len(arms))
	for i, arm := range arms {
		out[i] = arm
		out[i].Body = p.valueBlockTail(arm.Body)
	}
	return out
}

// valueBlockTail rewrites a block's final statement so it yields a value (docs/119 §4):
// a trailing `match`/`if` becomes a MatchExpr/TernaryExpr wrapped in an ExprStmt, which
// the analyzer/backend already treat as the block's value. A body whose tail is already
// an expression, or is not a value-yielding form, is returned unchanged (the analyzer
// then reports the precise "arm must end with an expression" diagnostic).
func (p *Parser) valueBlockTail(body []ast.Stmt) []ast.Stmt {
	if len(body) == 0 {
		return body
	}
	last := body[len(body)-1]
	if _, ok := last.(*ast.ExprStmt); ok {
		return body
	}
	value, ok := p.tailStmtToExpr(last)
	if !ok {
		return body
	}
	newBody := append([]ast.Stmt(nil), body[:len(body)-1]...)
	return append(newBody, &ast.ExprStmt{Position: last.Pos(), Expr: value})
}

// ifStmtToExpr converts an `if`/`elif`/`else` statement into a nested TernaryExpr.
// Requires a final `else` (E7); otherwise the if has no value on the false path.
func (p *Parser) ifStmtToExpr(n *ast.IfStmt) (ast.Expr, bool) {
	if len(n.Else) == 0 {
		p.errorf("an `if` used as a value must have a final `else` (docs/119 E7)")
		return nil, false
	}
	alt, ok := p.branchBlockToExpr(n.Else)
	if !ok {
		return nil, false
	}
	// Right-fold the elif chain into the alt so evaluation order is preserved.
	for i := len(n.Elifs) - 1; i >= 0; i-- {
		e := n.Elifs[i]
		thenE, ok := p.branchBlockToExpr(e.Body)
		if !ok {
			return nil, false
		}
		alt = &ast.TernaryExpr{Position: e.Position, Value: thenE, Cond: e.Cond, Alt: alt}
	}
	thenE, ok := p.branchBlockToExpr(n.Then)
	if !ok {
		return nil, false
	}
	return &ast.TernaryExpr{Position: n.Position, Value: thenE, Cond: n.Cond, Alt: alt}, true
}

// branchBlockToExpr turns one branch body (`if`/`elif`/`else` arm) into its value
// expression: the branch's own tail statement recursively yields the value, and any
// preceding statements ride an ExprBlock.
func (p *Parser) branchBlockToExpr(stmts []ast.Stmt) (ast.Expr, bool) {
	if len(stmts) == 0 {
		p.errorf("an `if`/`else` branch used as a value must end in an expression (docs/119 §4)")
		return nil, false
	}
	value, ok := p.tailStmtToExpr(stmts[len(stmts)-1])
	if !ok {
		p.errorf("an `if`/`else` branch used as a value must end in an expression (docs/119 §4)")
		return nil, false
	}
	if len(stmts) == 1 {
		return value, true
	}
	return &ast.ExprBlock{
		Position: stmts[0].Pos(),
		Stmts:    append([]ast.Stmt(nil), stmts[:len(stmts)-1]...),
		Value:    value,
	}, true
}
