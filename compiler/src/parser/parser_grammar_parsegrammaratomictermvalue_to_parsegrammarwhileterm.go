package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseGrammarAtomicTermValue() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_STRING_LIT {
		lit := p.advance()
		return &ast.GrammarTokenTerm{Position: pos, Value: lit.Text}
	}
	if p.peekGrammarTokenKindTerm() && !p.peekGrammarTokenKindBinding() {
		return p.parseGrammarTokenKindTerm()
	}
	if p.peekGrammarPrecedenceTerm() {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekGrammarBlockTerm("suffix") {
		return p.parseGrammarSuffixTerm()
	}
	if p.peekGrammarBlockTerm("postfix") {
		return p.parseGrammarPostfixTerm()
	}
	if p.peekGrammarBlockTerm("infix") {
		return p.parseGrammarInfixTableTerm()
	}
	if p.peekIdentText("choice") {
		return p.parseGrammarChoiceTerm()
	}
	if p.peekIdentText("when") {
		return p.parseGrammarWhenTerm()
	}
	if p.peek() == lexer.TOKEN_MATCH {
		return p.parseGrammarMatchTerm()
	}
	if p.peekIdentText("required") {
		return p.parseGrammarRequiredTerm()
	}
	if p.peekIdentText("delimited") {
		return p.parseGrammarDelimitedTerm()
	}
	if p.peekIdentText("seq") {
		return p.parseGrammarSeqTerm()
	}
	if p.peekIdentText("prefix") {
		return p.parseGrammarPrefixTerm()
	}
	if p.peekIdentText("lookahead") {
		return p.parseGrammarLookaheadTerm()
	}
	if p.peekIdentText("expr") {
		return p.parseGrammarExprTerm()
	}
	if p.peekIdentText("first") {
		return p.parseGrammarFirstTerm()
	}
	if p.peekIdentText("singleton") {
		return p.parseGrammarSingletonTerm()
	}
	if p.peekIdentText("empty") {
		return p.parseGrammarEmptyTerm()
	}
	if p.peekIdentText("none") {
		return p.parseGrammarNoneTerm()
	}
	if p.peekIdentText("guard") {
		return p.parseGrammarGuardTerm()
	}
	if p.peekIdentText("attempt") {
		return p.parseGrammarAttemptTerm()
	}
	if p.peek() == lexer.TOKEN_LBRACKET && p.isGrammarWhileTermStart() {
		return p.parseGrammarWhileTerm()
	}
	if p.peek() == lexer.TOKEN_LBRACKET {
		expr := p.parseExpr()
		return &ast.GrammarExprTerm{Position: expr.Pos(), Expr: expr}
	}
	if p.peekIdentText("apply") {
		return p.parseGrammarApplyTerm()
	}
	if p.peekIdentText("cut") {
		return p.parseGrammarCutTerm()
	}
	if p.peekIdentText("optional") {
		return p.parseGrammarOptionalTerm()
	}
	if p.peekIdentText("list") {
		return p.parseGrammarListTerm()
	}
	if p.peekIdentText("flatrepeat") {
		return p.parseGrammarFlatRepeatTerm()
	}
	if p.peekIdentText("repeat") {
		return p.parseGrammarRepeatTerm()
	}
	if p.peekIdentText("separated") {
		return p.parseGrammarSeparatedTerm()
	}
	name := p.parseQualifiedDeclName()
	explicit := false
	args := make([]ast.Expr, 0, 2)
	if p.match(lexer.TOKEN_LPAREN) {
		explicit = true
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			applyArgs := p.parseGrammarApplyArgsUntilRParen()
			return &ast.GrammarApplyTerm{Position: pos, Name: name, Direct: true, Args: applyArgs}
		}
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseExpr())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return &ast.GrammarCallTerm{Position: pos, Name: name, Explicit: explicit, Args: args}
}
func (p *Parser) parseGrammarApplyTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("apply")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	args := p.parseGrammarApplyArgsUntilRParen()
	return &ast.GrammarApplyTerm{Position: pos, Name: name, Args: args}
}
func (p *Parser) parseGrammarApplyArgsUntilRParen() []ast.GrammarApplyArg {
	args := make([]ast.GrammarApplyArg, 0, 4)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			argPos := p.cur().Pos
			name := ""
			if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
				name = p.advance().Text
				p.expect(lexer.TOKEN_COLON)
			}
			args = append(args, ast.GrammarApplyArg{Position: argPos, Name: name, Term: p.parseGrammarUntilStopTerm()})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return args
}
func (p *Parser) parseGrammarInfixTableTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("infix")
	p.expect(lexer.TOKEN_LPAREN)
	tableName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarInfixTableTerm{Position: pos, TableName: tableName}
}
func (p *Parser) peekGrammarTokenKindTerm() bool {
	return p.peek() == lexer.TOKEN_DOT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
}
func (p *Parser) peekGrammarTokenKindBinding() bool {
	return p.peekGrammarTokenKindTerm() && p.pos+4 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN && p.tokens[p.pos+3].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+4].Kind == lexer.TOKEN_RPAREN
}
func (p *Parser) parseGrammarTokenKindTerm() *ast.GrammarTokenKindTerm {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DOT)
	kind := p.expect(lexer.TOKEN_IDENT).Text
	return &ast.GrammarTokenKindTerm{Position: pos, Kind: kind}
}
func (p *Parser) parseGrammarTokenKindBinding() *ast.GrammarBindTerm {
	term := p.parseGrammarTokenKindTerm()
	p.expect(lexer.TOKEN_LPAREN)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarBindTerm{Position: term.Position, Name: name, Term: term}
}
func (p *Parser) parseGrammarChoiceTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("choice")
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		options := make([]ast.GrammarTerm, 0, 4)
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			options = append(options, p.parseGrammarTerm())
		}
		p.expect(lexer.TOKEN_DEDENT)
		return &ast.GrammarChoiceTerm{Position: pos, Options: options}
	}
	p.expect(lexer.TOKEN_LPAREN)
	options := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			options = append(options, p.parseGrammarRecoverableTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarChoiceTerm{Position: pos, Options: options}
}
func (p *Parser) parseGrammarWhenTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("when")
	p.expect(lexer.TOKEN_LPAREN)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	thenTerm := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	elseTerm := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarWhenTerm{Position: pos, Cond: cond, Then: thenTerm, Else: elseTerm}
}
func (p *Parser) parseGrammarMatchTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	value := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	arms := make([]ast.GrammarMatchArm, 0, 4)
	hasWildcard := false
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arm := p.parseGrammarMatchArm()
		for _, pattern := range arm.Patterns {
			if _, ok := pattern.(*ast.MatchWildcardPattern); ok {
				hasWildcard = true
			}
			p.validateGrammarDispatchPattern(pattern)
		}
		arms = append(arms, arm)
	}
	p.expect(lexer.TOKEN_DEDENT)
	if !hasWildcard {
		p.errorAt(pos, "grammar match term must include a wildcard '_' arm")
	}
	return &ast.GrammarMatchTerm{Position: pos, Value: value, Arms: arms}
}
func (p *Parser) parseGrammarMatchArm() ast.GrammarMatchArm {
	pos := p.cur().Pos
	patterns := p.parseGrammarDispatchPatterns()
	p.expect(lexer.TOKEN_COLON)
	term := p.parseGrammarRecoverableTermValue()
	p.expectNewline()
	return ast.GrammarMatchArm{Position: pos, Patterns: patterns, Term: term}
}
func (p *Parser) parseGrammarDispatchPatterns() []ast.MatchPattern {
	patterns := []ast.MatchPattern{p.parseNestedMatchPattern()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		patterns = append(patterns, p.parseNestedMatchPattern())
	}
	return patterns
}
func (p *Parser) validateGrammarDispatchPattern(pattern ast.MatchPattern) {
	switch n := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchVariantPattern:
		if len(n.Args) == 0 {
			return
		}
	}
	p.errorAt(pattern.Pos(), "grammar match arms support simple value patterns only")
}
func (p *Parser) parseGrammarRequiredTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("required")
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarRequiredTerm{Position: pos, Term: term, Message: message}
}
func (p *Parser) parseGrammarDelimitedTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("delimited")
	p.expect(lexer.TOKEN_LPAREN)
	open := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	body := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	close := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarDelimitedTerm{Position: pos, Open: open, Body: body, Close: close, Message: message}
}
func (p *Parser) parseGrammarSeqTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("seq")
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		return &ast.GrammarSeqTerm{Position: pos, Terms: p.parseGrammarTermBlock()}
	}
	p.expect(lexer.TOKEN_LPAREN)
	terms := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
			terms = append(terms, p.parseGrammarNestedTerm())
			if p.match(lexer.TOKEN_COMMA) {
				continue
			}
			if p.peek() == lexer.TOKEN_RPAREN || p.peek() == lexer.TOKEN_EOF {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarSeqTerm{Position: pos, Terms: terms}
}
func (p *Parser) parseGrammarPrefixTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("prefix")
	p.expect(lexer.TOKEN_LPAREN)
	ops := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			ops = append(ops, p.parseGrammarRecoverableTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	operand := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_ARROW)
	value := p.parseExpr()
	var opTerm ast.GrammarTerm
	if len(ops) == 1 {
		opTerm = ops[0]
	} else {
		opTerm = &ast.GrammarChoiceTerm{Position: pos, Options: ops}
	}
	return &ast.GrammarSeqTerm{Position: pos, Terms: []ast.GrammarTerm{
		&ast.GrammarBindTerm{Position: pos, Name: "op", Term: opTerm},
		&ast.GrammarBindTerm{Position: operand.Pos(), Name: "operand", Term: operand},
		&ast.GrammarExprTerm{Position: value.Pos(), Expr: value},
	}}
}
func (p *Parser) parseGrammarLookaheadTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("lookahead")
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarUntilStopTerm()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarLookaheadTerm{Position: pos, Term: term}
}
func (p *Parser) parseGrammarExprTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("expr")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarExprTerm{Position: pos, Type: typ, Expr: expr}
}
func (p *Parser) parseGrammarSingletonTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("singleton")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	value := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarSingletonTerm{Position: pos, Type: typ, Value: value}
}
func (p *Parser) parseGrammarEmptyTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("empty")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	return &ast.GrammarEmptyTerm{Position: pos, Type: typ}
}
func (p *Parser) parseGrammarNoneTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("none")
	p.expect(lexer.TOKEN_LBRACKET)
	typ := p.parseTypeExpr()
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.GrammarExprTerm{Position: pos, Type: &ast.OptionalTypeExpr{Position: pos, Value: typ}, Expr: &ast.NullLit{Position: pos}}
}
func (p *Parser) parseGrammarGuardTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("guard")
	p.expect(lexer.TOKEN_LPAREN)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarGuardTerm{Position: pos, Cond: cond}
}
func (p *Parser) parseGrammarAttemptTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("attempt")
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarAttemptTerm{Position: pos, Expr: expr}
}
func (p *Parser) parseGrammarCutTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("cut")
	return &ast.GrammarCutTerm{Position: pos}
}
func (p *Parser) parseGrammarOptionalTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("optional")
	if p.peek() != lexer.TOKEN_LPAREN {
		term := p.parseGrammarRecoverableTermValue()
		return &ast.GrammarOptionalTerm{Position: pos, Term: term}
	}
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarOptionalTerm{Position: pos, Term: term}
}
// parseGrammarListTerm parses the REMOVED `list(...)` spelling, emits a hard error,
// and recovers into the canonical repetition IR (a separated term when a separator
// is given, otherwise a plain repetition) so downstream passes stay well-formed.
func (p *Parser) parseGrammarListTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("list")
	p.errorAt(pos, "the `list(...)` grammar spelling has been removed; use the postfix repetition `term* until(stop)`, or `separated term by sep until(stop)`")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var separator ast.GrammarTerm
		var until []ast.GrammarTerm
		if p.peekIdentText("separated") {
			p.expectIdentText("separated")
			p.expectIdentText("by")
			separator = p.parseGrammarRecoverableTermValue()
		}
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return recoveredRepetitionTerm(pos, elem, separator, until)
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var separator ast.GrammarTerm
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		} else {
			separator = p.parseGrammarRecoverableTermValue()
			if p.match(lexer.TOKEN_COMMA) {
				until = p.parseGrammarUntilClause()
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return recoveredRepetitionTerm(pos, elem, separator, until)
}

// recoveredRepetitionTerm builds the canonical repetition node for legacy-spelling
// error recovery: a separated term when a separator is present, otherwise a plain
// repetition.
func recoveredRepetitionTerm(pos lexer.Pos, elem, separator ast.GrammarTerm, until []ast.GrammarTerm) ast.GrammarTerm {
	if separator != nil {
		return &ast.GrammarSeparatedTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
	}
	return &ast.GrammarRepeatTerm{Position: pos, Elem: elem, Until: until}
}

// parseGrammarRepeatTerm parses the REMOVED `repeat ...` spelling, emits a hard
// error, and recovers into the canonical `term*` repetition node.
func (p *Parser) parseGrammarRepeatTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("repeat")
	p.errorAt(pos, "the `repeat ...` grammar spelling has been removed; use the postfix repetition `term* until(stop)` (or `term+` for one-or-more)")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var until []ast.GrammarTerm
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarRepeatTerm{Position: pos, Elem: elem, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		until = p.parseGrammarUntilClause()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarRepeatTerm{Position: pos, Elem: elem, Until: until}
}
func (p *Parser) parseGrammarFlatRepeatTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("flatrepeat")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var until []ast.GrammarTerm
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarFlatRepeatTerm{Position: pos, Elem: elem, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		until = p.parseGrammarUntilClause()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarFlatRepeatTerm{Position: pos, Elem: elem, Until: until}
}
func (p *Parser) isGrammarWhileTermStart() bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for idx := p.pos + 1; idx < len(p.tokens); idx++ {
		tok := p.tokens[idx]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth == 0 {
				if tok.Kind != lexer.TOKEN_RBRACKET || idx+6 >= len(p.tokens) {
					return false
				}
				return p.tokens[idx+1].Kind == lexer.TOKEN_WHILE &&
					p.tokens[idx+2].Kind == lexer.TOKEN_IDENT &&
					p.tokens[idx+3].Kind == lexer.TOKEN_IN &&
					p.tokens[idx+4].Kind == lexer.TOKEN_IDENT && p.tokens[idx+4].Text == "tokens" &&
					p.tokens[idx+5].Kind == lexer.TOKEN_BANGEQ &&
					p.tokens[idx+6].Kind == lexer.TOKEN_LBRACKET
			}
			depth--
		}
	}
	return false
}
// parseGrammarWhileTerm parses the REMOVED `[term] while tok in tokens != [...]`
// spelling, emits a hard error, and recovers into the canonical `flatrepeat term
// until(stop)` node so the legacy GrammarWhileTerm is never produced.
func (p *Parser) parseGrammarWhileTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.errorAt(pos, "the `[term] while tok in tokens != [...]` grammar spelling has been removed; use `flatrepeat term until(stop)`")
	p.expect(lexer.TOKEN_LBRACKET)
	elem := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RBRACKET)
	p.expect(lexer.TOKEN_WHILE)
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_IN)
	p.expectIdentText("tokens")
	p.expect(lexer.TOKEN_BANGEQ)
	p.expect(lexer.TOKEN_LBRACKET)
	until := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	if p.peek() != lexer.TOKEN_RBRACKET {
		for {
			until = append(until, p.parseGrammarUntilStopTerm())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.GrammarFlatRepeatTerm{Position: pos, Elem: elem, Until: until}
}
