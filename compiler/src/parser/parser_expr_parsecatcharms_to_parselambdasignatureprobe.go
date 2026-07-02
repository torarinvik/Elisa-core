package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseCatchArms() (ast.CatchArm, []ast.CatchArm) {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	p.skipNewlines()
	if p.peek() == lexer.TOKEN_DEDENT || p.peek() == lexer.TOKEN_EOF {
		p.errorf("catch expression requires a success arm")
		p.expect(lexer.TOKEN_DEDENT)
		return ast.CatchArm{}, nil
	}
	success := p.parseCatchArm()
	if success.ErrorBinding {
		p.errorf("catch expression must start with a success arm")
	}
	var arms []ast.CatchArm
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseCatchArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return success, arms
}
func (p *Parser) parseCatchArm() ast.CatchArm {
	if p.peek() == lexer.TOKEN_ERROR && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		p.advance()
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		body := p.parseCatchArmBody(pos)
		return ast.CatchArm{Position: pos, Name: name, ErrorBinding: true, Body: body}
	}
	name, pos := p.parseQualifiedTargetName()
	var payload []string
	if p.match(lexer.TOKEN_LPAREN) {
		for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
			payload = append(payload, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_COLON)
	body := p.parseCatchArmBody(pos)
	return ast.CatchArm{Position: pos, Name: name, Payload: payload, Body: body}
}
func (p *Parser) parseCatchArmBody(pos lexer.Pos) []ast.Stmt {
	if p.match(lexer.TOKEN_NEWLINE) {
		return p.parseBlock()
	}
	value := p.parseExpr()
	return []ast.Stmt{&ast.ExprStmt{Position: pos, Expr: value}}
}
func (p *Parser) parseRewriteExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("rewrite")
	p.errorAt(pos, "`rewrite … as sequence[T]:` has been removed; build the result explicitly with a `darray[T] = []` and a `for` loop that `.push(…)`es each element (`emit all` → an inner push loop)")
	value := p.parseExpr()
	p.expect(lexer.TOKEN_AS)
	root := p.parseTypeExpr()
	rewriteDefault := p.matchIdentText("default")
	arms := p.parseVisitArms()
	return &ast.FoldExpr{Position: pos, Keyword: "rewrite", Value: value, Root: root, ResultType: root, RewriteDefault: rewriteDefault, Arms: arms}
}
func (p *Parser) parseExprAfterRemovedPrefixKeyword() ast.Expr {
	// Recovery for removed prefix-keyword expressions (docs/81): consume the keyword and
	// parse the operand so one clear error is reported instead of a cascade.
	p.advance()
	return p.parseExpr()
}
// isLambdaKeyword reports whether text introduces a lambda expression: the canonical
// `fn`, a Unicode lambda letter, or the removed `lambda` spelling (still recognized so
// parseLambdaExpr can emit a directed migration error). The Unicode set is the BMP lambda
// letters (Greek λ/Λ and the Latin lambda-with-stroke ƛ) — the ones actually typed. The
// supplementary-plane Mathematical-Alphanumeric lambda variants are intentionally NOT
// included: they are never keyed by hand, and stage1's lexer does not yet span a 4-byte
// identifier rune correctly, so accepting them would make stage0 and stage1 disagree.
func isLambdaKeyword(text string) bool {
	switch text {
	case "fn", "lambda",
		"λ", // U+03BB GREEK SMALL LETTER LAMBDA
		"Λ", // U+039B GREEK CAPITAL LETTER LAMBDA
		"ƛ": // U+019B LATIN SMALL LETTER LAMBDA WITH STROKE
		return true
	}
	return false
}

func (p *Parser) parseLambdaExpr() ast.Expr {
	pos := p.cur().Pos
	keyword := p.expect(lexer.TOKEN_IDENT).Text
	if keyword == "lambda" {
		// Directed migration: the `lambda` spelling has been removed in favor of `fn`
		// (or a Unicode lambda letter). Recover as canonical — parse the lambda anyway,
		// so a single clear error is reported rather than a cascade.
		p.errorAt(pos, "the `lambda` keyword has been removed; use `fn` (or a Unicode lambda letter `λ`)")
	}
	params, shorthand := p.parseLambdaParams()
	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	if p.match(lexer.TOKEN_FATARROW) {
		bodyExpr := p.parseExpr()
		return &ast.LambdaExpr{
			Position:            pos,
			Keyword:             keyword,
			UsesShorthandParams: shorthand,
			Params:              params,
			ReturnType:          retType,
			BodyExpr:            bodyExpr,
		}
	}
	p.expect(lexer.TOKEN_COLON)
	if p.match(lexer.TOKEN_NEWLINE) {
		body := p.parseBlock()
		return &ast.LambdaExpr{
			Position:            pos,
			Keyword:             keyword,
			UsesShorthandParams: shorthand,
			Params:              params,
			ReturnType:          retType,
			Body:                body,
		}
	}
	bodyExpr := p.parseExpr()
	return &ast.LambdaExpr{
		Position:            pos,
		Keyword:             keyword,
		UsesShorthandParams: shorthand,
		Params:              params,
		ReturnType:          retType,
		BodyExpr:            bodyExpr,
	}
}
func (p *Parser) parseLambdaParams() ([]ast.ParamDecl, bool) {
	if p.match(lexer.TOKEN_LPAREN) {
		params := make([]ast.ParamDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				params = append(params, p.parseLambdaParam())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
		return params, false
	}
	params := []ast.ParamDecl{{Position: p.cur().Pos, Name: p.expect(lexer.TOKEN_IDENT).Text}}
	for p.match(lexer.TOKEN_COMMA) {
		params = append(params, ast.ParamDecl{Position: p.cur().Pos, Name: p.expect(lexer.TOKEN_IDENT).Text})
	}
	return params, true
}

// parseLambdaParam parses one parenthesized lambda parameter. Unlike a
// declaration parameter, the type annotation is OPTIONAL: `lambda(n) => ...`
// leaves the param untyped so its type is inferred from the expected callback
// signature at the call site (the same inference that drives the bare-shorthand
// `lambda n => ...` form). `lambda(n: i64) => ...` keeps the explicit type.
func (p *Parser) parseLambdaParam() ast.ParamDecl {
	pos := p.cur().Pos
	mutable := p.match(lexer.TOKEN_MUTABLE)
	name := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COLON) {
		return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable}
	}
	typ := p.parseTypeExpr()
	return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable, Type: typ}
}
func (p *Parser) parseQualifiedTargetName() (string, lexer.Pos) {
	pos := p.cur().Pos
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.matchQualifiedNameSeparator() {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	return strings.Join(parts, "."), pos
}
func (p *Parser) parseVisitArms() []ast.VisitArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.VisitArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseVisitArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}
func (p *Parser) parseVisitArm() []ast.VisitArm {
	arms := []ast.VisitArm{p.parseVisitArmHead()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		arms = append(arms, p.parseVisitArmHead())
	}
	var guard ast.Expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "when" {
		p.advance()
		guard = p.parseExpr()
	}
	for i := range arms {
		arms[i].Guard = guard
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	for i := range arms {
		arms[i].Body = body
	}
	return arms
}
func (p *Parser) parseVisitArmHead() ast.VisitArm {
	pos := p.cur().Pos
	arm := ast.VisitArm{Position: pos}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		arm.Wildcard = true
	} else {
		arm.TargetName, _ = p.parseQualifiedTargetName()
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				arm.BindName = p.expect(lexer.TOKEN_IDENT).Text
				if p.match(lexer.TOKEN_COMMA) {
					arm = p.parseVisitArmChildBindings(arm)
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
	}
	return arm
}
func (p *Parser) parseVisitArmChildBindings(arm ast.VisitArm) ast.VisitArm {
	if p.peek() != lexer.TOKEN_IDENT {
		p.errorf("expected fold child result binding name, got %s", p.cur())
		return arm
	}
	firstTok := p.expect(lexer.TOKEN_IDENT)
	if p.peek() != lexer.TOKEN_COLON && p.peek() == lexer.TOKEN_RPAREN {
		arm.ChildResultsName = firstTok.Text
		return arm
	}
	arm.ChildBindings = append(arm.ChildBindings, p.finishVisitArmChildBinding(firstTok))
	for p.match(lexer.TOKEN_COMMA) {
		nameTok := p.expect(lexer.TOKEN_IDENT)
		arm.ChildBindings = append(arm.ChildBindings, p.finishVisitArmChildBinding(nameTok))
	}
	return arm
}
func (p *Parser) finishVisitArmChildBinding(nameTok lexer.Token) ast.VisitArmChildBinding {
	binding := ast.VisitArmChildBinding{Position: nameTok.Pos, FieldName: nameTok.Text, BindName: nameTok.Text}
	if p.match(lexer.TOKEN_COLON) {
		binding.BindName = p.expect(lexer.TOKEN_IDENT).Text
	}
	return binding
}
func (p *Parser) parseCallArgs() ([]ast.Expr, []string, []bool, []ast.CallArgItem, bool, lexer.Pos) {
	argCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN)
	args := make([]ast.Expr, 0, argCapacity)
	argNames := make([]string, 0, argCapacity)
	argShorthand := make([]bool, 0, argCapacity)
	items := make([]ast.CallArgItem, 0, argCapacity)
	hasArgForward := false
	argForwardPos := lexer.Pos{}
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil, nil, nil, false, lexer.Pos{}
	}
	for {
		if p.peek() == lexer.TOKEN_RANGE {
			pos := p.cur().Pos
			p.advance()
			if hasArgForward {
				p.errorf("call argument forwarding `..` can only appear once")
			}
			if len(args) != 0 {
				p.errorf("call argument forwarding `..` must appear before other call arguments")
			}
			hasArgForward, argForwardPos = true, pos
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RPAREN {
				break
			}
			continue
		}
		name := ""
		namePos := lexer.Pos{}
		shorthand := false
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON && !p.peekImmediateGroupedBlockExprStart() {
			tok := p.advance()
			name = tok.Text
			namePos = tok.Pos
			p.expect(lexer.TOKEN_COLON)
			if p.peek() == lexer.TOKEN_COMMA || p.peek() == lexer.TOKEN_RPAREN {
				p.errorf("shorthand named argument `%s:` has been removed; write `%s: %s`", name, name, name)
				shorthand = true // recover: treat as the punned reference so the rest of the arg list still parses
			}
		}
		var arg ast.Expr
		if shorthand {
			arg = &ast.Ident{Position: namePos, Name: name}
		} else {
			arg = p.parseExpr()
		}
		args = append(args, arg)
		argNames = append(argNames, name)
		argShorthand = append(argShorthand, shorthand)
		items = append(items, ast.CallArgItem{Position: arg.Pos(), ArgIndex: len(args) - 1})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	hasNamed := false
	for _, name := range argNames {
		if name != "" {
			hasNamed = true
			break
		}
	}
	if hasArgForward {
		for _, name := range argNames {
			if name == "" {
				p.errorf("call argument forwarding `..` only supports named arguments")
				break
			}
		}
	}
	if !hasNamed {
		return args, nil, nil, nil, hasArgForward, argForwardPos
	}
	return args, argNames, argShorthand, items, hasArgForward, argForwardPos
}
func (p *Parser) peekImmediateGroupedBlockExprStart() bool {
	return p.peek() == lexer.TOKEN_IDENT &&
		p.isImmediateGroupedBlockExprName(p.cur().Text) &&
		p.pos+2 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON &&
		p.tokens[p.pos+2].Kind == lexer.TOKEN_NEWLINE
}
func (p *Parser) isImmediateGroupedBlockExprName(name string) bool {
	switch name {
	case "do":
		return true
	default:
		return false
	}
}
func (p *Parser) peekPostfixGenericValueApplication() bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	if probe.peek() == lexer.TOKEN_RBRACKET {
		return false
	}
	count := 0
	for {
		_ = probe.parseGenericTypeArgExpr()
		count++
		if !probe.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	if probe.peek() != lexer.TOKEN_RBRACKET || count < 2 {
		return false
	}
	probe.advance()
	// A trailing `(` is the call form handled by peekPostfixGenericApplication; not a value.
	return probe.peek() != lexer.TOKEN_LPAREN
}

func (p *Parser) peekPostfixGenericApplication() bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	if probe.peek() == lexer.TOKEN_RBRACKET {
		return false
	}
	for {
		_ = probe.parseGenericTypeArgExpr()
		if !probe.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	if probe.peek() != lexer.TOKEN_RBRACKET {
		return false
	}
	probe.advance()
	return probe.peek() == lexer.TOKEN_LPAREN
}
func (p *Parser) looksLikeCascadeExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "cascade" {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	_ = probe.parseExpr()
	return probe.peek() == lexer.TOKEN_FATARROW
}
func (p *Parser) looksLikeLambdaExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || !isLambdaKeyword(p.cur().Text) {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	if !probe.parseLambdaSignatureProbe() {
		return false
	}
	return probe.peek() == lexer.TOKEN_COLON || probe.peek() == lexer.TOKEN_FATARROW
}
func (p *Parser) parseLambdaSignatureProbe() bool {
	if p.peek() == lexer.TOKEN_LPAREN {
		p.advance()
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				p.match(lexer.TOKEN_MUTABLE)
				if p.peek() != lexer.TOKEN_IDENT {
					return false
				}
				p.advance()
				// The type annotation is optional: `lambda(n) => ...` infers the
				// param type from context. Only consume a type when a colon follows.
				if p.match(lexer.TOKEN_COLON) {
					_ = p.parseTypeExpr()
				}
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		if !p.match(lexer.TOKEN_RPAREN) {
			return false
		}
	} else if p.peek() == lexer.TOKEN_IDENT {
		p.advance()
		for p.match(lexer.TOKEN_COMMA) {
			if p.peek() != lexer.TOKEN_IDENT {
				return false
			}
			p.advance()
		}
	} else {
		return false
	}
	if p.match(lexer.TOKEN_ARROW) {
		_ = p.parseTypeExpr()
	}
	return true
}
