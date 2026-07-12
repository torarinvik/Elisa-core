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
	return p.parseMachineFromTail(pos, startEnum, startState)
}

// parseMachineFromTail parses everything after the start-state reference of a `machine from`
// expression — the optional entry-state payload, the header accumulator pipe, `decreases`,
// and the arm block — into a MachineFromExpr. Shared by `machine from Enum.State` and the
// `start State` local-state sugar (docs/125 §5), which differ only in how (startEnum,
// startState) are obtained.
func (p *Parser) parseMachineFromTail(pos lexer.Pos, startEnum, startState string) ast.Expr {
	// Entry-state payload: `machine from Num.Exponent(seed)` constructs the start
	// variant with args (docs/125 §5). Payload-free start states omit the parens.
	startArgs := p.parseMachineFromCallArgs()

	// Optional header accumulator pipe `|r: i32 = 0, blocks: i32 = 0|` (docs/125 §5):
	// machine-private mutables threaded across transitions, in scope for `decreases` and
	// every arm. It precedes `decreases` so the measure can reference an accumulator by
	// name (`decreases 3 * (dh - r) + 2`). Reuses the loop-header parser; a `-> yield` is
	// rejected because a machine yields through `done`, not a header yield.
	var headerDecls []ast.Stmt
	var headerCaptures []string
	// A `|` here is unambiguously a header pipe: the only tokens legal after the start
	// state are `|`, `decreases`, or `:`, so there is no bitwise-or to disambiguate from
	// (unlike a loop iterable). Detect it directly rather than via loopHeaderDeclsAhead,
	// whose closing-pipe-must-be-followed-by-`:`/`->` rule fails when `decreases` follows.
	if p.peek() == lexer.TOKEN_PIPE {
		hdr := p.parseLoopHeader()
		if hdr.yield != nil {
			p.errorf("`machine from` yields through `done VALUE`, not a header `-> yield` — drop the `-> …` from the accumulator pipe (docs/125 §5)")
		}
		headerDecls = hdr.decls
		headerCaptures = hdr.captures
	}

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

	return &ast.MachineFromExpr{Position: pos, StartEnum: startEnum, StartState: startState, StartArgs: startArgs, Decreases: decreases, HeaderDecls: headerDecls, HeaderCaptures: headerCaptures, Arms: arms}
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
		stmt := p.parseStmt()
		p.validateMachineFromArmStmt(stmt)
		body = append(body, stmt)
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

// validateMachineFromArmStmt enforces the shared arm law (docs/125 §5 R1) on a `machine
// from` arm-body statement: straight-line computation only. All discrimination lives in
// the arm header and the `next … if guard` terminators — a body may compute and mutate
// (the desugared loop's captures license the mutation) but may NOT hide a branch, a loop,
// or a control-flow escape. This mirrors `machine over`'s validateMachineArmStmt so both
// forms obey one law; the only difference is that resolution here is `next`/`done`, so
// `return`/`break`/`continue` are escapes rather than legal arm exits.
func (p *Parser) validateMachineFromArmStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.IfStmt, *ast.MatchStmt, *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
		p.errorf("machine arms cannot branch or loop (docs/125 §5) — move the condition into a `next State if guard` terminator or split into separate arms")
	case *ast.ReturnStmt:
		p.errorf("machine arms cannot `return` — an arm resolves with `done VALUE`, not by escaping the enclosing function (docs/125 §5)")
	case *ast.BreakStmt, *ast.ContinueStmt:
		p.errorf("machine arms cannot `break`/`continue` — every path resolves via `next State` or `done VALUE` (docs/125 §5)")
	case *ast.CanStmt:
		// A postfix `can Effect` clause is a transparent effect-licensing wrapper, not a
		// branch. Validate the licensed statements.
		for _, inner := range s.Body {
			p.validateMachineFromArmStmt(inner)
		}
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.ExprStmt:
		_ = s // allowed straight-line forms
	case nil:
		// parse error already reported
	default:
		p.errorf("machine arms allow only straight-line statements before a `next`/`done` terminator (docs/125 §5)")
	}
}
