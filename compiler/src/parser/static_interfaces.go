package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (p *Parser) peekQualifiedDeclNameFollowedBy(text string) bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	i := p.pos + 1
	for i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_DOT && p.tokens[i+1].Kind == lexer.TOKEN_IDENT {
		i += 2
	}
	return i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_IDENT && p.tokens[i].Text == text
}

func (p *Parser) parseInterfaceDecl() *ast.InterfaceDecl {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_STATIC {
		p.advance()
	}
	p.expectIdentText("interface")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]ast.InterfaceMember, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		annotations := p.parseAnnotations()
		if len(annotations) != 0 {
			for _, annotation := range annotations {
				p.errorf("interface members do not support annotation @%s", annotation.Name)
			}
		}
		if p.peekIdentText("type") {
			members = append(members, p.parseAssociatedTypeDecl())
			continue
		}
		if p.peek() == lexer.TOKEN_DEF {
			members = append(members, p.parseInterfaceMethodDecl())
			continue
		}
		p.errorf("expected interface member declaration, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.InterfaceDecl{Position: pos, Name: name, Members: members}
}

func (p *Parser) parseAssociatedTypeDecl() *ast.AssociatedTypeDecl {
	pos := p.cur().Pos
	p.expectIdentText("type")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.AssociatedTypeDecl{Position: pos, Name: name}
}

func (p *Parser) parseInterfaceMethodDecl() *ast.ExternFuncDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	name := p.expect(lexer.TOKEN_IDENT).Text

	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params, paramPacks, paramItemOrder, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)

	var implicitParams []ast.ParamDecl
	var implicitBundles []string
	var implicitItemOrder []ast.ImplicitSigItem
	if p.peek() == lexer.TOKEN_WITH {
		implicitParams, implicitBundles, implicitItemOrder = p.parseWithSignatureClause()
	}

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	effectAliasPos := lexer.Pos{}
	effectAlias := ""
	var effects []ast.SignatureEffectItem
	if p.matchIdentText("effects") {
		effectAliasPos = p.tokens[p.pos-1].Pos
		if p.peek() == lexer.TOKEN_LBRACKET {
			effects = p.parseSignatureEffectsClause()
		} else {
			effectAlias = p.parseQualifiedDeclName()
		}
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	if effectAlias != "" {
		if signatureHasExplicitErrorEffects(retType) {
			p.errorf("effects alias cannot be combined with an explicit error[...] clause")
		}
		if len(permissions) != 0 {
			p.errorf("effects alias cannot be combined with an explicit can[...] clause")
		}
	}

	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}

	p.expectNewline()
	return &ast.ExternFuncDecl{
		Position:          pos,
		Name:              name,
		TypeParams:        typeParams,
		RefStorageParams:  refStorageParams,
		RefStateParams:    refStateParams,
		PermissionParams:  permissionParams,
		GenericParams:     genericParams,
		RegionParams:      regionParams,
		EffectAliasPos:    effectAliasPos,
		EffectAlias:       effectAlias,
		Effects:           effects,
		Permissions:       permissions,
		Ensures:           ensures,
		Params:            params,
		ParamPacks:        paramPacks,
		ParamItemOrder:    paramItemOrder,
		ImplicitParams:    implicitParams,
		ImplicitBundles:   implicitBundles,
		ImplicitItemOrder: implicitItemOrder,
		ReturnType:        retType,
	}
}

func (p *Parser) parseImplDecl() *ast.ImplDecl {
	return p.parseImplDeclWithAnnotations(nil)
}

func (p *Parser) parseImplDeclWithAnnotations(annotations []ast.Annotation) *ast.ImplDecl {
	pos := p.cur().Pos
	p.expectIdentText("impl")
	interfaceName := ""
	var forType ast.TypeExpr
	if p.peekQualifiedDeclNameFollowedBy("for") {
		interfaceName = p.parseQualifiedDeclName()
		p.expectIdentText("for")
		forType = p.parseTypeExpr()
	} else {
		forType = p.parseTypeExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]ast.ImplMember, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		annotations := p.parseAnnotations()
		override := false
		if p.peekIdentText("override") {
			override = true
			p.advance()
		}
		if p.peekIdentText("type") {
			if interfaceName == "" {
				p.errorf("extension impls do not support associated types")
			}
			if override {
				p.errorf("override can only be applied to impl methods")
			}
			if len(annotations) != 0 {
				for _, annotation := range annotations {
					p.errorf("impl associated types do not support annotation @%s", annotation.Name)
				}
			}
			members = append(members, p.parseImplAssociatedTypeDecl())
			continue
		}
		if p.peek() == lexer.TOKEN_DEF {
			members = append(members, p.parseImplMethodDeclWithAnnotations(annotations, override))
			continue
		}
		if override {
			p.errorf("override in an impl must be followed by def, got %s", p.cur())
		}
		if len(annotations) != 0 {
			p.errorf("impl member annotations must be followed by def, got %s", p.cur())
		}
		p.errorf("expected impl member declaration, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.ImplDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), InterfaceName: interfaceName, ForType: forType, Members: members}
}

func (p *Parser) parseImplAssociatedTypeDecl() *ast.ImplAssociatedTypeDecl {
	pos := p.cur().Pos
	p.expectIdentText("type")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	typ := p.parseTypeExpr()
	p.expectNewline()
	return &ast.ImplAssociatedTypeDecl{Position: pos, Name: name, Type: typ}
}

func (p *Parser) parseImplMethodDeclWithAnnotations(annotations []ast.Annotation, override bool) ast.ImplMember {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	name := p.expect(lexer.TOKEN_IDENT).Text

	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params, paramPacks, paramItemOrder, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)

	var implicitParams []ast.ParamDecl
	var implicitBundles []string
	var implicitItemOrder []ast.ImplicitSigItem
	if p.peek() == lexer.TOKEN_WITH {
		implicitParams, implicitBundles, implicitItemOrder = p.parseWithSignatureClause()
	}

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	effectAliasPos := lexer.Pos{}
	effectAlias := ""
	var effects []ast.SignatureEffectItem
	if p.matchIdentText("effects") {
		effectAliasPos = p.tokens[p.pos-1].Pos
		if p.peek() == lexer.TOKEN_LBRACKET {
			effects = p.parseSignatureEffectsClause()
		} else {
			effectAlias = p.parseQualifiedDeclName()
		}
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	if effectAlias != "" {
		if signatureHasExplicitErrorEffects(retType) {
			p.errorf("effects alias cannot be combined with an explicit error[...] clause")
		}
		if len(permissions) != 0 {
			p.errorf("effects alias cannot be combined with an explicit can[...] clause")
		}
	}

	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}

	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		body := p.parseBlock()
		return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Effects: effects, Permissions: permissions, Ensures: ensures, Params: params, ParamPacks: paramPacks, ParamItemOrder: paramItemOrder, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, ReturnType: retType, Body: body}
	}
	p.expectNewline()
	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Effects: effects, Permissions: permissions, Ensures: ensures, Params: params, ParamPacks: paramPacks, ParamItemOrder: paramItemOrder, ReturnType: retType, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder}
}
