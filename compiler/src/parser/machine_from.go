package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §5 — `machine from START [decreases MEASURE]:` state-machine EXPRESSION.
//
// Unlike `machine over` (docs/123), which consumes a sequence and is a pure parser
// desugar, `machine from` is expression-valued: states are the variants of an existing
// enum, each arm ends in `next State` (transition) or `done VALUE` (exit with a value),
// and the whole form yields the joined `done` value. The result TYPE is only knowable
// after analysis, so the parser emits a raw `MachineFromExpr` and the analyzer builds the
// loop/mode/match desugar into its `Lowered` field (the LoweredCall pattern).
//
//	kind: TokenKind = machine from Num.Integer:
//	    Num.Integer:
//	        next Num.Fraction if lexer.at_dot()
//	        done TokenKind.IntLit
//	    Num.Fraction:
//	        lexer <- lexer.advance_char()
//	        done TokenKind.FloatLit
//
// `machine` stays a contextual keyword; the `from` form is recognized only when the ident
// `from` immediately follows (no legal expression puts two bare identifiers in sequence).
// `next`/`done` are contextual arm terminators. Statement-position `machine from` is not
// intercepted by the statement dispatcher (it checks `over`), so it flows through as an
// ordinary expression statement.

// looksLikeMachineFromExpr gates the expression form: `machine from …`.
func (p *Parser) looksLikeMachineFromExpr() bool {
	return p.cur().Text == "machine" && p.pos+1 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "from"
}

func (p *Parser) parseMachineFromExpr() ast.Expr {
	pos := p.cur().Pos
	p.advance() // machine
	p.expectIdentText("from")

	startEnum, startState := p.parseMachineFromStateRef()
	// Entry-state payload: `machine from Num.Exponent(seed)` constructs the start
	// variant with args (docs/125 §5). Payload-free start states omit the parens.
	startArgs := p.parseMachineFromCallArgs()
	var decreases ast.Expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "decreases" {
		p.advance()
		decreases = p.parseMachineHeaderExpr(false)
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var arms []ast.MachineFromArm
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMachineFromArm())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.MachineFromExpr{Position: pos, StartEnum: startEnum, StartState: startState, StartArgs: startArgs, Decreases: decreases, Arms: arms}
}

// parseMachineFromCallArgs parses an optional `(EXPR, EXPR, …)` payload-construction
// argument list following a state reference (start state or a `next` target). Returns nil
// when no `(` follows, so a payload-free state stays a bare reference.
func (p *Parser) parseMachineFromCallArgs() []ast.Expr {
	if p.peek() != lexer.TOKEN_LPAREN {
		return nil
	}
	p.advance() // (
	var args []ast.Expr
	for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
		args = append(args, p.parseExpr())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return args
}

// parseMachineFromArmBindings parses an optional `(name, name, …)` payload BINDING list in
// an arm header (`Num.Exponent(was_fraction):`). Unlike call args these are identifiers the
// arm body binds, so they lower to a variant-pattern destructure, not a construction.
func (p *Parser) parseMachineFromArmBindings() []string {
	if p.peek() != lexer.TOKEN_LPAREN {
		return nil
	}
	p.advance() // (
	names := []string{}
	for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
		names = append(names, p.expect(lexer.TOKEN_IDENT).Text)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return names
}

// parseMachineFromDeclaredOut parses the optional R5 out-edge contract `-> {A, B}` after an
// arm header's state (and payload bindings). Returns (targets, true) when present so an
// empty `-> {}` (a terminal arm) is distinguishable from an omitted clause.
func (p *Parser) parseMachineFromDeclaredOut() ([]string, bool) {
	if !(p.peek() == lexer.TOKEN_ARROW) {
		return nil, false
	}
	p.advance() // ->
	p.expect(lexer.TOKEN_LBRACE)
	targets := []string{}
	for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
		_, target := p.parseMachineFromStateRef()
		targets = append(targets, target)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACE)
	return targets, true
}

// parseMachineFromStateRef parses `Enum.State` (or a bare `State`, whose enum defaults to
// the start state's enum, resolved in the analyzer). Returns (enumName, stateName).
func (p *Parser) parseMachineFromStateRef() (string, string) {
	first := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_DOT) {
		return first, p.expect(lexer.TOKEN_IDENT).Text
	}
	return "", first
}

func (p *Parser) parseMachineFromArm() ast.MachineFromArm {
	pos := p.cur().Pos
	_, state := p.parseMachineFromStateRef()
	bindings := p.parseMachineFromArmBindings()
	declaredOut, hasDeclaredOut := p.parseMachineFromDeclaredOut()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var body []ast.Stmt
	var terminators []ast.MachineFromTerminator
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "next" || p.cur().Text == "done") {
			terminators = append(terminators, p.parseMachineFromTerminator())
			continue
		}
		if len(terminators) > 0 {
			// A plain statement after a transition can never run — the arm has already
			// resolved on every path that reaches here.
			p.errorf("machine arm statement follows a `next`/`done` transition — nothing after a resolution can run (docs/125 §5)")
		}
		body = append(body, p.parseStmt())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return ast.MachineFromArm{Position: pos, State: state, Bindings: bindings, DeclaredOut: declaredOut, HasDeclaredOut: hasDeclaredOut, Body: body, Terminators: terminators}
}

func (p *Parser) parseMachineFromTerminator() ast.MachineFromTerminator {
	pos := p.cur().Pos
	kind := p.advance().Text // next | done
	term := ast.MachineFromTerminator{Position: pos, IsDone: kind == "done"}
	if term.IsDone {
		// The value is bounded at a top-level `if` (the guard) so `done 1 if c` parses
		// as `done (1) if (c)`, not the else-less ternary `1 if c`.
		term.Value = p.parseMachineFromValueExpr()
	} else {
		_, term.Target = p.parseMachineFromStateRef()
		// `next Num.Exponent(was_fraction)` constructs the successor's payload.
		term.Args = p.parseMachineFromCallArgs()
	}
	if p.match(lexer.TOKEN_IF) {
		term.Guard = p.parseExpr()
	}
	p.expectNewline()
	return term
}

// parseMachineFromValueExpr parses a `done` value expression, bounded at the first
// top-level `if` (the terminator guard) or the newline — the same slice technique the
// `machine over` header parser uses to keep a trailing clause out of the expression.
func (p *Parser) parseMachineFromValueExpr() ast.Expr {
	end := p.pos
	depth := 0
valueScan:
	for end < len(p.tokens) {
		switch p.tokens[end].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IF, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				break valueScan
			}
		}
		end++
	}
	return p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
}
