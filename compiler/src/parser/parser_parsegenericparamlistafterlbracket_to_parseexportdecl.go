package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseGenericParamListAfterLBracket(allowRegion bool, allowPermission bool) ([]string, []string, []string, []string, []string, []ast.GenericParam) {
	paramCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET)
	typeParams := make([]string, 0, paramCapacity)
	refStorageParams := make([]string, 0, paramCapacity)
	refStateParams := make([]string, 0, paramCapacity)
	regionParams := make([]string, 0, paramCapacity)
	permissionParams := make([]string, 0, paramCapacity)
	genericParams := make([]ast.GenericParam, 0, paramCapacity)
	seenType := map[string]bool{}
	seenRefStorage := map[string]bool{}
	seenRefState := map[string]bool{}
	seenRegion := map[string]bool{}
	seenPermission := map[string]bool{}
	for {
		paramPos := p.cur().Pos
		kind := ast.GenericParamType
		isRegionParam := allowRegion && p.match(lexer.TOKEN_REGION)
		if !isRegionParam && allowRegion && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "region" {
			p.advance()
			isRegionParam = true
		}
		isPermissionParam := false
		if !isRegionParam && allowPermission && p.matchIdentText("permission") {
			isPermissionParam = true
		}
		isRefStorageParam := false
		if !isRegionParam && !isPermissionParam && p.matchIdentText("refstorage") {
			isRefStorageParam = true
		}
		isRefStateParam := false
		if !isRegionParam && !isPermissionParam && !isRefStorageParam && p.matchIdentText("refstate") {
			isRefStateParam = true
		}
		if isRegionParam {
			kind = ast.GenericParamRegion
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenRegion[name] = true
				regionParams = append(regionParams, name)
			}
		} else if isRefStorageParam {
			kind = ast.GenericParamRefStorage
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenRefStorage[name] = true
				refStorageParams = append(refStorageParams, name)
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: kind, Name: name})
			}
		} else if isRefStateParam {
			kind = ast.GenericParamRefState
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenRefState[name] = true
				refStateParams = append(refStateParams, name)
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: kind, Name: name})
			}
		} else if isPermissionParam {
			kind = ast.GenericParamPermission
			name := p.expect(lexer.TOKEN_IDENT).Text
			if seenRegion[name] || seenType[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
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
			if seenType[name] || seenRegion[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
				p.errorf("duplicate function generic parameter %q", name)
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
	return typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams
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
func (p *Parser) parseEffectsSpec() (*ast.ErrorSetExpr, []ast.PermissionRef) {
	var errorEffects *ast.ErrorSetExpr
	var permissions []ast.PermissionRef
	switch {
	case p.peek() == lexer.TOKEN_ERROR:
		errorEffects = p.parseErrorSetExpr()
		if p.matchIdentText("can") {
			permissions = p.parsePermissionRefs(true)
		}
	case p.peekIdentText("can"):
		p.advance()
		permissions = p.parsePermissionRefs(true)
		if p.peek() == lexer.TOKEN_ERROR {
			errorEffects = p.parseErrorSetExpr()
		}
	default:
		p.errorf("effects declaration requires error[...] and/or can[...]")
	}
	return errorEffects, permissions
}
func (p *Parser) parseSignatureEffectsClause() []ast.SignatureEffectItem {
	p.expect(lexer.TOKEN_LBRACKET)
	items := make([]ast.SignatureEffectItem, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
		if p.peek() == lexer.TOKEN_ERROR {
			items = append(items, ast.SignatureEffectItem{Position: p.cur().Pos, ErrorEffects: p.parseSignatureEffectErrorSet()})
		} else {
			items = append(items, p.parseSignatureEffectNameItem())
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return items
}
func (p *Parser) parseSignatureEffectErrorSet() *ast.ErrorSetExpr {
	if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET {
		return p.parseErrorSetExpr()
	}
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ERROR)
	return &ast.ErrorSetExpr{Position: pos, Tags: []ast.ErrorTagExpr{p.parseErrorSetItem()}}
}
func (p *Parser) parseSignatureEffectNameItem() ast.SignatureEffectItem {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_DOT) {
		member := p.expect(lexer.TOKEN_IDENT).Text
		ref := ast.PermissionRef{Position: pos, Name: name, Member: member}
		return ast.SignatureEffectItem{Position: pos, Permission: &ref}
	}
	return ast.SignatureEffectItem{Position: pos, Alias: name}
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
		refs = append(refs, p.parsePermissionRef())
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

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	body := p.parseBlock()
	return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Effects: effects, Permissions: permissions, Ensures: ensures, Params: params, ParamPacks: paramPacks, ParamItemOrder: paramItemOrder, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, ReturnType: retType, Body: body}
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
func (p *Parser) parseExplicitSignatureParamList(allowDefault bool, allowVariadic bool) ([]ast.ParamDecl, []ast.ParamPackUse, []ast.ParamSigItem, bool) {
	params := make([]ast.ParamDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	packs := make([]ast.ParamPackUse, 0, 1)
	items := make([]ast.ParamSigItem, 0, cap(params))
	variadic := false
	if p.peek() == lexer.TOKEN_RPAREN {
		return params, nil, nil, false
	}
	for {
		if allowVariadic && p.peek() == lexer.TOKEN_ELLIPSIS {
			p.advance()
			variadic = true
			break
		}
		if p.matchIdentText("use") {
			pos := p.tokens[p.pos-1].Pos
			pack := ast.ParamPackUse{Position: pos, Name: p.parseQualifiedDeclName()}
			packs = append(packs, pack)
			items = append(items, ast.ParamSigItem{Position: pos, Pack: pack, IsPack: true})
		} else {
			param := p.parseParam(allowDefault)
			params = append(params, param)
			items = append(items, ast.ParamSigItem{Position: param.Position, Param: param})
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	if len(packs) == 0 {
		items = nil
	}
	return params, packs, items, variadic
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
func (p *Parser) lookaheadParamDecl() bool {
	i := p.pos
	if i >= len(p.tokens) {
		return false
	}
	if p.tokens[i].Kind == lexer.TOKEN_MUTABLE {
		i++
	}
	if i >= len(p.tokens) || p.tokens[i].Kind != lexer.TOKEN_IDENT {
		return false
	}
	i++
	return i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_COLON
}
func (p *Parser) parseWithSignatureClause() ([]ast.ParamDecl, []string, []ast.ImplicitSigItem) {
	p.expect(lexer.TOKEN_WITH)
	implicitParams := make([]ast.ParamDecl, 0, 2)
	implicitBundles := make([]string, 0, 2)
	implicitItemOrder := make([]ast.ImplicitSigItem, 0, 2)
	for {
		if p.lookaheadParamDecl() {
			param := p.parseParam(false)
			implicitParams = append(implicitParams, param)
			implicitItemOrder = append(implicitItemOrder, ast.ImplicitSigItem{Position: param.Position, Param: param})
		} else {
			namePos := p.cur().Pos
			bundle := p.parseQualifiedDeclName()
			implicitBundles = append(implicitBundles, bundle)
			implicitItemOrder = append(implicitItemOrder, ast.ImplicitSigItem{Position: namePos, Bundle: bundle, IsBundle: true})
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return implicitParams, implicitBundles, implicitItemOrder
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
		if len(annotations) != 0 {
			p.errorf("annotations on extern declarations require an extern function or extern var, got extern type %q", name)
		}
		p.expectNewline()
		return &ast.ExternTypeDecl{Position: pos, Name: name}
	}

	// extern name: Type  (variable)
	if p.peek() == lexer.TOKEN_COLON {
		p.advance()
		typ := p.parseTypeExpr()
		p.expectNewline()
		return &ast.ExternVarDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Type: typ}
	}

	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	// extern name(params...) [-> RetType]  (function)
	p.expect(lexer.TOKEN_LPAREN)
	params, paramPacks, paramItemOrder, variadic := p.parseExplicitSignatureParamList(true, true)
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

	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, PermissionParams: permissionParams, GenericParams: genericParams, RegionParams: regionParams, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Effects: effects, Permissions: permissions, Ensures: ensures, Params: params, ParamPacks: paramPacks, ParamItemOrder: paramItemOrder, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, ReturnType: retType, Variadic: variadic}
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
