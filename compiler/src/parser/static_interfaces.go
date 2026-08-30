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
	_, _, _, genericParams := p.parseFuncGenericParams()

	// Optional base-protocol list (protocol inheritance): `protocol Ord is Eq, Show:`.
	// `is` is the language's one subtype/conformance spelling (enum hierarchies
	// `enum Child is Parent`, refinements `u64 is Pred`), so protocols use it too.
	var bases []string
	if p.match(lexer.TOKEN_IS) {
		for {
			bases = append(bases, p.parseQualifiedDeclName())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_COLON)

	// The removed double-colon form `protocol Ord: Eq, Show:` gets a directed
	// diagnostic; recover by consuming the stray base list (and its block colon)
	// as if it were spelled with `is`, so the member block still parses cleanly.
	if len(bases) == 0 && p.peek() == lexer.TOKEN_IDENT {
		p.errorf("`protocol %s: Parent:` has been removed; use `protocol %s is Parent:`", name, name)
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

	return &ast.InterfaceDecl{Position: pos, Name: name, GenericParams: genericParams, Bases: bases, Members: members}
}

// parseEffectDecl parses an abstract algebraic effect signature. Effects use the
// same operation-signature representation as protocols, but are handled by name
// rather than by a receiver type: `effect Writer[T]: def write(value: T) -> void`.
func (p *Parser) parseEffectDecl() *ast.InterfaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("effect")
	name := p.expect(lexer.TOKEN_IDENT).Text
	_, _, _, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]ast.InterfaceMember, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_DEF {
			members = append(members, p.parseInterfaceMethodDecl())
			continue
		}
		p.errorf("abstract effect members must be operation declarations, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.InterfaceDecl{Position: pos, Name: name, IsEffect: true, GenericParams: genericParams, Members: members}
}

func (p *Parser) parseAssociatedTypeDecl() *ast.AssociatedTypeDecl {
	pos := p.cur().Pos
	p.expectIdentText("type")
	name := p.expect(lexer.TOKEN_IDENT).Text
	// An optional `= <type>` introduces a DEFAULT binding for the associated type that conforming
	// impls inherit unless they override it (`type Field = u64 is PageAligned`). The default type
	// may carry a refinement, which is parsed as part of the type expression and propagated to the
	// inheriting impl by the analyzer.
	var defaultType ast.TypeExpr
	if p.match(lexer.TOKEN_ASSIGN) {
		defaultType = p.parseTypeExpr()
	}
	p.expectNewline()
	return &ast.AssociatedTypeDecl{Position: pos, Name: name, DefaultType: defaultType}
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

	// Frame conditions on a protocol method: `changes`/`preserves` declare the upper-bound frame an
	// impl may write (P4 effect/frame variance — impl `changes` must be a subset).
	var changes, preserves []ast.EnsuresPath
	if p.matchIdentText("changes") {
		changes = p.parseChangesPathsAfterKeyword()
	}
	if p.matchIdentText("preserves") {
		preserves = p.parseChangesPathsAfterKeyword()
	}

	// A trailing `:` introduces a DEFAULT METHOD BODY: the protocol method carries an
	// implementation that conforming types inherit unless they override it. Represented as a
	// FuncDecl member (mirroring the impl-method shape) rather than a bodiless ExternFuncDecl.
	if p.match(lexer.TOKEN_COLON) {
		body := p.parseFuncBodyAfterColon()
		reqs, reqProofs, ensureVals, ensureProofs, _, _, _, body := liftLeadingContracts(body)
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			RegionParams:     regionParams,
			Permissions:      permissions,
			Ensures:          ensures,
			Requires:         reqs,
			RequiresProofs:   reqProofs,
			EnsureValues:     ensureVals,
			EnsureProofs:     ensureProofs,
			Changes:          changes,
			Preserves:        preserves,
			Params:           params,
			ReturnType:       retType,
			Body:             body,
		}
	}

	// A bodiless protocol method may still carry value contracts as an indented block of
	// `requires`/`ensure` statements with NO `:` introducing an executable body (docs P1):
	//     def get(self: Self, i: usize) -> Elem
	//         requires i < self.count()
	// These behavioral-subtyping contracts are checked against each impl with the correct
	// variance. Parse the indented contract-only block and lift it into the bodiless decl.
	// The indented block is the uniform contract surface: it may carry requires/ensure AND
	// changes/preserves. Merge any block-form frame conditions with the signature-line form
	// (back-compat) so the decl's Changes/Preserves are populated from EITHER place.
	reqs, ensureVals, blockChanges, blockPreserves := p.parseBodilessMethodContracts()
	changes = append(changes, blockChanges...)
	preserves = append(preserves, blockPreserves...)
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
		Requires:         reqs,
		EnsureValues:     ensureVals,
		Changes:          changes,
		Preserves:        preserves,
		Params:           params,
		ReturnType:       retType,
	}
}

// parseBodilessMethodContracts parses an optional indented block of contract clauses attached to a
// bodiless protocol/impl method signature. The signature line ends with a newline; if the following
// line is INDENTed it must contain only contract clauses (a stray non-contract statement is a parse
// error, since there is no executable body here). All four clause kinds are accepted uniformly in
// this block — value contracts `requires`/`ensure` AND frame conditions `changes`/`preserves` — so
// the indented block is the single consistent contract surface (the signature-line `changes`/
// `preserves` form is still parsed by the caller for back-compat). Returns the lifted preconditions,
// postconditions, and frame paths.
func (p *Parser) parseBodilessMethodContracts() (requires []ast.Expr, ensures []ast.Expr, changes []ast.EnsuresPath, preserves []ast.EnsuresPath) {
	if p.peek() != lexer.TOKEN_NEWLINE {
		return nil, nil, nil, nil
	}
	// Look past the newline for an INDENT; if absent, there is no contract block.
	if p.peekAt(1) != lexer.TOKEN_INDENT {
		return nil, nil, nil, nil
	}
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		// Frame conditions are parsed by keyword dispatch (they have no ContractStmt kind); value
		// contracts go through the contextual-statement parser as ContractStmts.
		switch {
		case p.matchIdentText("changes"):
			changes = append(changes, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
			continue
		case p.matchIdentText("preserves"):
			preserves = append(preserves, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
			continue
		}
		stmt := p.parseContextualStmt()
		cs, ok := stmt.(*ast.ContractStmt)
		if !ok || cs == nil {
			p.errorf("bodiless protocol method may only carry requires/ensure/changes/preserves contracts")
			continue
		}
		switch cs.Kind {
		case ast.ContractRequire:
			if cs.Cond != nil {
				requires = append(requires, cs.Cond)
			}
		case ast.ContractEnsure:
			if cs.Cond != nil {
				ensures = append(ensures, cs.Cond)
			}
		default:
			p.errorf("bodiless protocol method may only carry requires/ensure/changes/preserves contracts")
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return requires, ensures, changes, preserves
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

// parseHandlerDecl parses a static, first-order realization of an abstract
// effect. Captured handler parameters are prepended to every operation method;
// the semantic pass later rewrites abstract operation calls to those direct
// methods, so no handler object or continuation allocation is needed at runtime.
func (p *Parser) parseHandlerDecl() *ast.ImplDecl {
	pos := p.cur().Pos
	p.expectIdentText("handler")
	handlerStatic := false
	for {
		if p.peek() == lexer.TOKEN_STATIC {
			p.advance()
			handlerStatic = true
			continue
		}
		break
	}
	handlerName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	handlerParams, _ := p.parseExplicitSignatureParamList(false, false)
	p.expect(lexer.TOKEN_RPAREN)
	p.expectIdentText("for")
	effectType := p.parseTypeExpr()
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
		if p.peek() != lexer.TOKEN_DEF {
			p.errorf("effect handlers may only contain operation methods, got %s", p.cur())
			p.advance()
			continue
		}
		member := p.parseImplMethodDeclWithAnnotations(annotations, false)
		if fn, ok := member.(*ast.FuncDecl); ok {
			params := make([]ast.ParamDecl, 0, len(handlerParams)+len(fn.Params))
			params = append(params, handlerParams...)
			params = append(params, fn.Params...)
			fn.Params = params
		} else if fn, ok := member.(*ast.ExternFuncDecl); ok {
			params := make([]ast.ParamDecl, 0, len(handlerParams)+len(fn.Params))
			params = append(params, handlerParams...)
			params = append(params, fn.Params...)
			fn.Params = params
		}
		members = append(members, member)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.ImplDecl{
		Position:      pos,
		HandlerName:   handlerName,
		HandlerParams: handlerParams,
		HandlerEffect: effectType,
		ForType:       effectType,
		IsHandler:     true,
		HandlerStatic: handlerStatic,
		Members:       members,
	}
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

	// Frame conditions on an impl method (`changes`/`preserves`): checked for SUBSET conformance
	// against the protocol method's frame (P4) and enforced intraprocedurally as on any function.
	var changes, preserves []ast.EnsuresPath
	if p.matchIdentText("changes") {
		changes = p.parseChangesPathsAfterKeyword()
	}
	if p.matchIdentText("preserves") {
		preserves = p.parseChangesPathsAfterKeyword()
	}

	if p.match(lexer.TOKEN_COLON) {
		body := p.parseFuncBodyAfterColon()
		// Lift leading value contracts (`requires`/`ensure`) off the impl-method body into the decl,
		// mirroring the top-level function parser. These are checked for behavioral-subtyping variance
		// against the protocol method's contracts (contravariant requires, covariant ensure + P4's
		// effect/error/frame subset variance).
		reqs, reqProofs, ensureVals, ensureProofs, _, _, _, body := liftLeadingContracts(body)
		return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Requires: reqs, RequiresProofs: reqProofs, EnsureValues: ensureVals, EnsureProofs: ensureProofs, Changes: changes, Preserves: preserves, Params: params, ReturnType: retType, Body: body}
	}
	reqs, ensureVals, blockChanges, blockPreserves := p.parseBodilessMethodContracts()
	changes = append(changes, blockChanges...)
	preserves = append(preserves, blockPreserves...)
	p.expectNewline()
	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Override: override, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Requires: reqs, EnsureValues: ensureVals, Changes: changes, Preserves: preserves, Params: params, ReturnType: retType}
}
