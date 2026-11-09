package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func matchPatternContainsBindNames(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case nil, *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return false
	case *ast.MatchBindPattern:
		return p.Name != "" && p.Name != "_"
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			if matchPatternContainsBindNames(elem) {
				return true
			}
		}
		return false
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			if matchPatternContainsBindNames(elem) {
				return true
			}
		}
		return false
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			if matchPatternContainsBindNames(option) {
				return true
			}
		}
		return false
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			if matchPatternContainsBindNames(arg.Pattern) {
				return true
			}
		}
		return false
	case *ast.MatchVariantPattern:
		for _, arg := range p.Args {
			if matchPatternContainsBindNames(arg.Pattern) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
func (p *Parser) parseGuardStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance()
	cond := p.parseGuardConditionExpr()
	if guardConditionIntroducesBindings(cond) {
		p.errorf("guard conditions do not support bindings; use `if value is name:` or `if ... is Variant(bind):` directly")
	}
	p.expect(lexer.TOKEN_ELSE)
	elseStmt := p.parseStmt()
	return &ast.IfStmt{
		Position: pos,
		Cond:     &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: cond},
		Then:     []ast.Stmt{elseStmt},
	}
}
func (p *Parser) looksLikeExpectPatternStmt() bool {
	if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "let" {
		depth := 0
		for i := p.pos + 2; p.hasToken(i); i++ {
			tok := p.tokenAt(i)
			switch tok.Kind {
			case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
				depth++
			case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
				if depth > 0 {
					depth--
				}
			case lexer.TOKEN_ASSIGN:
				if depth == 0 {
					return true
				}
			case lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
				if depth == 0 {
					return false
				}
			}
		}
		return false
	}
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_AS:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_ASSIGN, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}
func (p *Parser) parseExpectPatternStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("expect")
	var value ast.Expr
	var patterns []ast.MatchPattern
	if p.matchIdentText("let") {
		patterns = p.parseTopLevelMatchPatterns()
		p.expect(lexer.TOKEN_ASSIGN)
		value = p.withInMembershipDisabled(p.parseExpr)
	} else {
		value = p.withInMembershipDisabled(p.parseExpr)
		p.expect(lexer.TOKEN_AS)
		patterns = p.parseTopLevelMatchPatterns()
	}
	var body []ast.Stmt
	hasBody := false
	if p.match(lexer.TOKEN_COLON) {
		hasBody = true
		p.expectNewline()
		body = p.parseBlock()
	} else {
		p.expectNewline()
	}
	if !hasBody {
		return &ast.ExpectPatternStmt{Position: pos, Value: value, Patterns: patterns}
	}
	if len(body) == 0 {
		body = []ast.Stmt{&ast.PassStmt{Position: pos}}
	}

	arms := make([]ast.MatchArm, 0, len(patterns)+1)
	for _, pattern := range patterns {
		arms = append(arms, ast.MatchArm{
			Position: pattern.Pos(),
			Pattern:  pattern,
			Body:     body,
		})
	}
	arms = append(arms, ast.MatchArm{
		Position: pos,
		Pattern:  &ast.MatchWildcardPattern{Position: pos},
		Body: []ast.Stmt{&ast.PanicStmt{
			Position: pos,
			Message:  &ast.StringLit{Position: pos, Value: "expect pattern failed"},
		}},
	})
	return &ast.MatchStmt{Position: pos, Value: value, Arms: arms}
}
func lowerIfClauses(clauses []ifClause, elseBlock []ast.Stmt) ast.Stmt {
	tail := elseBlock
	for i := len(clauses) - 1; i >= 0; i-- {
		clause := clauses[i]
		if len(clause.Patterns) != 0 {
			arms := make([]ast.MatchArm, 0, len(clause.Patterns)+1)
			for _, pattern := range clause.Patterns {
				arms = append(arms, ast.MatchArm{Position: pattern.Pos(), Pattern: pattern, Body: clause.Body})
			}
			arms = append(arms, ast.MatchArm{Position: clause.Position, Pattern: &ast.MatchWildcardPattern{Position: clause.Position}, Body: tail})
			tail = []ast.Stmt{&ast.MatchStmt{
				Position: clause.Position,
				Value:    clause.Value,
				Store:    clause.Store,
				Arms:     arms,
			}}
			continue
		}
		if len(clause.OptionalBindings) != 0 {
			tail = []ast.Stmt{buildOptionalIfLetChain(clause.Position, clause.OptionalBindings, clause.Cond, clause.Body, tail, 0)}
			continue
		}
		// FromSource: this is the ONE site where a written block `if:`/`elif:` becomes an
		// IfStmt — the strict-flow ban (docs/125 §6b) keys on it (synthetic ifs stay false).
		tail = []ast.Stmt{&ast.IfStmt{Position: clause.Position, Hint: clause.Hint, Cond: clause.Cond, Then: clause.Body, Else: tail, FromSource: true}}
	}
	if len(tail) == 0 {
		return &ast.PassStmt{}
	}
	return tail[0]
}
func buildOptionalIfLetChain(pos lexer.Pos, bindings []optionalIfBinding, cond ast.Expr, body []ast.Stmt, elseBlock []ast.Stmt, index int) ast.Stmt {
	if index >= len(bindings) {
		if cond != nil {
			return &ast.IfStmt{Position: pos, Cond: cond, Then: body, Else: elseBlock}
		}
		if len(body) == 1 {
			return body[0]
		}
		return &ast.IfStmt{Position: pos, Cond: &ast.BoolLit{Position: pos, Value: true}, Then: body, Else: elseBlock}
	}
	binding := bindings[index]
	return &ast.IfStmt{
		Position: pos,
		Cond:     &ast.OptionalBindExpr{Position: pos, Name: binding.name, Value: binding.value},
		Then:     []ast.Stmt{buildOptionalIfLetChain(pos, bindings, cond, body, elseBlock, index+1)},
		Else:     elseBlock,
	}
}
func (p *Parser) parseWhile() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WHILE)
	hint := p.parseBranchHint()
	// docs/119 §3: a `|i = 0|` loop header ends the condition expression (the
	// condition may reference the header's decls). Scan for the boundary pipe
	// so `cond | i` bitwise-or is not mis-consumed; `IDENT =` disambiguates.
	var cond ast.Expr
	condEnd := p.pos
	depth := 0
	boundary := -1
scan:
	for p.hasToken(condEnd) {
		switch p.tokenAt(condEnd).Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_PIPE:
			if depth == 0 && p.loopHeaderDeclsAt(condEnd) {
				boundary = condEnd
				break scan
			}
		case lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				break scan
			}
		}
		condEnd++
	}
	if boundary >= 0 {
		cond = p.parseForHeaderSlice(boundary, p.tokenAt(boundary).Pos)
	} else {
		cond = p.parseExpr()
	}
	var hdr *loopHeader
	if p.loopHeaderDeclsAhead() {
		hdr = p.parseLoopHeader()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.loopBreakTargets = append(p.loopBreakTargets, loopBreakTarget(hdr))
	body := p.parseBlock()
	p.loopBreakTargets = p.loopBreakTargets[:len(p.loopBreakTargets)-1]
	return p.wrapLoopHeader(hdr, &ast.WhileStmt{Position: pos, Hint: hint, Cond: cond, Body: body})
}
func (p *Parser) parseStaticStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)

	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		return &ast.StaticBlockStmt{Position: pos, Body: p.parseStaticOnlyBlock()}
	}
	if p.peek() == lexer.TOKEN_ERROR {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		msg := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		p.expectNewline()
		return &ast.StaticErrorStmt{Position: pos, Message: msg}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "assert" {
		p.advance()
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			return &ast.StaticAssertBlockStmt{Position: pos, Assertions: p.parseStaticAssertItemBlock()}
		}
		cond := p.parseExpr()
		var msg ast.Expr
		if p.match(lexer.TOKEN_COMMA) {
			msg = p.parseExpr()
		}
		p.expectNewline()
		return &ast.StaticAssertStmt{Position: pos, Cond: cond, Message: msg}
	}

	if p.peek() != lexer.TOKEN_IF {
		expr := p.parseExpr()
		p.expectNewlineAfterValueExpr(expr)
		return &ast.StaticBlockStmt{Position: pos, Body: []ast.Stmt{&ast.ExprStmt{Position: pos, Expr: expr}}}
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
			elifs = append(elifs, ast.StaticElifClause{Position: p.tokenAt(saved).Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseBlock()
			break
		} else {
			p.seek(saved)
			break
		}
	}

	return &ast.StaticIfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseStaticOnlyBlock() []ast.Stmt {
	p.expect(lexer.TOKEN_INDENT)
	stmts := make([]ast.Stmt, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		stmt := p.parseStaticOnlyStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return stmts
}

func (p *Parser) parseStaticOnlyStmt() ast.Stmt {
	pos := p.cur().Pos
	switch p.peek() {
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	case lexer.TOKEN_IF:
		return p.parseStaticOnlyIfStmt()
	case lexer.TOKEN_ERROR:
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		msg := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		p.expectNewline()
		return &ast.StaticErrorStmt{Position: pos, Message: msg}
	case lexer.TOKEN_PASS:
		return p.parsePass()
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "assert" {
			p.advance()
			if p.match(lexer.TOKEN_COLON) {
				p.expectNewline()
				return &ast.StaticAssertBlockStmt{Position: pos, Assertions: p.parseStaticAssertItemBlock()}
			}
			cond := p.parseExpr()
			var msg ast.Expr
			if p.match(lexer.TOKEN_COMMA) {
				msg = p.parseExpr()
			}
			p.expectNewline()
			return &ast.StaticAssertStmt{Position: pos, Cond: cond, Message: msg}
		}
	}
	return p.parseStmt()
}

func (p *Parser) parseStaticOnlyIfStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseStaticOnlyBlock()

	var elifs []ast.StaticElifClause
	var elseBlock []ast.Stmt

	for p.skipNewlines(); p.peek() == lexer.TOKEN_ELIF || p.peek() == lexer.TOKEN_ELSE; p.skipNewlines() {
		if p.peek() == lexer.TOKEN_ELIF {
			elifPos := p.cur().Pos
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseStaticOnlyBlock()
			elifs = append(elifs, ast.StaticElifClause{Position: elifPos, Cond: elifCond, Body: elifBody})
			continue
		}
		p.expect(lexer.TOKEN_ELSE)
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elseBlock = p.parseStaticOnlyBlock()
		break
	}

	return &ast.StaticIfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseMoveBindPattern() ast.MoveBindPattern {
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(p.cur().Pos, "")
	}
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.peek() == lexer.TOKEN_DOT || p.peek() == lexer.TOKEN_SCOPE {
		parts := p.parseVariantPathName(pos, name)
		name = strings.Join(parts[:len(parts)-1], ".")
		variant := parts[len(parts)-1]
		args := make([]ast.MatchPatternArg, 0)
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseMoveBindVariantArg())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		return &ast.MoveBindVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(pos, name)
	}
	if !p.match(lexer.TOKEN_LPAREN) {
		return &ast.MoveBindNamePattern{Position: pos, Name: name}
	}
	args := make([]ast.MoveBindArg, 0)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			argPos := p.cur().Pos
			argName := p.expect(lexer.TOKEN_IDENT).Text
			args = append(args, ast.MoveBindArg{Position: argPos, Name: argName})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.MoveBindStructPattern{Position: pos, TypeName: name, Args: args}
}
func (p *Parser) peekQualifiedStructDestructurePattern() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	i := p.pos + 1
	for p.hasToken(i+1) && (p.tokenAt(i).Kind == lexer.TOKEN_DOT || p.tokenAt(i).Kind == lexer.TOKEN_SCOPE) && p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT {
		i += 2
	}
	return p.hasToken(i) && p.tokenAt(i).Kind == lexer.TOKEN_LBRACE
}
func (p *Parser) parseMoveBindStructBracePattern(pos lexer.Pos, typeName string) *ast.MoveBindStructPattern {
	p.expect(lexer.TOKEN_LBRACE)
	args := make([]ast.MoveBindArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	if p.peek() != lexer.TOKEN_RBRACE {
		for {
			args = append(args, p.parseMoveBindStructBraceArg())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACE {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.MoveBindStructPattern{Position: pos, TypeName: typeName, Args: args, Brace: true}
}
func (p *Parser) parseMoveBindStructBraceArg() ast.MoveBindArg {
	pos := p.cur().Pos
	field := p.expect(lexer.TOKEN_IDENT).Text
	name := field
	if p.match(lexer.TOKEN_COLON) {
		name = p.expect(lexer.TOKEN_IDENT).Text
	}
	return ast.MoveBindArg{Position: pos, Field: field, Name: name}
}
func (p *Parser) parseMoveBindVariantArg() ast.MatchPatternArg {
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		pattern := p.parseMoveBindVariantBindingPattern()
		return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
	}
	pattern := p.parseMoveBindVariantBindingPattern()
	return ast.MatchPatternArg{Position: pattern.Pos(), Pattern: pattern}
}
func (p *Parser) parseMoveBindVariantBindingPattern() ast.MatchPattern {
	return p.parseNestedMatchPattern()
}
