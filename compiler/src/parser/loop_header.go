package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/119 §3 — loop expression headers. `for x in xs |acc = 0| -> acc:` and
// `while cond |i = 0|:` introduce loop-private mutable accumulators; the
// optional `-> yield` makes the loop a value (evaluated against the final
// accumulator state, whether the loop finishes or `break`s).
//
// Implementation is a pure parser desugar onto existing nodes:
//   with `->`:  ExprStmt( ExprBlock{ decls…, loop } yielding YIELD )
//   without:    ScopeStmt{ decls…, loop }   (privacy without a value)
// so scoping, break-yields-current-state, analysis, and codegen are all the
// already-tested ExprBlock/ScopeStmt paths.

type loopHeader struct {
	pos      lexer.Pos
	decls    []ast.Stmt
	captures []string // docs/119 §6: outer mutables threaded through the loop
	yield    ast.Expr
}

// loopHeaderDeclsAt reports whether tokens[i:] begins a loop-header. Two shapes are
// accepted, both disambiguated from a bitwise `|` in the iterable/condition:
//
//   - a decl-led header `| IDENT = …` — `=` cannot appear inside an expression, so
//     `xs | mask` can never match; and
//   - a capture header `| IDENT … |` whose CLOSING pipe is immediately followed by `:`
//     or `->` (docs/119 §6). A bitwise `a | b | c` never satisfies this: its second
//     top-level pipe is followed by another operand, not the loop's `:`/`->`.
func (p *Parser) loopHeaderDeclsAt(i int) bool {
	if i+2 < len(p.tokens) &&
		p.tokens[i].Kind == lexer.TOKEN_PIPE &&
		p.tokens[i+1].Kind == lexer.TOKEN_IDENT &&
		p.tokens[i+2].Kind == lexer.TOKEN_ASSIGN {
		return true
	}
	return p.captureHeaderAt(i)
}

// captureHeaderAt reports whether tokens[i:] is a `| item, … |` header terminated by a
// closing pipe immediately followed by `:` or `->`. Items begin with an identifier; a
// top-level `|` (outside ()[]{}) closes the header, so an init with a bitwise `|` must
// be parenthesized (same rule as decl headers).
func (p *Parser) captureHeaderAt(i int) bool {
	if i+1 >= len(p.tokens) ||
		p.tokens[i].Kind != lexer.TOKEN_PIPE ||
		p.tokens[i+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	depth := 0
	for j := i + 1; j < len(p.tokens); j++ {
		switch p.tokens[j].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_PIPE:
			if depth == 0 {
				// Closing pipe — a header only if the loop body / yield follows directly.
				return j+1 < len(p.tokens) &&
					(p.tokens[j+1].Kind == lexer.TOKEN_COLON || p.tokens[j+1].Kind == lexer.TOKEN_ARROW)
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return false // reached line end without a closing pipe — not a header
			}
			// NB: a `:` is NOT a scan terminator — a typed accumulator (`|n: T = 0|`)
			// carries a depth-0 `:` inside the header, before the closing pipe. The
			// single-line NEWLINE bound already keeps the scan from reaching the loop
			// body, and the closing-pipe test still requires a following `:`/`->`.
		}
	}
	return false
}

func (p *Parser) loopHeaderDeclsAhead() bool {
	return p.loopHeaderDeclsAt(p.pos)
}

// parseLoopHeader parses `|name = expr, ...| [-> yield]`. Each decl becomes a
// mutable local initialized once, before the first iteration. An initializer
// containing a top-level bitwise `|` needs parens.
func (p *Parser) parseLoopHeader() *loopHeader {
	hdr := &loopHeader{pos: p.cur().Pos}
	p.expect(lexer.TOKEN_PIPE)
	for {
		nameTok := p.expect(lexer.TOKEN_IDENT)
		// Optional `: T` type annotation on an accumulator (`|n: u32 = 0|`) — for when
		// the accumulator's type can't be inferred from its initializer alone.
		var declType ast.TypeExpr
		if p.match(lexer.TOKEN_COLON) {
			declType = p.parseTypeExpr()
		}
		if !p.match(lexer.TOKEN_ASSIGN) {
			if declType != nil {
				p.errorf("loop accumulator %q needs an initializer (write `%s: T = init`)", nameTok.Text, nameTok.Text)
			}
			// Bare name — a capture of an outer mutable (docs/119 §6).
			hdr.captures = append(hdr.captures, nameTok.Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			continue
		}
		// The initializer ends at the first top-level `,` or `|` (a bitwise `|`
		// inside an initializer needs parens) — sub-parse the slice so the
		// expression grammar cannot eat the header's closing pipe as an operator.
		end := p.pos
		depth := 0
	initScan:
		for end < len(p.tokens) {
			switch p.tokens[end].Kind {
			case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
				depth++
			case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
				if depth > 0 {
					depth--
				}
			case lexer.TOKEN_COMMA, lexer.TOKEN_PIPE, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
				if depth == 0 {
					break initScan
				}
			}
			end++
		}
		init := p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
		hdr.decls = append(hdr.decls, &ast.VarDeclStmt{Position: nameTok.Pos, Name: nameTok.Text, Mutable: true, Type: declType, Value: init})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_PIPE)
	if p.match(lexer.TOKEN_ARROW) {
		hdr.yield = p.parseValueExprAllowTuple()
	}
	return hdr
}

// wrapLoopHeader attaches a parsed loop header to its loop statement.
func (p *Parser) wrapLoopHeader(hdr *loopHeader, loop ast.Stmt) ast.Stmt {
	if hdr == nil {
		return loop
	}
	stmts := append(append([]ast.Stmt(nil), hdr.decls...), loop)
	if hdr.yield != nil {
		// docs/119 §6: a value-form loop is an ExprBlock; captures license the loop body
		// to mutate the named outer bindings (E4 exemption). The header is the loop's
		// mutation contract.
		return &ast.ExprStmt{Position: hdr.pos, Expr: &ast.ExprBlock{Position: hdr.pos, Stmts: stmts, Value: hdr.yield, Captures: hdr.captures}}
	}
	// Statement form (no `->`): the decls are loop-private but there is no value.
	// The captures ride the loop node as its declared mutation manifest
	// (`while parser.accept(k) |parser|:` — docs/120 §9/§10 licensing).
	if w, ok := loop.(*ast.WhileStmt); ok && len(hdr.captures) > 0 {
		w.Captures = append([]string(nil), hdr.captures...)
	}
	// `if true:` is the scoped wrapper (folded at O2); ScopeStmt requires a guard.
	return &ast.IfStmt{Position: hdr.pos, Cond: &ast.BoolLit{Position: hdr.pos, Value: true}, Then: stmts}
}
