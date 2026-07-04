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
	pos   lexer.Pos
	decls []ast.Stmt
	yield ast.Expr
}

// loopHeaderDeclsAt reports whether tokens[i:] begins a loop-header decl list:
// `| IDENT =`. The `IDENT =` requirement is what disambiguates from a bitwise
// `|` in the iterable/condition expression — `=` cannot appear inside an
// expression, so `xs | mask` can never match. (Bare-name captures relax this
// in the docs/119 §6 batch.)
func (p *Parser) loopHeaderDeclsAt(i int) bool {
	return i+2 < len(p.tokens) &&
		p.tokens[i].Kind == lexer.TOKEN_PIPE &&
		p.tokens[i+1].Kind == lexer.TOKEN_IDENT &&
		p.tokens[i+2].Kind == lexer.TOKEN_ASSIGN
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
		p.expect(lexer.TOKEN_ASSIGN)
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
		hdr.decls = append(hdr.decls, &ast.VarDeclStmt{Position: nameTok.Pos, Name: nameTok.Text, Mutable: true, Value: init})
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
		return &ast.ExprStmt{Position: hdr.pos, Expr: &ast.ExprBlock{Position: hdr.pos, Stmts: stmts, Value: hdr.yield}}
	}
	// Statement form (no `->`): the decls are loop-private but there is no value.
	// `if true:` is the scoped wrapper (folded at O2); ScopeStmt requires a guard.
	return &ast.IfStmt{Position: hdr.pos, Cond: &ast.BoolLit{Position: hdr.pos, Value: true}, Then: stmts}
}
