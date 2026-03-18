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
	if p.peek() == lexer.TOKEN_IDENT {
		switch p.cur().Text {
		case "region":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseRegion()
			}
		case "destroy":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseDestroy()
			}
		}
	}
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		return p.parseReturn()
	case lexer.TOKEN_PASS:
		return p.parsePass()
	case lexer.TOKEN_PANIC:
		return p.parsePanic()
	case lexer.TOKEN_IF:
		return p.parseIf()
	case lexer.TOKEN_MATCH:
		return p.parseMatch()
	case lexer.TOKEN_WHILE:
		return p.parseWhile()
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
}

func (p *Parser) parseMatch() *ast.MatchStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	value := p.parseExpr()
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchStmt{Position: pos, Value: value, Store: store, Arms: arms}
}

func (p *Parser) parseMatchArms() []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.MatchArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMatchArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}

func (p *Parser) parseMatchArm() ast.MatchArm {
	pos := p.cur().Pos
	pattern := p.parseMatchPattern()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return ast.MatchArm{Position: pos, Pattern: pattern, Body: body}
}

func (p *Parser) parseMatchPattern() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	switch pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchVariantPattern:
		return pattern
	default:
		p.errorf("top-level match arm must use Enum.Variant(...) or _")
		return pattern
	}
}

func (p *Parser) parseNestedMatchPattern() ast.MatchPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		return &ast.MatchWildcardPattern{Position: pos}
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_DOT) {
		return &ast.MatchBindPattern{Position: pos, Name: name}
	}
	variant := p.expect(lexer.TOKEN_IDENT).Text
	args := make([]ast.MatchPatternArg, 0)
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseMatchPatternArg())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return &ast.MatchVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
}

func (p *Parser) parseMatchPatternArg() ast.MatchPatternArg {
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		pattern := p.parseNestedMatchPattern()
		return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
	}
	pattern := p.parseNestedMatchPattern()
	return ast.MatchPatternArg{Position: pattern.Pos(), Pattern: pattern}
}

func (p *Parser) parseRegion() *ast.RegionStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	var capacity ast.Expr
	if p.match(lexer.TOKEN_LPAREN) {
		capacity = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expectNewline()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity}
}

func (p *Parser) parseDestroy() *ast.DestroyStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.DestroyStmt{Position: pos, Name: name}
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

	if p.peek() == lexer.TOKEN_ERROR {
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
		if afterColon == lexer.TOKEN_IDENT || afterColon == lexer.TOKEN_MUTABLE || afterColon == lexer.TOKEN_TAIL ||
			afterColon == lexer.TOKEN_ANY || afterColon == lexer.TOKEN_HEAP || afterColon == lexer.TOKEN_STACK || afterColon == lexer.TOKEN_STATIC {
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

	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		name := p.cur().Text
		p.advance()
		p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.VarDeclStmt{Position: pos, Name: name, Value: value}
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
