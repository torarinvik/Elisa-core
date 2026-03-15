package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

// ---------- Block / Statements ----------

func (p *Parser) parseBlock() []ast.Stmt {
	p.expect(lexer.TOKEN_INDENT)
	var stmts []ast.Stmt
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		stmt := p.parseStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return stmts
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		return p.parseReturn()
	case lexer.TOKEN_PASS:
		return p.parsePass()
	case lexer.TOKEN_PANIC:
		return p.parsePanic()
	case lexer.TOKEN_IF:
		return p.parseIf()
	case lexer.TOKEN_WHILE:
		return p.parseWhile()
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_RETURN)
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		value = p.parseExpr()
	}
	p.expectNewline()
	return &ast.ReturnStmt{Position: pos, Value: value}
}

func (p *Parser) parsePass() *ast.PassStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PASS)
	p.expectNewline()
	return &ast.PassStmt{Position: pos}
}

func (p *Parser) parsePanic() *ast.PanicStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PANIC)
	p.expect(lexer.TOKEN_LPAREN)
	msg := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	p.expectNewline()
	return &ast.PanicStmt{Position: pos, Message: msg}
}

func (p *Parser) parseIf() *ast.IfStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseBlock()

	var elifs []ast.ElifClause
	var elseBlock []ast.Stmt

	for p.peek() == lexer.TOKEN_ELIF {
		elifPos := p.cur().Pos
		p.advance()
		elifCond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elifBody := p.parseBlock()
		elifs = append(elifs, ast.ElifClause{Position: elifPos, Cond: elifCond, Body: elifBody})
	}

	if p.match(lexer.TOKEN_ELSE) {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elseBlock = p.parseBlock()
	}

	return &ast.IfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseWhile() *ast.WhileStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WHILE)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.WhileStmt{Position: pos, Cond: cond, Body: body}
}

func (p *Parser) parseStaticStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)

	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "error" {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		msg := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		p.expectNewline()
		return &ast.StaticErrorStmt{Position: pos, Message: msg}
	}

	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseBlock()

	var elifs []ast.StaticElifClause
	var elseBlock []ast.Stmt

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseBlock()
			elifs = append(elifs, ast.StaticElifClause{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

// parseExprOrAssignStmt: handles expressions, assignments, var decls, discards
func (p *Parser) parseExprOrAssignStmt() ast.Stmt {
	pos := p.cur().Pos

	// Discard: _ = expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		p.advance() // _
		p.advance() // =
		value := p.parseExpr()
		p.expectNewline()
		return &ast.DiscardStmt{Position: pos, Value: value}
	}

	// Variable declaration: name: [mutable] Type [= value]
	// But NOT name:mutable (no space) which would be field:Type
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		colonPos := p.pos + 1
		afterColon := lexer.TOKEN_EOF
		if colonPos+1 < len(p.tokens) {
			afterColon = p.tokens[colonPos+1].Kind
		}
		if afterColon == lexer.TOKEN_IDENT || afterColon == lexer.TOKEN_MUTABLE || afterColon == lexer.TOKEN_TAIL {
			name := p.cur().Text
			p.advance()
			p.advance()

			mutable := false
			if p.match(lexer.TOKEN_MUTABLE) {
				mutable = true
			}

			typ := p.parseTypeExpr()

			var value ast.Expr
			if p.match(lexer.TOKEN_ASSIGN) {
				value = p.parseExpr()
			}
			p.expectNewline()
			return &ast.VarDeclStmt{Position: pos, Name: name, Mutable: mutable, Type: typ, Value: value}
		}
	}

	expr := p.parseExpr()

	switch p.peek() {
	case lexer.TOKEN_LARROW:
		p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AssignStmt{Position: pos, Target: expr, Value: value}

	case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ, lexer.TOKEN_STAREQ, lexer.TOKEN_SLASHEQ, lexer.TOKEN_PERCENTEQ,
		lexer.TOKEN_CARETEQ, lexer.TOKEN_PIPEEQ, lexer.TOKEN_AMPEQ,
		lexer.TOKEN_LSHIFTEQ, lexer.TOKEN_RSHIFTEQ:
		op := p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AugAssignStmt{Position: pos, Op: op.Kind, Target: expr, Value: value}

	case lexer.TOKEN_AS:
		p.advance()
		var asKind string
		if p.match(lexer.TOKEN_AMPERSAND) {
			asKind = "&"
		} else if p.match(lexer.TOKEN_BANG) {
			asKind = "!"
		}
		p.expect(lexer.TOKEN_LARROW)
		value := p.parseExpr()
		p.expectNewline()
		return &ast.AsRefAssignStmt{Position: pos, Target: expr, AsKind: asKind, Value: value}
	}

	if ident, ok := expr.(*ast.Ident); ok && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		arg := p.parseExpr()
		p.expectNewline()
		return &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
			Position: pos,
			Func:     ident,
			Args:     []ast.Expr{arg},
		}}
	}

	p.expectNewline()
	return &ast.ExprStmt{Position: pos, Expr: expr}
}
