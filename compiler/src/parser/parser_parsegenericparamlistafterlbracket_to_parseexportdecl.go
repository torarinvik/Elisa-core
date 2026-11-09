package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseGenericParamListAfterLBracket(allowRegion bool, allowPermission bool) ([]string, []string, []string, []ast.GenericParam) {
	paramCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET)
	typeParams := make([]string, 0, paramCapacity)
	regionParams := make([]string, 0, paramCapacity)
	permissionParams := make([]string, 0, paramCapacity)
	genericParams := make([]ast.GenericParam, 0, paramCapacity)
	seenType := map[string]bool{}
	seenRegion := map[string]bool{}
	seenPermission := map[string]bool{}
	seenValue := map[string]bool{}
	for {
		paramPos := p.cur().Pos
		kind := ast.GenericParamType
		isRegionParam := allowRegion && p.match(lexer.TOKEN_REGION)
		if isRegionParam {
			p.errorAt(paramPos, "region-param spelling `region %s` has been removed; use `@%s`", p.cur().Text, p.cur().Text)
		}
		if !isRegionParam && allowRegion && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "region" {
			p.advance()
			p.errorAt(paramPos, "region-param spelling `region %s` has been removed; use `@%s`", p.cur().Text, p.cur().Text)
			isRegionParam = true
		}
		// Canonical region-param spelling: `@r` — the same token used at every use site
		// (`darray[T] @r`, `&@r`), so declaration and use read identically (cf. Rust lifetimes).
		// `[T, @r]` composes: the `@` marks the entry as a region.
		if !isRegionParam && allowRegion && p.peek() == lexer.TOKEN_AT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
			p.advance() // consume '@'
			isRegionParam = true
		}
		isPermissionParam := false
		if !isRegionParam && allowPermission && p.matchIdentText("permission") {
			isPermissionParam = true
		}
		isErrorSetParam := false
		if !isRegionParam && !isPermissionParam && p.matchIdentText("errorset") {
			isErrorSetParam = true
		}
		if isErrorSetParam {
			kind = ast.GenericParamErrorSet
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenType[name] = true
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: kind, Name: name})
			}
		} else if isRegionParam {
			kind = ast.GenericParamRegion
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenRegion[name] = true
				regionParams = append(regionParams, name)
			}
		} else if isPermissionParam {
			kind = ast.GenericParamPermission
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenPermission[name] = true
				permissionParams = append(permissionParams, name)
			}
		} else {
			name := p.expect(lexer.TOKEN_IDENT).Text
			boundName := ""
			if p.match(lexer.TOKEN_COLON) {
				boundName = p.parseQualifiedDeclName()
			}
			if seenType[name] || seenRegion[name] || seenPermission[name] || seenValue[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else if boundName != "" && isBuiltinValueGenericParamTypeName(boundName) {
				seenValue[name] = true
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: ast.GenericParamValue, Name: name, InterfaceBound: boundName})
			} else {
				seenType[name] = true
				typeParams = append(typeParams, name)
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: kind, Name: name, InterfaceBound: boundName})
			}
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return typeParams, regionParams, permissionParams, genericParams
}

func isBuiltinValueGenericParamTypeName(name string) bool {
	switch name {
	case "usize", "isize", "uintptr",
		"int", "i8", "i16", "i32", "i64",
		"u8", "u16", "u32", "u64":
		return true
	default:
		return false
	}
}
func (p *Parser) parsePermissionRef() ast.PermissionRef {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeArgs := p.parsePermissionTypeArgs()
	member := ""
	if p.match(lexer.TOKEN_DOT) {
		member = p.expect(lexer.TOKEN_IDENT).Text
	}
	return ast.PermissionRef{Position: pos, Name: name, Member: member, TypeArgs: typeArgs}
}

func (p *Parser) parsePermissionTypeArgs() []ast.TypeExpr {
	if !p.match(lexer.TOKEN_LBRACKET) {
		return nil
	}
	args := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	for {
		args = append(args, p.parseTypeExpr())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return args
}

// parsePermissionRefGroup parses one permission item, expanding the member-brace sugar
// `Family{M1, M2, ...}` into one PermissionRef per member (equivalent to repeating
// `Family.M1, Family.M2`). `Family` and `Family.Member` yield a single ref.
func (p *Parser) parsePermissionRefGroup() []ast.PermissionRef {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeArgs := p.parsePermissionTypeArgs()
	if p.match(lexer.TOKEN_LBRACE) {
		var refs []ast.PermissionRef
		for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
			mpos := p.cur().Pos
			member := p.expect(lexer.TOKEN_IDENT).Text
			refs = append(refs, ast.PermissionRef{Position: mpos, Name: name, Member: member, TypeArgs: typeArgs})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACE)
		return refs
	}
	member := ""
	if p.match(lexer.TOKEN_DOT) {
		member = p.expect(lexer.TOKEN_IDENT).Text
	}
	ref := ast.PermissionRef{Position: pos, Name: name, Member: member, TypeArgs: typeArgs}
	if p.matchIdentText("via") {
		ref.Via = []ast.PermissionRef{p.parsePermissionRef()}
	}
	return []ast.PermissionRef{ref}
}
func signatureHasExplicitErrorEffects(retType ast.TypeExpr) bool {
	_, ok := retType.(*ast.ErrorUnionTypeExpr)
	return ok
}
func (p *Parser) parsePermissionRefs(bracketed bool) []ast.PermissionRef {
	if bracketed {
		p.expect(lexer.TOKEN_LBRACKET)
	} else if p.match(lexer.TOKEN_LBRACKET) {
		bracketed = true
	}
	capacity := 1
	if bracketed {
		capacity = p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET)
	}
	refs := make([]ast.PermissionRef, 0, capacity)
	for {
		refs = append(refs, p.parsePermissionRefGroup()...)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	if bracketed {
		p.expect(lexer.TOKEN_RBRACKET)
	}
	return refs
}
func (p *Parser) parseEnsuresPath() ast.EnsuresPath {
	pos := p.cur().Pos
	root := p.expect(lexer.TOKEN_IDENT).Text
	fields := make([]string, 0, 2)
	for p.match(lexer.TOKEN_DOT) {
		fields = append(fields, p.expect(lexer.TOKEN_IDENT).Text)
	}
	return ast.EnsuresPath{Position: pos, Root: root, Fields: fields}
}
func (p *Parser) parseEnsuresCondition() ast.EnsuresCondition {
	pos := p.cur().Pos
	if !p.match(lexer.TOKEN_RETURN) {
		return ast.EnsuresCondition{Position: pos, Kind: ast.EnsuresConditionAlways}
	}
	condition := ast.EnsuresCondition{Position: pos, Kind: ast.EnsuresConditionReturnBool}
	switch p.peek() {
	case lexer.TOKEN_TRUE:
		p.advance()
		condition.ReturnBool = true
	case lexer.TOKEN_FALSE:
		p.advance()
		condition.ReturnBool = false
	default:
		p.errorf("ensures return condition expects true or false, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_FATARROW)
	return condition
}
func (p *Parser) parseEnsuresStateCases() []string {
	stateCases := make([]string, 0, 2)
	stateCases = append(stateCases, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_PIPE) {
		stateCases = append(stateCases, p.expect(lexer.TOKEN_IDENT).Text)
	}
	return stateCases
}
func (p *Parser) parseEnsuresClause() ast.EnsuresClause {
	pos := p.cur().Pos
	condition := p.parseEnsuresCondition()
	target := p.parseEnsuresPath()
	// `ensures <param> is <Law>` / `ensures <param> is <Law>[args]` (docs/85): a law-predicate
	// postcondition, no `=>` arm. Bracket args make it a parametric refinement (`Bounded[0, 500]`,
	// `Bounded[0..500]` desugaring to its two endpoints), mirroring parseRefinementPred.
	if p.match(lexer.TOKEN_IS) {
		law := p.expect(lexer.TOKEN_IDENT).Text
		var args []ast.Expr
		if p.match(lexer.TOKEN_LBRACKET) {
			args = p.parseRefinementPredArgs()
			p.expect(lexer.TOKEN_RBRACKET)
		}
		return ast.EnsuresClause{Position: pos, Condition: condition, Target: target, Kind: ast.EnsuresKindRefinement, RefinementLaw: law, RefinementArgs: args}
	}
	p.expect(lexer.TOKEN_FATARROW)
	if p.matchIdentText("preserve") {
		return ast.EnsuresClause{Position: pos, Condition: condition, Target: target, Kind: ast.EnsuresKindPreserve}
	}
	if p.match(lexer.TOKEN_AMPERSAND) {
		state := ast.RefStateNonNull
		if p.match(lexer.TOKEN_QUESTION) {
			state = ast.RefStateNullable
		}
		return ast.EnsuresClause{Position: pos, Condition: condition, Target: target, Kind: ast.EnsuresKindRefState, RefState: state}
	}
	if p.match(lexer.TOKEN_BANG) {
		return ast.EnsuresClause{Position: pos, Condition: condition, Target: target, Kind: ast.EnsuresKindRefState, RefState: ast.RefStateNull}
	}
	return ast.EnsuresClause{Position: pos, Condition: condition, Target: target, Kind: ast.EnsuresKindNamedState, StateCases: p.parseEnsuresStateCases()}
}

// parseChangesPathsAfterKeyword parses a comma-separated list of param-rooted paths for a frame
// `changes` clause (docs/87), e.g. `changes r.px, r.py`. Reuses parseEnsuresPath.
func (p *Parser) parseChangesPathsAfterKeyword() []ast.EnsuresPath {
	paths := make([]ast.EnsuresPath, 0, 2)
	for {
		paths = append(paths, p.parseEnsuresPath())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return paths
}

// parseFulfillsClausesAfterKeyword parses a comma-separated `fulfills` list (docs/88, docs/85 §4).
// Each item is either a FRAME-law application `<param> is <Law>` (e.g. `fulfills r is
// MovesPlayerOnly`) or a subject-free EFFECT-law application `<Law>` (e.g. `fulfills NoAlloc`). The
// form is told apart by whether `is` follows the first identifier.
func (p *Parser) parseFulfillsClausesAfterKeyword() []ast.FulfillsClause {
	clauses := make([]ast.FulfillsClause, 0, 2)
	for {
		pos := p.cur().Pos
		first := p.expect(lexer.TOKEN_IDENT).Text
		if p.match(lexer.TOKEN_IS) {
			law := p.expect(lexer.TOKEN_IDENT).Text
			clauses = append(clauses, ast.FulfillsClause{Position: pos, Param: first, Law: law})
		} else {
			clauses = append(clauses, ast.FulfillsClause{Position: pos, Param: "", Law: first})
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return clauses
}
func (p *Parser) parseEnsuresClausesAfterKeyword() []ast.EnsuresClause {
	clauses := make([]ast.EnsuresClause, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_COLON))
	for {
		clauses = append(clauses, p.parseEnsuresClause())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return clauses
}
func (p *Parser) parseFuncDeclWithAnnotations(annotations []ast.Annotation) *ast.FuncDecl {
	return p.parseFuncDeclWithAnnotationsAndStatic(annotations, false)
}

func (p *Parser) parseFuncDeclWithAnnotationsAndStatic(annotations []ast.Annotation, isStatic bool) *ast.FuncDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	return p.parseFuncDeclRest(pos, annotations, isStatic)
}

// takeReturnTypeRegion consumes the parser-side region suffix attached to the
// return type. Stage1 records `-> T @r` as a function return contract rather
// than changing the nominal type T; keeping that distinction is essential for
// recursive packed enums, whose constructor views must remain assignable to T.
func takeReturnTypeRegion(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case *ast.NamedType:
		region := t.Region
		t.Region = ""
		return region
	case *ast.GenericType:
		region := t.Region
		t.Region = ""
		return region
	case *ast.TupleTypeExpr:
		region := t.Region
		t.Region = ""
		return region
	default:
		return ""
	}
}

// parseFuncDeclRest parses everything after the opening keyword of a function-shaped declaration
// (`def` or `lemma`): the name, signature, contract clauses, and body. Shared so a `lemma` reuses
// the entire function grammar (generics, params, requires/ensure, body) and differs only by the
// IsLemma flag its caller sets on the returned decl.
func (p *Parser) parseFuncDeclRest(pos lexer.Pos, annotations []ast.Annotation, isStatic bool) *ast.FuncDecl {
	name := p.expect(lexer.TOKEN_IDENT).Text

	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params, variadic := p.parseExplicitSignatureParamList(true, true)
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	returnRegion := ""
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
		returnRegion = takeReturnTypeRegion(retType)
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}

	var changes []ast.EnsuresPath
	if p.matchIdentText("changes") {
		changes = p.parseChangesPathsAfterKeyword()
	}

	var preserves []ast.EnsuresPath
	if p.matchIdentText("preserves") {
		preserves = p.parseChangesPathsAfterKeyword()
	}

	var fulfills []ast.FulfillsClause
	if p.matchIdentText("fulfills") {
		fulfills = p.parseFulfillsClausesAfterKeyword()
	}

	// docs/125 §5: `state` declarations are function-local; clear any left uncollected by a
	// prior function (malformed states-without-`start`) so they cannot leak into this body.
	p.pendingStateDecls = nil

	var body []ast.Stmt
	if p.match(lexer.TOKEN_ASSIGN) {
		// Expression-bodied function: `def f(...) -> T = EXPR` is exactly
		// `def f(...) -> T: return EXPR` (same `=` "the definition is this expression"
		// as `law`/`type`/`alias`/`export func`). One expression, so no leading
		// contracts — for `requires`/`ensure` use the block body form.
		valuePos := p.cur().Pos
		valueExpr := p.parseExpr()
		p.expectNewline()
		body = []ast.Stmt{&ast.ReturnStmt{Position: valuePos, Value: valueExpr}}
	} else {
		p.expect(lexer.TOKEN_COLON)
		if isStatic {
			body = p.withStaticFunctionBody(p.parseFuncBodyAfterColon)
		} else {
			body = p.parseFuncBodyAfterColon()
		}
	}
	// Value contracts are written as the FIRST statements of the body (`requires <bool-expr>` /
	// `ensure <bool-expr>`), then lifted out into the decl here. They are NOT post-signature
	// clauses: `-> T requires ...` is ambiguous with the region-prefix type grammar (`<region> T&`),
	// where `T requires` reads as region label `T` + type `requires`.
	requires, requireProofs, ensures2, ensureProofs, decreases, decreasesWild, uses, body := liftLeadingContracts(body)
	body = normalizeFunctionTailReturns(body, retType)
	if !isStatic {
		// Static functions are evaluated at compile time and have no runtime region; never
		// wrap them in an auto region (it would break static darray construction).
		body = wrapReclaimableLoopBodies(body)
		desugarDStrReturnLiterals(body, retType)
		body = p.maybeWrapFunctionBodyInAutoRegion(body, params, pos)
	}
	fn := &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Static: isStatic, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Changes: changes, Preserves: preserves, Fulfills: fulfills, Requires: requires, RequiresProofs: requireProofs, EnsureValues: ensures2, EnsureProofs: ensureProofs, Decreases: decreases, DecreasesWild: decreasesWild, Uses: uses, Params: params, Variadic: variadic, ReturnType: retType, ReturnRegion: returnRegion, Body: body}
	// docs/120 §2: validate and erase a declared lmut-threading return manifest
	// (`-> (ch: char, lexer: lmut Lexer)`) before anything downstream sees the type.
	p.applyDeclaredLmutThreading(fn)
	return fn
}
