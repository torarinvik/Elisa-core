package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// liftLeadingContracts pulls leading `requires`/`ensure` value-contract statements (parsed as
// ContractStmt) off the front of a function body into precondition/postcondition lists, returning
// the remaining body. Only the leading run is honoured; a later stray ContractStmt is left in place
// so the analyzer reports the misplacement rather than silently dropping it.
func liftLeadingContracts(body []ast.Stmt) (requires []ast.Expr, requireProofs []*ast.ProofBlockStmt, ensures []ast.Expr, ensureProofs []*ast.ProofBlockStmt, decreases []ast.Expr, decreasesWild string, uses []*ast.ContractStmt, rest []ast.Stmt) {
	i := 0
	for i < len(body) {
		cs, ok := body[i].(*ast.ContractStmt)
		if !ok {
			break
		}
		switch cs.Kind {
		case ast.ContractUses:
			uses = append(uses, cs)
		case ast.ContractRequire:
			if cs.Cond != nil {
				requires = append(requires, cs.Cond)
				requireProofs = append(requireProofs, contractProofBlock(cs))
			}
		case ast.ContractEnsure:
			if cs.Cond != nil {
				ensures = append(ensures, cs.Cond)
				ensureProofs = append(ensureProofs, contractProofBlock(cs))
			}
		case ast.ContractDecreases:
			if cs.Cond != nil {
				decreases = append(decreases, cs.Cond)
			}
		case ast.ContractDecreasesWild:
			decreases = nil // wildcard replaces any prior measure clauses
			if cs.WildReason != "" {
				decreasesWild = cs.WildReason
			} else {
				// Missing reason: use the sentinel "*" so the analyzer can diagnose the error
				// while still knowing the wildcard clause was present. Non-empty = clause seen.
				decreasesWild = "*"
			}
		default:
			break
		}
		i++
	}
	if i == 0 {
		return nil, nil, nil, nil, nil, "", nil, body
	}
	return requires, requireProofs, ensures, ensureProofs, decreases, decreasesWild, uses, body[i:]
}

func contractProofBlock(cs *ast.ContractStmt) *ast.ProofBlockStmt {
	if cs == nil || !cs.ScopedProof || cs.Cond == nil {
		return nil
	}
	return &ast.ProofBlockStmt{Position: cs.Position, Goal: cs.Cond, Proof: cs.Proof}
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
	// `extern resource Name` (docs/127 §3.2): an opaque native handle that Elisa OWNS.
	// Desugars to the handle type `Name__native` plus a one-field struct `Name` wrapping it,
	// so the existing affine/`__drop__` machinery gives moves, scope-exit release and
	// use-after-release for free; the extern boundary passes the handle in place of the
	// struct (backend/llvm_extern_resource_abi.go). The struct lands at file scope via
	// pendingDecls, like the protocol and machine sugars.
	// `extern enum Name of T:` (docs/127 D9) — a C enum's member list, whose values must be
	// validated on the way in so `match` exhaustiveness is sound. See parser_extern_enum.go.
	if p.peek() == lexer.TOKEN_ENUM {
		return p.parseExternEnumDecl(pos, annotations)
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "resource" && p.peekAt(1) == lexer.TOKEN_IDENT {
		p.advance()
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expectNewline()
		handleName := name + "__native"
		p.pendingDecls = append(p.pendingDecls, &ast.StructDecl{Position: pos, Name: name, Resource: true, Fields: []ast.FieldDecl{{Position: pos, Name: "__handle", Type: &ast.NamedType{Position: pos, Name: handleName, Spelling: handleName}}}})
		return &ast.ExternTypeDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: handleName}
	}
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
	// Boundary preconditions: `extern f(...) -> T requires <bool>[, <bool>]` — checked at calls.
	var requires []ast.Expr
	var uses []*ast.ContractStmt
	if p.matchIdentText("requires") || p.matchIdentText("require") {
		saved := p.allowQuantifiers
		p.allowQuantifiers = true
		requires = append(requires, p.parseExpr())
		for p.match(lexer.TOKEN_COMMA) {
			requires = append(requires, p.parseExpr())
		}
		p.allowQuantifiers = saved
	}
	for p.matchIdentText("uses") {
		uses = append(uses, p.parseUsesContractAfterKeyword())
	}
	var ensureValues []ast.Expr
	if p.matchIdentText("ensure") {
		saved := p.allowQuantifiers
		p.allowQuantifiers = true
		ensureValues = append(ensureValues, p.parseExpr())
		for p.match(lexer.TOKEN_COMMA) {
			ensureValues = append(ensureValues, p.parseExpr())
		}
		p.allowQuantifiers = saved
	}
	var ensures []ast.EnsuresClause
	if p.matchIdentText("ensures") {
		ensures = p.parseEnsuresClausesAfterKeyword()
	}
	var changes, preserves []ast.EnsuresPath
	if p.matchIdentText("changes") {
		changes = p.parseChangesPathsAfterKeyword()
	}
	if p.matchIdentText("preserves") {
		preserves = p.parseChangesPathsAfterKeyword()
	}
	var fulfills []ast.FulfillsClause
	if p.matchIdentText("fulfills") {
		fulfills = p.parseFulfillsClausesAfterKeyword()
	}
	p.expectNewline()

	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, PermissionParams: permissionParams, GenericParams: genericParams, RegionParams: regionParams, Permissions: permissions, Requires: requires, EnsureValues: ensureValues, Uses: uses, Ensures: ensures, Changes: changes, Preserves: preserves, Fulfills: fulfills, Params: params, ReturnType: retType, Variadic: variadic}
}

func (p *Parser) parseUsesContractAfterKeyword() *ast.ContractStmt {
	pos := p.cur().Pos
	cname := p.expect(lexer.TOKEN_IDENT).Text
	var typeArgs []ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typeArgs = make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
		for {
			typeArgs = append(typeArgs, p.parseGenericTypeArgExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	var args []ast.Expr
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			args = append(args, p.parseExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.ContractStmt{Position: pos, Kind: ast.ContractUses, UsesName: cname, UsesTypeArgs: typeArgs, UsesArgs: args}
}
func (p *Parser) parseExportDecl() ast.Decl {
	return p.parseExportDeclWithAnnotations(nil)
}

func (p *Parser) parseExportDeclWithAnnotations(annotations []ast.Annotation) ast.Decl {
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
	case "func", "fn":
		if kindText == "func" {
			// `export func NAME …` is now `export fn NAME …` (the `func` keyword is
			// renamed to `fn` everywhere). Directed error; recover by parsing as usual.
			p.errorf("`export func` has been renamed to `export fn`")
		}
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LPAREN)
		params := p.parseParamList(false)
		p.expect(lexer.TOKEN_RPAREN)

		var retType ast.TypeExpr
		if p.match(lexer.TOKEN_ARROW) {
			retType = p.parseTypeExpr()
		}

		p.expect(lexer.TOKEN_ASSIGN)
		// The target may be `::`-qualified (`Mod::fn`); store it as the internal
		// dotted name so lookupVisibleGlobal resolves it like any module member.
		targetName := p.parseQualifiedIdentNameAfterFirst(p.expect(lexer.TOKEN_IDENT).Text)
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
		return &ast.ExportFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Params: params, ReturnType: retType, TargetName: targetName, TargetTypeArgs: targetTypeArgs}
	case "global":
		// A `::`-qualified target (`Mod::g`) stores as the internal dotted name; the
		// default public alias is its last segment, since a dotted name is not a
		// valid C export symbol. An explicit `as` overrides it.
		targetName := p.parseQualifiedIdentNameAfterFirst(p.expect(lexer.TOKEN_IDENT).Text)
		alias := targetName
		if idx := strings.LastIndex(alias, "."); idx >= 0 {
			alias = alias[idx+1:]
		}
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
