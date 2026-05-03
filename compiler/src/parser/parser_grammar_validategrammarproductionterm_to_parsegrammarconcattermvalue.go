package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (p *Parser) validateGrammarProductionTerm(productionName string, term ast.GrammarTerm) {
	switch n := term.(type) {
	case *ast.GrammarBindTerm:
		p.validateGrammarProductionTerm(productionName, n.Term)
	case *ast.GrammarAssignTerm:
		p.validateGrammarProductionTerm(productionName, n.Term)
	case *ast.GrammarReturnTerm:
		if n.Term != nil {
			p.validateGrammarProductionTerm(productionName, n.Term)
		}
	case *ast.GrammarPassTerm:
		// valid
	case *ast.GrammarSeqTerm:
		for _, seqTerm := range n.Terms {
			p.validateGrammarProductionTerm(productionName, seqTerm)
		}
	case *ast.GrammarChoiceTerm:
		for _, opt := range n.Options {
			p.validateGrammarProductionTerm(productionName, opt)
		}
	case *ast.GrammarOptionalTerm, *ast.GrammarRequiredTerm, *ast.GrammarDelimitedTerm,
		*ast.GrammarLookaheadTerm, *ast.GrammarGuardTerm, *ast.GrammarAttemptTerm,
		*ast.GrammarWhenTerm, *ast.GrammarMatchTerm, *ast.GrammarRecoverTerm:
		// valid
	case *ast.GrammarListTerm, *ast.GrammarRepeatTerm, *ast.GrammarFlatRepeatTerm, *ast.GrammarWhileTerm, *ast.GrammarSeparatedTerm:
		// valid
	case *ast.GrammarSuffixTerm, *ast.GrammarPostfixTerm, *ast.GrammarPrecedenceTerm:
		// valid
	case *ast.GrammarExprTerm, *ast.GrammarSingletonTerm, *ast.GrammarEmptyTerm,
		*ast.GrammarConcatTerm, *ast.GrammarCutTerm:
		// valid
	case *ast.GrammarCallTerm, *ast.GrammarApplyTerm, *ast.GrammarTokenTerm, *ast.GrammarTokenKindTerm,
		*ast.GrammarTokenSetRefTerm, *ast.GrammarFirstTerm, *ast.GrammarInfixTableTerm:
		// valid
	default:
		pos := term.Pos()
		p.errorAt(pos, "grammar production %q contains an unsupported construct at %s; expected grammar terms such as token matches, bindings, choices, sequences, or helper calls", productionName, pos)
	}
}
func (p *Parser) parseGrammarRecoveryDecl() ast.GrammarRecoveryDecl {
	pos := p.cur().Pos
	p.expectIdentText("recovery")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	decl := ast.GrammarRecoveryDecl{Position: pos, Name: name}
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("message"):
			p.expectIdentText("message")
			decl.Message = p.parseExpr()
			p.expectNewline()
		case p.peekIdentText("until"):
			decl.Until = p.parseGrammarRecoveryUntilDecl()
		case p.peekIdentText("fallback"):
			p.expectIdentText("fallback")
			decl.Fallback = p.parseExpr()
			p.expectNewline()
		default:
			p.errorf("expected grammar recovery declaration item")
			p.advance()
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	if decl.Message == nil {
		p.errorf("expected recovery declaration message")
	}
	if len(decl.Until) == 0 {
		p.errorf("expected recovery declaration until clause")
	}
	return decl
}
func (p *Parser) parseGrammarInfixTableDecl() ast.GrammarInfixTableDecl {
	pos := p.cur().Pos
	p.expectIdentText("infix")
	p.expectIdentText("table")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	result := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	levels := make([]ast.GrammarPrecedenceLevel, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		levels = append(levels, p.parseGrammarPrecedenceLevel())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.GrammarInfixTableDecl{Position: pos, Name: name, Result: result, Levels: levels}
}
func (p *Parser) parseGrammarRecoveryUntilDecl() []ast.GrammarTerm {
	p.expectIdentText("until")
	if p.peek() == lexer.TOKEN_LPAREN {
		terms := p.parseGrammarUntilClauseBody()
		p.expectNewline()
		return terms
	}
	terms := make([]ast.GrammarTerm, 0, 4)
	for {
		terms = append(terms, p.parseGrammarUntilStopTerm())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expectNewline()
	return terms
}
func (p *Parser) parseGrammarProductionDecls() []ast.GrammarProductionDecl {
	pos := p.cur().Pos
	public := p.peekIdentText("pub") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
	if public {
		p.expectIdentText("pub")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_PLUSEQ) {
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			groupedTerms := p.parseGroupedGrammarAppendTermBlocks()
			productions := make([]ast.GrammarProductionDecl, 0, len(groupedTerms))
			for _, terms := range groupedTerms {
				productions = append(productions, ast.GrammarProductionDecl{Position: pos, Public: public, Append: true, Name: name, Terms: terms})
			}
			return productions
		}
		p.expectNewline()
		terms := p.parseGrammarTermBlock()
		return []ast.GrammarProductionDecl{{Position: pos, Public: public, Append: true, Name: name, Terms: terms}}
	}
	hasParamList := p.match(lexer.TOKEN_LPAREN)
	params := make([]ast.ParamDecl, 0)
	if hasParamList {
		params = p.parseParamList(false)
		p.expect(lexer.TOKEN_RPAREN)
	}

	var retType ast.TypeExpr
	var recoverPolicy string
	var recoverMsg ast.Expr
	var recoverUntil []ast.GrammarTerm
	var recoverValue ast.Expr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	if p.peekIdentText("recover") {
		recoverPolicy, recoverMsg, recoverUntil, recoverValue = p.parseGrammarRecoverSpec()
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	channels, terms := p.parseGrammarProductionBody()
	return []ast.GrammarProductionDecl{{Position: pos, Public: public, Name: name, HasParamList: hasParamList, Params: params, ReturnType: retType, RecoverPolicy: recoverPolicy, RecoverMsg: recoverMsg, RecoverUntil: recoverUntil, RecoverValue: recoverValue, Channels: channels, Terms: terms}}
}
func (p *Parser) parseGrammarProductionBody() ([]ast.GrammarChannelDecl, []ast.GrammarTerm) {
	p.expect(lexer.TOKEN_INDENT)
	channels := make([]ast.GrammarChannelDecl, 0)
	terms := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peekIdentText("channel") {
			channels = append(channels, p.parseGrammarChannelDecl())
			continue
		}
		term := p.parseGrammarTerm()
		if term != nil {
			terms = append(terms, term)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return channels, terms
}
func (p *Parser) parseGroupedGrammarAppendTermBlocks() [][]ast.GrammarTerm {
	p.expect(lexer.TOKEN_INDENT)
	groups := make([][]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	current := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	lastWasSeparator := false
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		for p.peek() == lexer.TOKEN_NEWLINE {
			p.advance()
		}
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_PIPE {
			if len(current) == 0 {
				p.errorf("expected grouped append arm before '|' separator")
			}
			p.advance()
			p.expectNewline()
			if len(current) != 0 {
				groups = append(groups, current)
				current = make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
			}
			lastWasSeparator = true
			continue
		}
		term := p.parseGrammarTerm()
		if term != nil {
			current = append(current, term)
			lastWasSeparator = false
		}
	}
	if len(current) != 0 {
		groups = append(groups, current)
	} else if lastWasSeparator {
		p.errorf("expected grouped append arm after '|' separator")
	}
	p.expect(lexer.TOKEN_DEDENT)
	return groups
}
func (p *Parser) parseGrammarRecoverSpec() (string, ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expectIdentText("recover")
	if p.peek() != lexer.TOKEN_LPAREN {
		return p.expect(lexer.TOKEN_IDENT).Text, nil, nil, nil
	}
	message, until, fallback := p.parseGrammarRecoverClauseBody()
	return "", message, until, fallback
}
func (p *Parser) parseGrammarRecoverClause() (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expectIdentText("recover")
	return p.parseGrammarRecoverClauseBody()
}
func (p *Parser) parseGrammarRecoverClauseBody() (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expect(lexer.TOKEN_LPAREN)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	until := p.parseGrammarUntilClause()
	var fallback ast.Expr
	if p.match(lexer.TOKEN_COMMA) {
		fallback = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return message, until, fallback
}
func (p *Parser) wrapGrammarRecoverTerm(term ast.GrammarTerm) ast.GrammarTerm {
	if term == nil || !p.peekIdentText("recover") {
		return term
	}
	policy, message, until, fallback := p.parseGrammarRecoverSpec()
	return &ast.GrammarRecoverTerm{
		Position:      term.Pos(),
		Term:          term,
		RecoverPolicy: policy,
		RecoverMsg:    message,
		RecoverUntil:  until,
		RecoverValue:  fallback,
	}
}
func (p *Parser) parseGrammarTermBlock() []ast.GrammarTerm {
	p.expect(lexer.TOKEN_INDENT)
	terms := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		term := p.parseGrammarTerm()
		if term != nil {
			terms = append(terms, term)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return terms
}
func (p *Parser) parseGrammarTerm() ast.GrammarTerm {
	if p.peek() == lexer.TOKEN_PASS {
		pos := p.cur().Pos
		p.advance()
		p.expectNewline()
		return &ast.GrammarPassTerm{Position: pos}
	}
	if p.peek() == lexer.TOKEN_RETURN {
		return p.parseGrammarReturnTerm()
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
	if p.peekGrammarTokenKindBinding() {
		binding := p.parseGrammarTokenKindBinding()
		p.expectNewline()
		return binding
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LARROW {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LARROW)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		if grammarTermNeedsTrailingNewline(term, p.peek()) {
			p.expectNewline()
		}
		return &ast.GrammarAssignTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		if grammarTermNeedsTrailingNewline(term, p.peek()) {
			p.expectNewline()
		}
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_MUTABLE || p.peek() == lexer.TOKEN_IF || p.peek() == lexer.TOKEN_WHILE || p.peek() == lexer.TOKEN_MATCH {
		pos := p.cur().Pos
		keyword := p.cur().Text
		p.errorAt(pos, "grammar production body cannot contain general statements (found %q); grammar terms should use token matches, bindings, choices, or helper calls, not general control flow or local variable declarations", keyword)
		p.advance()
		return nil
	}
	term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
	if grammarTermNeedsTrailingNewline(term, p.peek()) {
		p.expectNewline()
	}
	return term
}
func (p *Parser) parseGrammarReturnTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.advance()
	term := p.parseGrammarReturnValueTerm()
	if grammarTermNeedsTrailingNewline(term, p.peek()) {
		p.expectNewline()
	}
	return &ast.GrammarReturnTerm{Position: pos, Term: term}
}
func (p *Parser) parseGrammarReturnValueTerm() ast.GrammarTerm {
	if p.peekIdentText("seq") {
		return p.parseGrammarSeqTerm()
	}
	expr := p.parseExpr()
	return &ast.GrammarExprTerm{Position: expr.Pos(), Expr: expr}
}
func grammarTermNeedsTrailingNewline(term ast.GrammarTerm, next lexer.TokenKind) bool {
	switch term.(type) {
	case *ast.GrammarPrecedenceTerm, *ast.GrammarPostfixTerm, *ast.GrammarSuffixTerm, *ast.GrammarSeqTerm, *ast.GrammarMatchTerm:
		return false
	case *ast.GrammarChoiceTerm:
		return next == lexer.TOKEN_NEWLINE
	default:
		return true
	}
}
func (p *Parser) parseGrammarNestedTerm() ast.GrammarTerm {
	if p.peek() == lexer.TOKEN_PASS {
		pos := p.cur().Pos
		p.advance()
		return &ast.GrammarPassTerm{Position: pos}
	}
	if p.peekGrammarTokenKindBinding() {
		return p.parseGrammarTokenKindBinding()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LARROW {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LARROW)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		return &ast.GrammarAssignTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	return p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
}
func (p *Parser) parseGrammarRecoverableTermValue() ast.GrammarTerm {
	return p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
}
func (p *Parser) peekGrammarBlockTerm(name string) bool {
	return p.peekIdentText(name) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN
}
func (p *Parser) peekGrammarPrecedenceTerm() bool {
	if !p.peekIdentText("precedence") || p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	if next.Kind == lexer.TOKEN_LPAREN {
		return true
	}
	if next.Kind != lexer.TOKEN_IDENT {
		return false
	}
	if next.Text != ast.GrammarAssociativityLeft && next.Text != ast.GrammarAssociativityRight && next.Text != ast.GrammarAssociativityNonAssoc {
		return false
	}
	return p.pos+2 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN
}
func (p *Parser) parseGrammarTermValue() ast.GrammarTerm {
	term := p.parseGrammarChoiceTermValue()
	for {
		switch {
		case p.match(lexer.TOKEN_QUESTION):
			if tokenKind, ok := term.(*ast.GrammarTokenKindTerm); ok && p.peekIdentText("then") {
				return p.parseGrammarOptionalTokenGateTerm(tokenKind)
			}
			term = &ast.GrammarOptionalTerm{Position: term.Pos(), Term: term}
		case p.match(lexer.TOKEN_STAR):
			var until []ast.GrammarTerm
			if p.peekIdentText("until") {
				until = p.parseGrammarUntilClause()
			}
			term = &ast.GrammarRepeatTerm{Position: term.Pos(), Elem: term, Until: until}
		default:
			return term
		}
	}
}
func (p *Parser) parseGrammarOptionalTokenGateTerm(tokenKind *ast.GrammarTokenKindTerm) ast.GrammarTerm {
	p.expectIdentText("then")
	thenTerm := p.parseGrammarRecoverableTermValue()
	elseTerm := ast.GrammarTerm(&ast.GrammarExprTerm{Position: tokenKind.Position, Expr: &ast.NullLit{Position: tokenKind.Position}})
	if p.match(lexer.TOKEN_ELSE) {
		elseTerm = p.parseGrammarTokenGateElseTerm()
	}
	return &ast.GrammarWhenTerm{Position: tokenKind.Position, TokenKindGate: tokenKind.Kind, Then: thenTerm, Else: elseTerm}
}
func (p *Parser) parseGrammarTokenGateElseTerm() ast.GrammarTerm {
	term := p.parseGrammarRecoverableTermValue()
	if exprTerm, ok := term.(*ast.GrammarExprTerm); ok && exprTerm != nil {
		if list, ok := exprTerm.Expr.(*ast.ListLitExpr); ok && list != nil && len(list.Elems) == 0 {
			return &ast.GrammarEmptyTerm{Position: list.Position}
		}
	}
	return term
}
func (p *Parser) parseGrammarChoiceTermValue() ast.GrammarTerm {
	term := p.parseGrammarPipelineTermValue()
	if p.peek() == lexer.TOKEN_PIPE {
		options := []ast.GrammarTerm{term}
		for p.match(lexer.TOKEN_PIPE) {
			options = append(options, p.parseGrammarPipelineTermValue())
		}
		term = &ast.GrammarChoiceTerm{Position: term.Pos(), Options: options}
	}
	return term
}
func (p *Parser) parseGrammarPipelineTermValue() ast.GrammarTerm {
	term := p.parseGrammarConcatTermValue()
	for p.peek() == lexer.TOKEN_PIPEGT {
		term = p.parseGrammarPipelineStep(term)
	}
	return term
}
func (p *Parser) parseGrammarPipelineStep(input ast.GrammarTerm) ast.GrammarTerm {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PIPEGT)
	name := p.parseQualifiedDeclName()
	if !p.match(lexer.TOKEN_LPAREN) {
		p.errorAt(pos, "expected '(' after grammar pipeline target")
		return input
	}
	args := []ast.GrammarApplyArg{{Position: input.Pos(), Term: input}}
	args = append(args, p.parseGrammarApplyArgsUntilRParen()...)
	return &ast.GrammarApplyTerm{Position: pos, Name: name, Direct: true, Piped: true, Args: args}
}
func (p *Parser) parseGrammarConcatTermValue() ast.GrammarTerm {
	term := p.parseGrammarAtomicTermValue()
	if !p.match(lexer.TOKEN_PLUS) {
		return term
	}
	terms := []ast.GrammarTerm{term, p.parseGrammarAtomicTermValue()}
	for p.match(lexer.TOKEN_PLUS) {
		terms = append(terms, p.parseGrammarAtomicTermValue())
	}
	return &ast.GrammarConcatTerm{Position: term.Pos(), Terms: terms}
}
