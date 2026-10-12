package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// parseGuardCondition is shared by all existing postfix statement guards. An
// indented condition list is an ordered, short-circuiting OR, not a statement body.
func (p *Parser) parseGuardCondition() ast.Expr {
	if p.peek() != lexer.TOKEN_COLON {
		return p.parseExpr()
	}
	pos := p.advance().Pos
	p.expect(lexer.TOKEN_NEWLINE)
	p.skipNewlines()
	if !p.match(lexer.TOKEN_INDENT) {
		p.errorAt(pos, "guard condition list requires an indented, nonempty block of bool expressions")
		return &ast.BoolLit{Position: pos, Value: false}
	}
	var condition ast.Expr
	savedGuard := p.stmtGuardArmed
	p.stmtGuardArmed = false
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT || p.peek() == lexer.TOKEN_EOF {
			break
		}
		start := p.pos
		next := p.parseExpr()
		p.expectNewlineAfterValueExpr(next)
		if next != nil {
			if condition == nil {
				condition = next
			} else {
				condition = &ast.BinaryExpr{Position: next.Pos(), Op: lexer.TOKEN_OR, Left: condition, Right: next}
			}
		}
		if p.pos == start {
			p.advance() // Recover without hanging on a statement where an expression was required.
		}
	}
	p.stmtGuardArmed = savedGuard
	p.expect(lexer.TOKEN_DEDENT)
	p.stmtGuardBlockEnd = true
	if condition == nil {
		p.errorAt(pos, "guard condition list must contain at least one bool expression")
		return &ast.BoolLit{Position: pos, Value: false}
	}
	return condition
}
