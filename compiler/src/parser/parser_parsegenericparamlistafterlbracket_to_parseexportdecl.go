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
		if !isRegionParam && allowRegion && p.peek() == lexer.TOKEN_AT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
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
	member := ""
	if p.match(lexer.TOKEN_DOT) {
		member = p.expect(lexer.TOKEN_IDENT).Text
	}
	return ast.PermissionRef{Position: pos, Name: name, Member: member}
}

// parsePermissionRefGroup parses one permission item, expanding the member-brace sugar
// `Family{M1, M2, ...}` into one PermissionRef per member (equivalent to repeating
// `Family.M1, Family.M2`). `Family` and `Family.Member` yield a single ref.
func (p *Parser) parsePermissionRefGroup() []ast.PermissionRef {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_LBRACE) {
		var refs []ast.PermissionRef
		for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
			mpos := p.cur().Pos
			member := p.expect(lexer.TOKEN_IDENT).Text
			refs = append(refs, ast.PermissionRef{Position: mpos, Name: name, Member: member})
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
	return []ast.PermissionRef{{Position: pos, Name: name, Member: member}}
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
	refs := make([]ast.PermissionRef, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
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
// parseFulfillsClausesAfterKeyword parses a comma-separated list of `<param> is <Law>` frame-law
// applications for a `fulfills` clause (docs/88), e.g. `fulfills r is MovesPlayerOnly`.
func (p *Parser) parseFulfillsClausesAfterKeyword() []ast.FulfillsClause {
	clauses := make([]ast.FulfillsClause, 0, 2)
	for {
		pos := p.cur().Pos
		param := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_IS)
		law := p.expect(lexer.TOKEN_IDENT).Text
		clauses = append(clauses, ast.FulfillsClause{Position: pos, Param: param, Law: law})
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

	p.expect(lexer.TOKEN_COLON)

	var body []ast.Stmt
	if isStatic {
		body = p.withStaticFunctionBody(p.parseFuncBodyAfterColon)
	} else {
		body = p.parseFuncBodyAfterColon()
	}
	// Value contracts are written as the FIRST statements of the body (`requires <bool-expr>` /
	// `ensure <bool-expr>`), then lifted out into the decl here. They are NOT post-signature
	// clauses: `-> T requires ...` is ambiguous with the region-prefix type grammar (`<region> T&`),
	// where `T requires` reads as region label `T` + type `requires`.
	requires, ensures2, body := liftLeadingContracts(body)
	if !isStatic {
		// Static functions are evaluated at compile time and have no runtime region; never
		// wrap them in an auto region (it would break static darray construction).
		body = wrapReclaimableLoopBodies(body)
		desugarDStrReturnLiterals(body, retType)
		body = p.maybeWrapFunctionBodyInAutoRegion(body, params, pos)
	}
	return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Static: isStatic, Name: name, TypeParams: typeParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Ensures: ensures, Changes: changes, Preserves: preserves, Fulfills: fulfills, Requires: requires, EnsureValues: ensures2, Params: params, ReturnType: retType, Body: body}
}

// liftLeadingContracts pulls leading `requires`/`ensure` value-contract statements (parsed as
// ContractStmt) off the front of a function body into precondition/postcondition lists, returning
// the remaining body. Only the leading run is honoured; a later stray ContractStmt is left in place
// so the analyzer reports the misplacement rather than silently dropping it.
func liftLeadingContracts(body []ast.Stmt) ([]ast.Expr, []ast.Expr, []ast.Stmt) {
	var requires, ensures []ast.Expr
	i := 0
	for i < len(body) {
		cs, ok := body[i].(*ast.ContractStmt)
		if !ok {
			break
		}
		if cs.Cond != nil {
			switch cs.Kind {
			case ast.ContractRequire:
				requires = append(requires, cs.Cond)
			case ast.ContractEnsure:
				ensures = append(ensures, cs.Cond)
			}
		}
		i++
	}
	if i == 0 {
		return nil, nil, body
	}
	return requires, ensures, body[i:]
}

func (p *Parser) parseFuncBodyAfterColon() []ast.Stmt {
	if p.match(lexer.TOKEN_NEWLINE) {
		return p.parseBlock()
	}
	stmts := make([]ast.Stmt, 0, 1)
	inlineLine := p.cur().Pos.Line
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT && (p.cur().Pos.Line == inlineLine || p.cur().Pos.Line == 0) {
		stmt := p.parseContextualStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
	return stmts
}

func (p *Parser) parseStmtBodyAfterColon() []ast.Stmt {
	if p.match(lexer.TOKEN_NEWLINE) {
		return p.parseBlock()
	}
	stmt := p.parseContextualStmt()
	if stmt == nil {
		return nil
	}
	return []ast.Stmt{stmt}
}

func (p *Parser) parseParamList(allowDefault bool) []ast.ParamDecl {
	params := make([]ast.ParamDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() == lexer.TOKEN_RPAREN {
		return params
	}
	for {
		params = append(params, p.parseParam(allowDefault))
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return params
}
func (p *Parser) parseExplicitSignatureParamList(allowDefault bool, allowVariadic bool) ([]ast.ParamDecl, bool) {
	params := make([]ast.ParamDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	variadic := false
	if p.peek() == lexer.TOKEN_RPAREN {
		return params, false
	}
	for {
		if allowVariadic && p.peek() == lexer.TOKEN_ELLIPSIS {
			p.advance()
			variadic = true
			break
		}
		params = append(params, p.parseParam(allowDefault))
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return params, variadic
}
func (p *Parser) parseParam(allowDefault bool) ast.ParamDecl {
	pos := p.cur().Pos
	mutable := false
	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()
	var defaultValue ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		if !allowDefault {
			p.errorf("parameter defaults are not allowed here")
			_ = p.parseExpr()
			return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable, Type: typ}
		}
		defaultValue = p.parseExpr()
	}
	return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable, Type: typ, DefaultValue: defaultValue}
}
func (p *Parser) parseExternDecl() ast.Decl {
	return p.parseExternDeclWithAnnotations(nil)
}
func (p *Parser) parseExternDeclWithAnnotations(annotations []ast.Annotation) ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_EXTERN)
	name := p.expect(lexer.TOKEN_IDENT).Text

	// extern TypeName  (opaque type - no parens, no colon)
	if p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_EOF {
		p.expectNewline()
		return &ast.ExternTypeDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name}
	}

	// extern name: Type  (variable)
	if p.peek() == lexer.TOKEN_COLON {
		p.advance()
		typ := p.parseTypeExpr()
		p.expectNewline()
		return &ast.ExternVarDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Type: typ}
	}

	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	// extern name(params...) [-> RetType]  (function)
	p.expect(lexer.TOKEN_LPAREN)
	params, variadic := p.parseExplicitSignatureParamList(true, true)
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
	p.expectNewline()

	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, PermissionParams: permissionParams, GenericParams: genericParams, RegionParams: regionParams, Permissions: permissions, Ensures: ensures, Params: params, ReturnType: retType, Variadic: variadic}
}
func (p *Parser) parseExportDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_EXPORT)
	kindText := ""
	switch p.peek() {
	case lexer.TOKEN_IDENT:
		kindText = p.advance().Text
	case lexer.TOKEN_GLOBAL:
		p.advance()
		kindText = "global"
	default:
		p.errorf("expected export type, export func, or export global, got %s", p.cur())
		p.skipNewlines()
		return nil
	}
	switch kindText {
	case "type":
		target := p.parseTypeExpr()
		p.expect(lexer.TOKEN_AS)
		alias := p.expect(lexer.TOKEN_IDENT).Text
		p.expectNewline()
		return &ast.ExportTypeDecl{Position: pos, ExportedType: target, Alias: alias}
	case "func":
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LPAREN)
		params := p.parseParamList(false)
		p.expect(lexer.TOKEN_RPAREN)

		var retType ast.TypeExpr
		if p.match(lexer.TOKEN_ARROW) {
			retType = p.parseTypeExpr()
		}

		p.expect(lexer.TOKEN_ASSIGN)
		targetName := p.expect(lexer.TOKEN_IDENT).Text
		var targetTypeArgs []ast.TypeExpr
		if p.match(lexer.TOKEN_LBRACKET) {
			targetTypeArgs = make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
			for {
				targetTypeArgs = append(targetTypeArgs, p.parseGenericTypeArgExpr())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expect(lexer.TOKEN_RBRACKET)
		}
		p.expectNewline()
		return &ast.ExportFuncDecl{Position: pos, Name: name, Params: params, ReturnType: retType, TargetName: targetName, TargetTypeArgs: targetTypeArgs}
	case "global":
		targetName := p.expect(lexer.TOKEN_IDENT).Text
		alias := targetName
		if p.match(lexer.TOKEN_AS) {
			alias = p.expect(lexer.TOKEN_IDENT).Text
		}
		p.expectNewline()
		return &ast.ExportGlobalDecl{Position: pos, TargetName: targetName, Alias: alias}
	default:
		p.errorf("expected export type, export func, or export global, got %q", kindText)
		p.skipNewlines()
		return nil
	}
}
