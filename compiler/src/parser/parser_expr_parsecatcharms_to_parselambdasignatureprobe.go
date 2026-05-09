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
	p.expect(lexer.TOKEN_COLON)
	body := p.parseCatchArmBody(pos)
	return ast.CatchArm{Position: pos, Name: name, Body: body}
}
func (p *Parser) parseCatchArmBody(pos lexer.Pos) []ast.Stmt {
	if p.match(lexer.TOKEN_NEWLINE) {
		return p.parseBlock()
	}
	value := p.parseExpr()
	return []ast.Stmt{&ast.ExprStmt{Position: pos, Expr: value}}
}
func (p *Parser) parseVisitExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("visit")
	value := p.withAsCastDisabled(p.parseExpr)
	var root ast.TypeExpr
	if p.match(lexer.TOKEN_AS) {
		root = p.parseTypeExpr()
	}
	arms := p.parseVisitArms()
	return &ast.VisitExpr{Position: pos, Value: value, Root: root, Arms: arms}
}
func (p *Parser) parseFoldExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("fold")
	value := p.withAsCastDisabled(p.parseExpr)
	p.expect(lexer.TOKEN_AS)
	root := p.parseTypeExpr()
	p.expectIdentText("into")
	resultType := p.parseTypeExpr()
	arms := p.parseVisitArms()
	return &ast.FoldExpr{Position: pos, Value: value, Root: root, ResultType: resultType, Arms: arms}
}
func (p *Parser) parseRewriteExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("rewrite")
	value := p.withAsCastDisabled(p.parseExpr)
	p.expect(lexer.TOKEN_AS)
	root := p.parseTypeExpr()
	rewriteDefault := p.matchIdentText("default")
	arms := p.parseVisitArms()
	return &ast.FoldExpr{Position: pos, Keyword: "rewrite", Value: value, Root: root, ResultType: root, RewriteDefault: rewriteDefault, Arms: arms}
}
func (p *Parser) parseLambdaExpr() ast.Expr {
	pos := p.cur().Pos
	keyword := p.expect(lexer.TOKEN_IDENT).Text
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
				params = append(params, p.parseParam(false))
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
func (p *Parser) parseQualifiedTargetName() (string, lexer.Pos) {
	pos := p.cur().Pos
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_DOT) {
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
func (p *Parser) parseNamedArgList(endToken lexer.TokenKind, allowSpread bool) ([]ast.WithArg, bool) {
	args := make([]ast.WithArg, 0, p.estimateCommaSeparatedCount(endToken))
	spread := false
	if p.peek() == endToken {
		return nil, false
	}
	for {
		if allowSpread && p.match(lexer.TOKEN_RANGE) {
			if spread {
				p.errorf("with bundle spread `..` can only appear once")
			}
			spread = true
		} else {
			pos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			arg := ast.WithArg{Position: pos, Name: name}
			switch {
			case p.match(lexer.TOKEN_ASSIGN):
				arg.Value = p.parseExpr()
			case p.match(lexer.TOKEN_COLON):
				if p.peek() == endToken || p.peek() == lexer.TOKEN_COMMA {
					arg.Value = &ast.Ident{Position: pos, Name: name}
					arg.Shorthand = true
				} else {
					arg.Value = p.parseExpr()
				}
			default:
				p.errorf("expected `=` or `:` after named argument %q", name)
				arg.Value = &ast.Ident{Position: pos, Name: name}
				arg.Shorthand = true
			}
			args = append(args, arg)
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == endToken {
			break
		}
	}
	return args, spread
}
func (p *Parser) parseValueParamPackUse() ast.ParamPackUse {
	pos := p.tokens[p.pos-1].Pos
	pack := ast.ParamPackUse{Position: pos, Name: p.parseQualifiedDeclName()}
	if p.peek() != lexer.TOKEN_LPAREN {
		pack.Bare = true
		return pack
	}
	p.expect(lexer.TOKEN_LPAREN)
	pack.Args, _ = p.parseNamedArgList(lexer.TOKEN_RPAREN, false)
	p.expect(lexer.TOKEN_RPAREN)
	return pack
}
func (p *Parser) parseCallArgs() ([]ast.Expr, []string, []bool, []ast.ParamPackUse, []ast.CallArgItem, bool, lexer.Pos) {
	argCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN)
	args := make([]ast.Expr, 0, argCapacity)
	argNames := make([]string, 0, argCapacity)
	argShorthand := make([]bool, 0, argCapacity)
	packs := make([]ast.ParamPackUse, 0, 1)
	items := make([]ast.CallArgItem, 0, argCapacity)
	hasArgForward := false
	argForwardPos := lexer.Pos{}
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil, nil, nil, nil, false, lexer.Pos{}
	}
	sawPack := false
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
		if p.matchIdentText("use") {
			if sawPack {
				p.errorf("call parameter-pack application may appear at most once")
			}
			if len(argNames) != 0 {
				for _, name := range argNames {
					if name != "" {
						p.errorf("call parameter-pack application must appear before ordinary named arguments")
						break
					}
				}
			}
			pack := p.parseValueParamPackUse()
			packs = append(packs, pack)
			items = append(items, ast.CallArgItem{Position: pack.Position, Pack: pack, IsPack: true})
			sawPack = true
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
				shorthand = true
			}
		}
		var arg ast.Expr
		if shorthand {
			arg = &ast.Ident{Position: namePos, Name: name}
		} else {
			arg = p.withAsCastEnabled(p.parseExpr)
		}
		if name == "" && sawPack {
			p.errorf("call parameter-pack application must come after all positional arguments")
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
	if !hasNamed && len(packs) == 0 {
		return args, nil, nil, nil, nil, hasArgForward, argForwardPos
	}
	return args, argNames, argShorthand, packs, items, hasArgForward, argForwardPos
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
func (p *Parser) parseWithBundleNamedArgs() ([]ast.WithArg, bool) {
	return p.parseNamedArgList(lexer.TOKEN_RPAREN, true)
}
func (p *Parser) parseWithValueClause() ([]ast.WithArg, []ast.WithBundleUse, []ast.WithItem) {
	p.expect(lexer.TOKEN_WITH)
	args := make([]ast.WithArg, 0, 2)
	bundles := make([]ast.WithBundleUse, 0, 1)
	items := make([]ast.WithItem, 0, 2)
	for {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		qualifiedName := name
		hasDot := false
		for p.match(lexer.TOKEN_DOT) {
			hasDot = true
			qualifiedName += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
		switch {
		case p.peek() == lexer.TOKEN_LPAREN:
			p.advance()
			bundleArgs, spread := p.parseWithBundleNamedArgs()
			p.expect(lexer.TOKEN_RPAREN)
			bundle := ast.WithBundleUse{Position: pos, Name: qualifiedName, Args: bundleArgs, Spread: spread}
			bundles = append(bundles, bundle)
			items = append(items, ast.WithItem{Position: pos, Bundle: bundle, IsBundle: true})
		case !hasDot && p.match(lexer.TOKEN_ASSIGN):
			arg := ast.WithArg{Position: pos, Name: name, Value: p.parseExpr()}
			args = append(args, arg)
			items = append(items, ast.WithItem{Position: pos, Arg: arg})
		case !hasDot:
			arg := ast.WithArg{Position: pos, Name: name, Value: &ast.Ident{Position: pos, Name: name}, Shorthand: true}
			args = append(args, arg)
			items = append(items, ast.WithItem{Position: pos, Arg: arg})
		default:
			p.errorf("implicit bundle use %q requires (...) in a with clause", qualifiedName)
			bundle := ast.WithBundleUse{Position: pos, Name: qualifiedName}
			bundles = append(bundles, bundle)
			items = append(items, ast.WithItem{Position: pos, Bundle: bundle, IsBundle: true})
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return args, bundles, items
}
func (p *Parser) attachOptionalCallWithClause(expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || p.peek() != lexer.TOKEN_WITH {
		return expr
	}
	call.WithArgs, call.WithBundles, call.WithItemOrder = p.parseWithValueClause()
	return call
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
	if p.peek() != lexer.TOKEN_IDENT || (p.cur().Text != "lambda" && p.cur().Text != "λ") {
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
				if !p.match(lexer.TOKEN_COLON) {
					return false
				}
				_ = p.parseTypeExpr()
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
