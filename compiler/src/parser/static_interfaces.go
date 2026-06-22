package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	p.expectIdentText("protocol")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)

	// Optional base-protocol list (protocol inheritance): `protocol Ord: Eq, Show:`.
	// The first colon is consumed above; if it is followed by an identifier (rather than the
	// block-opening newline) those identifiers are the inherited protocols, terminated by a
	// second colon that opens the member block.
	var bases []string
	if p.peek() == lexer.TOKEN_IDENT {
		for {
			bases = append(bases, p.parseQualifiedDeclName())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_COLON)
	}

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
		// A protocol LAW (docs/85 P3): `law total(a: Self, b: Self) = a.le(b) or b.le(a)`. It is an
		// algebraic obligation every conforming impl must satisfy, parsed with the existing law grammar
		// (a bool-returning FuncDecl with IsLaw set) and folded into the protocol's member set. Its
		// params range over `Self` (and related types); the analyzer discharges it per impl by SMT or,
		// on a non-affine body, auto-lowers it to a per-impl @property fuzz harness.
		if p.peekIdentText("law") && p.looksLikeLawDecl() {
			decl := p.parseLawDecl()
			if fn, ok := decl.(*ast.FuncDecl); ok {
				members = append(members, fn)
			} else {
				p.errorf("a protocol law must be a predicate law (`law Name(...) = <bool-expr>`)")
			}
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

	return &ast.InterfaceDecl{Position: pos, Name: name, Bases: bases, Members: members}
}

func (p *Parser) parseAssociatedTypeDecl() *ast.AssociatedTypeDecl {
	pos := p.cur().Pos
	p.expectIdentText("type")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.AssociatedTypeDecl{Position: pos, Name: name}
}

func (p *Parser) parseInterfaceMethodDecl() ast.InterfaceMember {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	name := p.expect(lexer.TOKEN_IDENT).Text

	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}

	// A trailing `:` introduces a DEFAULT METHOD BODY: the protocol method carries an
	// implementation that conforming types inherit unless they override it. Represented as a
	// FuncDecl member (mirroring the impl-method shape) rather than a bodiless ExternFuncDecl.
	if p.match(lexer.TOKEN_COLON) {
		body := p.parseFuncBodyAfterColon()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			RegionParams:     regionParams,
			Permissions:      permissions,
			Ensures:          ensures,
			Params:           params,
			ReturnType:       retType,
			Body:             body,
		}
	}

	p.expectNewline()
	return &ast.ExternFuncDecl{
		Position:         pos,
		Name:             name,
		TypeParams:       typeParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		RegionParams:     regionParams,
		Permissions:      permissions,
		Ensures:          ensures,
		Params:           params,
		ReturnType:       retType,
	}
}

func (p *Parser) parseImplDecl() *ast.ImplDecl {
	return p.parseImplDeclWithAnnotations(nil)
}

func (p *Parser) parseImplDeclWithAnnotations(annotations []ast.Annotation) *ast.ImplDecl {
	pos := p.cur().Pos
	p.expectIdentText("impl")
	var genericParams []ast.GenericParam
	if p.peek() == lexer.TOKEN_LBRACKET {
		// `impl[T] Iface for Box[T]:` — parametric impl. Reuse the function generic-param
		// grammar; only the plain type params are relevant for impl matching.
		_, _, _, genericParams = p.parseFuncGenericParams()
	}
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

	return &ast.ImplDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), InterfaceName: interfaceName, GenericParams: genericParams, ForType: forType, Members: members}
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

	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}

	if p.match(lexer.TOKEN_COLON) {
		body := p.parseFuncBodyAfterColon()
		return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Params: params, ReturnType: retType, Body: body}
	}
	p.expectNewline()
	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Params: params, ReturnType: retType}
}
