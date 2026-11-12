package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parserdesugar"
	"elisacore/src/parserregionscan"
)

// exprBlockTailNeedsAutoRegion classifies a direct return value or an ExprBlock
// tail that needs an ambient owner. A comprehension or `each` query needs an
// active scope when it has no explicit owner. A plain top-level list literal
// remains region-polymorphic, but a list literal nested in a tuple or branch
// may need a region for its contextual container type. Struct literals with
// allocating container fields need an ambient owner as well.
func isAllocatingLiteral(value ast.Expr) bool { return parserregionscan.IsAllocatingLiteral(value) }

// isInferredDArrayBuilder reports whether value is an expression that constructs
// a darray without needing a declared container type as context.
func isInferredDArrayBuilder(value ast.Expr) bool {
	return parserregionscan.InferredDArrayBuilder(value)
}
func blockLaterUsesUntypedListLiteralAsDArray(stmts []ast.Stmt, name string) bool {
	return parserregionscan.BlockLaterUsesUntypedListLiteralAsDArray(stmts, name)
}
func isRegionlessContainerType(typ ast.TypeExpr) bool {
	return parserregionscan.IsRegionlessContainerType(typ)
}

// desugarDStrStringLiteralInit rewrites `s: dstr = "abc"` into `s: dstr = [97, 98, 99]` — the
// byte-list literal a user would otherwise hand-write (`['a'.u8(), ...]`). `dstr` is the u8
// specialization of darray, so a bare string literal (otherwise a static `cstr`) does not assign to
// it. Desugaring at parse time — before functionBodyNeedsAutoRegion runs — means the result is an
// ordinary allocating list literal: it triggers the inferred auto-region wrap and flows through the
// identical growable-darray path (region backing, escape checks, codegen), adding no new surface.
//
// StringLit.Value is the already-decoded raw byte sequence (escapes resolved by the lexer; the
// backend emits these bytes verbatim), so each byte becomes a suffix-less IntLit whose type is
// driven to u8 by the darray[u8] list-literal context (no discouraged-suffix warning). An empty
// string yields an empty `[]`. The value is only rewritten when the declared type is a region-less
// `dstr`; any other type (including a `cstr`/view) is left untouched.
func desugarDStrStringLiteralInit(typ ast.TypeExpr, value ast.Expr) ast.Expr {
	return parserdesugar.DStrStringLiteralInit(typ, value)
}

func desugarDStrReturnLiterals(body []ast.Stmt, retType ast.TypeExpr) {
	parserdesugar.DStrReturnLiterals(body, retType)
}

func (p *Parser) parseCanStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("can")
	permissions := p.parsePermissionRefs(false)
	var handlerName string
	var handlerArgs []ast.Expr
	if p.match(lexer.TOKEN_WITH) {
		handlerName = p.expect(lexer.TOKEN_IDENT).Text
		// A zero-capture handler may be installed as `with Noop` as well as
		// `with Noop()`. Keeping both forms preserves the concise static-handler
		// spelling without changing the AST or lowering strategy.
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					handlerArgs = append(handlerArgs, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
	}
	var asTarget string
	if p.match(lexer.TOKEN_AS) {
		asTarget = p.expect(lexer.TOKEN_IDENT).Text
		for p.match(lexer.TOKEN_DOT) {
			asTarget += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, As: asTarget, HandlerName: handlerName, HandlerArgs: handlerArgs}
}
func (p *Parser) parseTrustedStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("trusted")
	permissions := p.parsePermissionRefs(false)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, SuppressPermissionInference: true}
}
func (p *Parser) parseWithArenaStmt() *ast.RegionStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WITH)
	p.expectIdentText("arena")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var capacity ast.Expr
	if p.match(lexer.TOKEN_LPAREN) {
		capacity = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_AS)
	ownerName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity, OwnerName: ownerName, Body: body}
}

// parseCascadeStmt recognizes the removed `cascade <target>:` statement and rejects it. The cascade
// feature (method-cascade sugar) has been removed; the target and body are consumed for recovery.
func (p *Parser) parseCascadeStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("cascade")
	p.errorAt(pos, "the `cascade` statement has been removed")
	_ = p.parseExpr()
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		_ = p.parseBlock()
	}
	return &ast.PassStmt{Position: pos}
}
