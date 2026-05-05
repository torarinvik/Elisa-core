package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseMatchValuePatternExpr() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: false}
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: true}
	case lexer.TOKEN_FLOAT_LIT:
		tok := p.advance()
		return &ast.FloatLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix}
	case lexer.TOKEN_CHAR_LIT:
		tok := p.advance()
		return &ast.CharLit{Position: tok.Pos, Value: tok.Text}
	case lexer.TOKEN_TRUE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: true}
	case lexer.TOKEN_FALSE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: false}
	case lexer.TOKEN_NULL:
		tok := p.advance()
		return &ast.NullLit{Position: tok.Pos}
	case lexer.TOKEN_DOT:
		return p.parsePrimary()
	case lexer.TOKEN_IDENT:
		pos := p.cur().Pos
		parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
		if !p.match(lexer.TOKEN_DOT) {
			p.errorf("value pattern expects a literal or qualified member")
			return &ast.Ident{Position: pos, Name: parts[0]}
		}
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		for p.match(lexer.TOKEN_DOT) {
			parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		}
		return buildQualifiedMatchValueExpr(pos, parts)
	case lexer.TOKEN_MINUS:
		pos := p.cur().Pos
		p.advance()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: p.parseMatchValuePatternExpr()}
	case lexer.TOKEN_LPAREN:
		pos := p.cur().Pos
		p.advance()
		inner := p.parseMatchValuePatternExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: pos, Inner: inner}
	default:
		p.errorf("match value pattern expects a literal or qualified member")
		return &ast.IntLit{Position: p.cur().Pos, Value: "0"}
	}
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
func (p *Parser) parseScopeStmt() *ast.ScopeStmt {
	pos := p.cur().Pos
	p.expectIdentText("scope")
	guard := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return &ast.ScopeStmt{Position: pos, Guard: guard, Body: p.parseBlock()}
}
func (p *Parser) parseDestroy() *ast.DestroyStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.DestroyStmt{Position: pos, Name: name}
}
func (p *Parser) parseMark() *ast.MarkStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_AS)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.MarkStmt{Position: pos, RegionName: regionName, Name: name}
}
func (p *Parser) parseCheckpointStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("checkpoint")
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		target := p.parseExpr()
		var body []ast.Stmt
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			body = p.parseBlock()
		} else {
			p.expectNewline()
		}
		return &ast.CheckpointStmt{Position: pos, Name: name, Target: target, Body: body}
	}
	firstTarget := p.parseExpr()
	targets := []ast.Expr{firstTarget}
	if !p.match(lexer.TOKEN_COMMA) {
		p.errorf("grouped checkpoint requires at least 2 targets")
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		return &ast.GroupedCheckpointStmt{Position: pos, Targets: targets, Body: p.parseBlock()}
	}
	targets = append(targets, p.parseExpr())
	for p.match(lexer.TOKEN_COMMA) {
		targets = append(targets, p.parseExpr())
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return &ast.GroupedCheckpointStmt{Position: pos, Targets: targets, Body: p.parseBlock()}
}
func (p *Parser) parseRestore() *ast.RestoreStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("from")
	markName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreStmt{Position: pos, RegionName: regionName, MarkName: markName}
}
func (p *Parser) parseRestoreCheckpointStmt() *ast.RestoreCheckpointStmt {
	pos := p.cur().Pos
	p.expectIdentText("restore")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreCheckpointStmt{Position: pos, Name: name}
}
func (p *Parser) parseReset() *ast.ResetStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.ResetStmt{Position: pos, Name: name}
}
func (p *Parser) parseReturn() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_RETURN)
	if p.match(lexer.TOKEN_QUESTION) {
		if p.peek() == lexer.TOKEN_WITH {
			return p.parseOptionalReturnWith(pos)
		}
		value := p.withTernaryDisabled(p.parseValueExprAllowTuple)
		if p.match(lexer.TOKEN_IF) {
			cond := p.parseExpr()
			p.expectNewlineAfterValueExpr(cond)
			return &ast.IfStmt{
				Position: pos,
				Cond:     cond,
				Then:     []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: value}},
			}
		}
		p.expectNewlineAfterValueExpr(value)
		name := "__return_optional"
		return &ast.IfStmt{
			Position: pos,
			Cond:     &ast.OptionalBindExpr{Position: pos, Name: name, Value: value},
			Then:     []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: name}}},
		}
	}
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		value = p.parseValueExprAllowTuple()
	}
	p.expectNewlineAfterValueExpr(value)
	return &ast.ReturnStmt{Position: pos, Value: value}
}

type optionalReturnWithBinding struct {
	name  string
	value ast.Expr
}

func (p *Parser) parseOptionalReturnWith(pos lexer.Pos) ast.Stmt {
	p.expect(lexer.TOKEN_WITH)
	bindings := make([]optionalReturnWithBinding, 0, 2)
	for {
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.withTernaryDisabled(p.parseExpr)
		bindings = append(bindings, optionalReturnWithBinding{name: name, value: value})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		p.skipOptionalReturnWithBindingTrivia()
	}
	if len(bindings) == 0 {
		p.errorAt(pos, "return? with requires at least one optional binding")
	}
	value := p.parseOptionalReturnWithValue(pos)
	return buildOptionalReturnWithChain(pos, bindings, value, 0)
}
func (p *Parser) skipOptionalReturnWithBindingTrivia() {
	p.skipNewlines()
	if p.peek() == lexer.TOKEN_INDENT {
		p.advance()
	}
}
func (p *Parser) parseOptionalReturnWithValue(pos lexer.Pos) ast.Expr {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	consumedBlockIndent := false
	if p.match(lexer.TOKEN_INDENT) {
		consumedBlockIndent = true
	} else {
		// Multiline binding lists can leave the lexer unwinding continuation
		// indentation before the expression body.
		for p.peek() == lexer.TOKEN_DEDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_EOF {
			p.advance()
			if p.peek() != lexer.TOKEN_DEDENT {
				break
			}
		}
	}
	value := p.parseValueExprAllowTuple()
	p.expectNewlineAfterValueExpr(value)
	if consumedBlockIndent {
		p.expect(lexer.TOKEN_DEDENT)
	}
	if value == nil {
		p.errorAt(pos, "return? with requires a value expression body")
		return &ast.NullLit{Position: pos}
	}
	return value
}
func buildOptionalReturnWithChain(pos lexer.Pos, bindings []optionalReturnWithBinding, value ast.Expr, index int) ast.Stmt {
	if index >= len(bindings) {
		return &ast.ReturnStmt{Position: pos, Value: value}
	}
	binding := bindings[index]
	return &ast.IfStmt{
		Position: pos,
		Cond:     &ast.OptionalBindExpr{Position: pos, Name: binding.name, Value: binding.value},
		Then:     []ast.Stmt{buildOptionalReturnWithChain(pos, bindings, value, index+1)},
	}
}
func (p *Parser) parseValueExprAllowTuple() ast.Expr {
	first := p.parseExpr()
	if p.peek() != lexer.TOKEN_COMMA {
		return first
	}
	return p.parseTupleExprFromFirst(first.Pos(), first)
}
func (p *Parser) tryParseTupleBindStmt(pos lexer.Pos) ast.Stmt {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_COMMA {
		return nil
	}
	savedPos := p.pos
	names := make([]ast.TupleBindName, 0, 4)
	for {
		if p.peek() != lexer.TOKEN_IDENT {
			p.pos = savedPos
			return nil
		}
		tok := p.advance()
		names = append(names, ast.TupleBindName{Position: tok.Pos, Name: tok.Text})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	declare := false
	switch p.peek() {
	case lexer.TOKEN_ASSIGN:
		declare = true
		p.advance()
	case lexer.TOKEN_LARROW:
		p.advance()
	default:
		p.pos = savedPos
		return nil
	}
	value := p.parseValueExprAllowTuple()
	p.expectNewline()
	return &ast.TupleBindStmt{Position: pos, Names: names, Declare: declare, Value: value}
}
func (p *Parser) parseLetDestructureStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("let")
	pattern := p.parseLetDestructurePattern()
	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseValueExprAllowTuple()
	p.expectNewlineAfterValueExpr(value)
	return &ast.LetDestructureStmt{Position: pos, Pattern: pattern, Value: value}
}
func (p *Parser) parseLetDestructurePattern() *ast.MoveBindStructPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(pos, "")
	}
	if p.peekQualifiedStructDestructurePattern() {
		typeName := p.parseQualifiedDeclName()
		return p.parseMoveBindStructBracePattern(pos, typeName)
	}
	p.errorf("let destructuring expects {...} or Type{...}")
	return &ast.MoveBindStructPattern{Position: pos, Brace: true}
}
func (p *Parser) parsePass() *ast.PassStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PASS)
	p.expectNewline()
	return &ast.PassStmt{Position: pos}
}
func (p *Parser) parseSignalStmt() *ast.SignalStmt {
	pos := p.cur().Pos
	p.expectIdentText("signal")
	ref := p.parsePermissionRef()
	p.expectNewline()
	return &ast.SignalStmt{Position: pos, Permissions: []ast.PermissionRef{ref}}
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

type ifClause struct {
	Position         lexer.Pos
	Hint             ast.BranchHint
	Cond             ast.Expr
	Value            ast.Expr
	Store            ast.Expr
	Patterns         []ast.MatchPattern
	OptionalBindings []optionalReturnWithBinding
	Body             []ast.Stmt
}

func (p *Parser) parseBranchHint() ast.BranchHint {
	if p.matchIdentText("likely") {
		return ast.BranchHintLikely
	}
	if p.matchIdentText("unlikely") {
		return ast.BranchHintUnlikely
	}
	return ast.BranchHintNone
}
func (p *Parser) parseIf() ast.Stmt {
	p.expect(lexer.TOKEN_IF)
	first := p.parseIfClause(false)
	clauses := []ifClause{first}

	for p.peek() == lexer.TOKEN_ELIF {
		p.advance()
		clauses = append(clauses, p.parseIfClause(true))
	}

	var elseBlock []ast.Stmt
	if p.match(lexer.TOKEN_ELSE) {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		elseBlock = p.parseBlock()
	}

	return lowerIfClauses(clauses, elseBlock)
}
func (p *Parser) parseIfClause(isElif bool) ifClause {
	pos := p.cur().Pos
	hint := p.parseBranchHint()
	if p.peekIdentText("let") {
		return p.parseIfLetClause(pos, hint, isElif)
	}
	headStart := p.pos
	head := p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	if p.peek() == lexer.TOKEN_IN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET {
		p.pos = headStart
		cond := p.withAsCastDisabled(p.parseExpr)
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	if p.match(lexer.TOKEN_AS) {
		if hint != ast.BranchHintNone {
			if isElif {
				p.errorf("elif likely/unlikely hint cannot be combined with pattern binders")
			} else {
				p.errorf("if likely/unlikely hint cannot be combined with pattern binders")
			}
		}
		patterns := p.parseTopLevelMatchPatterns()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
		return ifClause{Position: pos, Hint: hint, Value: head, Patterns: patterns, Body: body}
	}
	if p.match(lexer.TOKEN_IN) {
		if hint != ast.BranchHintNone {
			if isElif {
				p.errorf("elif likely/unlikely hint cannot be combined with pattern binders")
			} else {
				p.errorf("if likely/unlikely hint cannot be combined with pattern binders")
			}
		}
		store := p.withAsCastDisabled(p.parseExpr)
		if p.match(lexer.TOKEN_AS) {
			patterns := p.parseTopLevelMatchPatterns()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			body := p.parseBlock()
			return ifClause{Position: pos, Hint: hint, Value: head, Store: store, Patterns: patterns, Body: body}
		}
		p.pos = headStart
		cond := p.withAsCastDisabled(p.parseExpr)
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return ifClause{Position: pos, Hint: hint, Cond: head, Body: body}
}
func (p *Parser) parseIfLetClause(pos lexer.Pos, hint ast.BranchHint, isElif bool) ifClause {
	if hint != ast.BranchHintNone {
		if isElif {
			p.errorf("elif likely/unlikely hint cannot be combined with optional binders")
		} else {
			p.errorf("if likely/unlikely hint cannot be combined with optional binders")
		}
	}
	p.expectIdentText("let")
	bindings := make([]optionalReturnWithBinding, 0, 2)
	for {
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseNot()
		bindings = append(bindings, optionalReturnWithBinding{name: name, value: value})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		p.skipNewlines()
	}
	var cond ast.Expr
	if p.match(lexer.TOKEN_AND) {
		cond = p.parseExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return ifClause{Position: pos, Cond: cond, OptionalBindings: bindings, Body: body}
}
func (p *Parser) parseGuardConditionExpr() ast.Expr {
	depth := 0
	start := p.pos
	end := start
	for end < len(p.tokens) {
		tok := p.tokens[end]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_ELSE:
			if depth == 0 {
				goto parse
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			goto parse
		}
		end++
	}

parse:
	if end == start {
		p.errorf("guard requires a condition before else")
		return &ast.BoolLit{Position: p.cur().Pos, Value: false}
	}
	slice := append([]lexer.Token(nil), p.tokens[start:end]...)
	eofPos := slice[len(slice)-1].Pos
	slice = append(slice, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: eofPos})
	sub := New(slice)
	sub.poolScopes = append([]string(nil), p.poolScopes...)
	expr := sub.parseExpr()
	p.errors = append(p.errors, sub.Errors()...)
	p.pos = end
	return expr
}
func guardConditionIntroducesBindings(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.OptionalBindExpr:
		return true
	case *ast.ParenExpr:
		return guardConditionIntroducesBindings(n.Inner)
	case *ast.UnaryExpr:
		return guardConditionIntroducesBindings(n.Operand)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			return guardConditionIntroducesBindings(n.Left) || guardConditionIntroducesBindings(n.Right)
		}
		if n.Op != lexer.TOKEN_IS {
			return false
		}
		switch test := n.Right.(type) {
		case *ast.StructTestExpr:
			return matchPatternContainsBindNames(test.Pattern)
		case *ast.VariantTestExpr:
			return matchPatternContainsBindNames(test.Pattern)
		default:
			return false
		}
	default:
		return false
	}
}
