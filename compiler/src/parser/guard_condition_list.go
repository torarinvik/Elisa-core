package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// guardConditionListAhead reports whether a postfix guard's condition is an indented
// list: `if:` or `if not:`.
func (p *Parser) guardConditionListAhead() bool {
	if p.peek() == lexer.TOKEN_COLON {
		return true
	}
	return p.peek() == lexer.TOKEN_NOT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON
}

// parseGuardCondition is shared by all existing postfix statement guards. An
// indented condition list is an ordered, short-circuiting OR, not a statement body:
// `return x if:` leaves when any line holds. Under `if not:` the lines are an ordered,
// short-circuiting AND that is negated: `return x if not:` leaves unless every line
// holds.
func (p *Parser) parseGuardCondition() ast.Expr {
	if !p.guardConditionListAhead() {
		return p.parseExpr()
	}
	var negated *lexer.Token
	if p.peek() == lexer.TOKEN_NOT {
		not := p.advance()
		negated = &not
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
				join := lexer.TOKEN_OR
				if negated != nil {
					join = lexer.TOKEN_AND
				}
				condition = &ast.BinaryExpr{Position: next.Pos(), Op: join, Left: condition, Right: next}
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
	if negated != nil {
		return &ast.UnaryExpr{Position: negated.Pos, Op: lexer.TOKEN_NOT, Operand: &ast.ParenExpr{Position: condition.Pos(), Inner: condition}}
	}
	return condition
}
