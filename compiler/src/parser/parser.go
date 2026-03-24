package parser

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

type Parser struct {
	tokens     []lexer.Token
	pos        int
	errors     []string
	poolScopes []string
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens}
}

func (p *Parser) Errors() []string { return p.errors }

func (p *Parser) activePoolName() string {
	if len(p.poolScopes) == 0 {
		return ""
	}
	return p.poolScopes[len(p.poolScopes)-1]
}

func (p *Parser) cur() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Kind: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) peek() lexer.TokenKind {
	return p.cur().Kind
}

func (p *Parser) advance() lexer.Token {
	tok := p.cur()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *Parser) expect(kind lexer.TokenKind) lexer.Token {
	tok := p.cur()
	if tok.Kind != kind {
		p.errorf("expected %s, got %s", lexer.TokenName(kind), tok)
	}
	return p.advance()
}

func (p *Parser) match(kind lexer.TokenKind) bool {
	if p.peek() == kind {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) peekIdentText(text string) bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == text
}

func (p *Parser) matchIdentText(text string) bool {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == text {
		p.advance()
		return true
	}
	return false
}

func (p *Parser) expectIdentText(text string) lexer.Token {
	tok := p.expect(lexer.TOKEN_IDENT)
	if tok.Text != text {
		p.errorf("expected %q, got %q", text, tok.Text)
	}
	return tok
}

func (p *Parser) errorf(format string, args ...interface{}) {
	pos := p.cur().Pos
	msg := fmt.Sprintf("%s:%d:%d: %s", pos.File, pos.Line, pos.Col, fmt.Sprintf(format, args...))
	p.errors = append(p.errors, msg)
}

func (p *Parser) skipNewlines() {
	for p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
}

// ---------- Top-level parsing ----------

func (p *Parser) ParseFile(filename string) *ast.File {
	file := &ast.File{Filename: filename}
	p.skipNewlines()
	for p.peek() != lexer.TOKEN_EOF {
		decl := p.parseDecl()
		if decl != nil {
			file.Decls = append(file.Decls, decl)
		}
		p.skipNewlines()
	}
	return file
}

func (p *Parser) parseDecl() ast.Decl {
	if p.peekIdentText("permission") {
		return p.parsePermissionDecl()
	}
	if p.peekIdentText("affine") {
		return p.parseStructDecl()
	}
	if p.peek() == lexer.TOKEN_AT {
		annotations := p.parseFuncAnnotations()
		switch p.peek() {
		case lexer.TOKEN_DEF:
			return p.parseFuncDeclWithAnnotations(annotations)
		case lexer.TOKEN_EXTERN:
			return p.parseExternDeclWithAnnotations(annotations)
		case lexer.TOKEN_PACKED:
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
				return p.parsePackedEnumDeclWithAnnotations(annotations)
			}
			p.errorf("declaration annotations must be followed by def, extern, enum, or packed enum, got %s", p.cur())
			return nil
		case lexer.TOKEN_ENUM:
			return p.parseEnumDeclWithAnnotations(annotations)
		default:
			p.errorf("declaration annotations must be followed by def, extern, enum, or packed enum, got %s", p.cur())
			return nil
		}
	}
	if p.peek() == lexer.TOKEN_PACKED && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
		return p.parsePackedEnumDecl()
	}
	switch p.peek() {
	case lexer.TOKEN_CONST:
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
			return p.parseConstEnumDecl()
		}
		return p.parseConstDecl()
	case lexer.TOKEN_ERROR:
		return p.parseErrorDecl()
	case lexer.TOKEN_ENUM:
		return p.parseEnumDecl()
	case lexer.TOKEN_GLOBAL:
		return p.parseGlobalDecl()
	case lexer.TOKEN_REPR:
		return p.parseStructDecl()
	case lexer.TOKEN_STRUCT:
		return p.parseStructDecl()
	case lexer.TOKEN_DEF:
		return p.parseFuncDecl()
	case lexer.TOKEN_EXTERN:
		return p.parseExternDecl()
	case lexer.TOKEN_EXPORT:
		return p.parseExportDecl()
	case lexer.TOKEN_STATIC:
		return p.parseStaticIfDecl()
	default:
		p.errorf("unexpected token %s at top level", p.cur())
		p.advance()
		return nil
	}
}

func (p *Parser) parseFuncAnnotations() []ast.Annotation {
	annotations := make([]ast.Annotation, 0, 1)
	for p.peek() == lexer.TOKEN_AT {
		pos := p.cur().Pos
		p.advance()
		name := p.expect(lexer.TOKEN_IDENT).Text
		var args []string
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseAnnotationArg())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		annotations = append(annotations, ast.Annotation{Position: pos, Name: name, Args: args})
		p.skipNewlines()
	}
	return annotations
}

func (p *Parser) parseAnnotationArg() string {
	root := p.expect(lexer.TOKEN_IDENT).Text
	var b strings.Builder
	b.WriteString(root)
	for {
		switch p.peek() {
		case lexer.TOKEN_DOT:
			p.advance()
			b.WriteByte('.')
			b.WriteString(p.expect(lexer.TOKEN_IDENT).Text)
		case lexer.TOKEN_LBRACKET:
			p.advance()
			b.WriteByte('[')
			switch p.peek() {
			case lexer.TOKEN_STAR:
				p.advance()
				b.WriteByte('*')
			case lexer.TOKEN_INT_LIT:
				b.WriteString(p.advance().Text)
			default:
				p.errorf("expected * or integer index in annotation path, got %s", p.cur())
			}
			p.expect(lexer.TOKEN_RBRACKET)
			b.WriteByte(']')
		default:
			return b.String()
		}
	}
}

func (p *Parser) parsePackedEnumDecl() *ast.EnumDecl {
	return p.parsePackedEnumDeclWithAnnotations(nil)
}

func (p *Parser) parsePackedEnumDeclWithAnnotations(annotations []ast.Annotation) *ast.EnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PACKED)
	p.expect(lexer.TOKEN_ENUM)
	return p.parseEnumDeclRest(pos, true, annotations)
}

func (p *Parser) parseErrorDecl() *ast.ErrorDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ERROR)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var tags []string
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		tags = append(tags, p.expect(lexer.TOKEN_IDENT).Text)
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.ErrorDecl{Position: pos, Name: name, Tags: tags}
}

func (p *Parser) parsePermissionDecl() *ast.PermissionDecl {
	pos := p.cur().Pos
	p.expectIdentText("permission")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]string, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.PermissionDecl{Position: pos, Name: name, Members: members}
}

func (p *Parser) parseEnumDecl() *ast.EnumDecl {
	return p.parseEnumDeclWithAnnotations(nil)
}

func (p *Parser) parseEnumDeclWithAnnotations(annotations []ast.Annotation) *ast.EnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ENUM)
	return p.parseEnumDeclRest(pos, false, annotations)
}

func (p *Parser) parseEnumDeclRest(pos lexer.Pos, packed bool, annotations []ast.Annotation) *ast.EnumDecl {
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	commonFields := make([]ast.FieldDecl, 0)
	variants := make([]ast.EnumVariantDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			commonFields = append(commonFields, p.parseEnumCommonFields()...)
			continue
		}
		variants = append(variants, p.parseEnumVariantDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Common: commonFields, Variants: variants}
}

func (p *Parser) parseEnumCommonFields() []ast.FieldDecl {
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return fields
}

func (p *Parser) parseEnumVariantDecl() ast.EnumVariantDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	payload := make([]ast.EnumPayloadDecl, 0)
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				payload = append(payload, p.parseEnumPayloadDecl())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expectNewline()
	return ast.EnumVariantDecl{Position: pos, Name: name, Payload: payload}
}

func (p *Parser) parseEnumPayloadDecl() ast.EnumPayloadDecl {
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		typ := p.parseTypeExpr()
		return ast.EnumPayloadDecl{Position: pos, Name: name, Type: typ}
	}
	typ := p.parseTypeExpr()
	return ast.EnumPayloadDecl{Position: typ.Pos(), Type: typ}
}

func (p *Parser) parseConstDecl() *ast.ConstDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_COLON) {
		typ = p.parseTypeExpr()
	}

	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseExpr()
	p.expectNewline()

	return &ast.ConstDecl{Position: pos, Name: name, Type: typ, Value: value}
}

func (p *Parser) parseConstEnumDecl() *ast.ConstEnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	p.expect(lexer.TOKEN_ENUM)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("of")
	storage := p.parseTypeExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]ast.ConstEnumMemberDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		members = append(members, p.parseConstEnumMemberDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.ConstEnumDecl{Position: pos, Name: name, Storage: storage, Members: members}
}

func (p *Parser) parseConstEnumMemberDecl() ast.ConstEnumMemberDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var value ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		value = p.parseExpr()
	}
	p.expectNewline()
	return ast.ConstEnumMemberDecl{Position: pos, Name: name, Value: value}
}

func (p *Parser) parseGlobalDecl() *ast.GlobalDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_GLOBAL)
	mutable := p.match(lexer.TOKEN_MUTABLE)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()

	var value ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		value = p.parseExpr()
	}
	p.expectNewline()

	return &ast.GlobalDecl{Position: pos, Mutable: mutable, Name: name, Type: typ, Value: value}
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	pos := p.cur().Pos
	affine := p.matchIdentText("affine")
	reprC := true
	if p.peek() == lexer.TOKEN_REPR {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		p.expect(lexer.TOKEN_IDENT) // "c"
		p.expect(lexer.TOKEN_RPAREN)
		reprC = true
	}
	p.expect(lexer.TOKEN_STRUCT)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typeParams []string
	var refStorageParams []string
	var refStateParams []string
	var genericParams []ast.GenericParam
	hasStateParam := false
	stateParamCount := 0
	if states, ok := p.peekAggregateStateBracketList(); ok {
		states = p.parseAggregateStateBracketList()
		for _, state := range states {
			if state != ast.RefStateNullable {
				p.errorf("struct state parameter declaration must use only [?] placeholders, got [%s]", joinAggregateStateMarkers(states))
				break
			}
		}
		hasStateParam = len(states) > 0
		stateParamCount = len(states)
	} else if p.match(lexer.TOKEN_LBRACKET) {
		typeParams, refStorageParams, refStateParams, _, _, genericParams = p.parseGenericParamListAfterLBracket(false, false)
		p.expect(lexer.TOKEN_RBRACKET)
		if states, ok := p.peekAggregateStateBracketList(); ok {
			states = p.parseAggregateStateBracketList()
			for _, state := range states {
				if state != ast.RefStateNullable {
					p.errorf("struct state parameter declaration must use only [?] placeholders, got [%s]", joinAggregateStateMarkers(states))
					break
				}
			}
			hasStateParam = len(states) > 0
			stateParamCount = len(states)
		}
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var fields []ast.FieldDecl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, Affine: affine, ReprC: reprC, Fields: fields}
}

func joinAggregateStateMarkers(states []ast.RefState) string {
	if len(states) == 0 {
		return ""
	}
	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, ast.RefStateMarker(state))
	}
	return strings.Join(parts, ", ")
}

func (p *Parser) parseFieldDecl() ast.FieldDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)

	mutable := false
	isTail := false

	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	if p.match(lexer.TOKEN_TAIL) {
		isTail = true
	}

	typ := p.parseTypeExpr()
	p.expectNewline()

	return ast.FieldDecl{Position: pos, Name: name, Mutable: mutable, IsTail: isTail, Type: typ}
}

func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	return p.parseFuncDeclWithAnnotations(nil)
}

func (p *Parser) parseFuncGenericParams() ([]string, []string, []string, []string, []string, []ast.GenericParam) {
	if !p.match(lexer.TOKEN_LBRACKET) {
		return nil, nil, nil, nil, nil, nil
	}
	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseGenericParamListAfterLBracket(true, true)
	p.expect(lexer.TOKEN_RBRACKET)
	return typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams
}

func (p *Parser) parseGenericParamListAfterLBracket(allowRegion bool, allowPermission bool) ([]string, []string, []string, []string, []string, []ast.GenericParam) {
	typeParams := make([]string, 0)
	refStorageParams := make([]string, 0)
	refStateParams := make([]string, 0)
	regionParams := make([]string, 0)
	permissionParams := make([]string, 0)
	genericParams := make([]ast.GenericParam, 0)
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
			if seenType[name] || seenRegion[name] || seenPermission[name] || seenRefStorage[name] || seenRefState[name] {
				p.errorf("duplicate function generic parameter %q", name)
			} else {
				seenType[name] = true
				typeParams = append(typeParams, name)
				genericParams = append(genericParams, ast.GenericParam{Position: paramPos, Kind: kind, Name: name})
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

func (p *Parser) parsePermissionRefs(bracketed bool) []ast.PermissionRef {
	if bracketed {
		p.expect(lexer.TOKEN_LBRACKET)
	} else if p.match(lexer.TOKEN_LBRACKET) {
		bracketed = true
	}
	refs := make([]ast.PermissionRef, 0)
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

func (p *Parser) parseFuncDeclWithAnnotations(annotations []ast.Annotation) *ast.FuncDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DEF)
	name := p.expect(lexer.TOKEN_IDENT).Text

	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()

	p.expect(lexer.TOKEN_LPAREN)
	params := p.parseParamList()
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	body := p.parseBlock()
	return &ast.FuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, RegionParams: regionParams, PermissionParams: permissionParams, GenericParams: genericParams, Permissions: permissions, Params: params, ReturnType: retType, Body: body}
}

func (p *Parser) parseParamList() []ast.ParamDecl {
	var params []ast.ParamDecl
	if p.peek() == lexer.TOKEN_RPAREN {
		return params
	}
	for {
		params = append(params, p.parseParam())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return params
}

func (p *Parser) parseParam() ast.ParamDecl {
	pos := p.cur().Pos
	mutable := false
	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()
	return ast.ParamDecl{Position: pos, Name: name, Mutable: mutable, Type: typ}
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
			p.errorf("function annotations on extern declarations require an extern function, got extern type %q", name)
		}
		p.expectNewline()
		return &ast.ExternTypeDecl{Position: pos, Name: name}
	}

	// extern name: Type  (variable)
	if p.peek() == lexer.TOKEN_COLON {
		if len(annotations) != 0 {
			p.errorf("function annotations on extern declarations require an extern function, got extern var %q", name)
		}
		p.advance()
		typ := p.parseTypeExpr()
		p.expectNewline()
		return &ast.ExternVarDecl{Position: pos, Name: name, Type: typ}
	}

	typeParams, _, _, regionParams, permissionParams, _ := p.parseFuncGenericParams()
	if len(typeParams) > 0 {
		p.errorf("extern functions do not support type parameters yet")
	}
	if len(permissionParams) > 0 {
		p.errorf("extern functions do not support permission parameters yet")
	}

	// extern name(params...) [-> RetType]  (function)
	p.expect(lexer.TOKEN_LPAREN)
	var params []ast.ParamDecl
	variadic := false
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			if p.peek() == lexer.TOKEN_ELLIPSIS {
				p.advance()
				variadic = true
				break
			}
			params = append(params, p.parseParam())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}
	p.expectNewline()

	return &ast.ExternFuncDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, RegionParams: regionParams, Permissions: permissions, Params: params, ReturnType: retType, Variadic: variadic}
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
		params := p.parseParamList()
		p.expect(lexer.TOKEN_RPAREN)

		var retType ast.TypeExpr
		if p.match(lexer.TOKEN_ARROW) {
			retType = p.parseTypeExpr()
		}

		p.expect(lexer.TOKEN_ASSIGN)
		targetName := p.expect(lexer.TOKEN_IDENT).Text
		var targetTypeArgs []ast.TypeExpr
		if p.match(lexer.TOKEN_LBRACKET) {
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

func (p *Parser) parseStaticIfDecl() *ast.StaticIfDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	thenBlock := p.parseDeclBlock()

	var elifs []ast.StaticElifDecl
	var elseBlock []ast.Decl

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseDeclBlock()
			elifs = append(elifs, ast.StaticElifDecl{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseDeclBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfDecl{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseDeclBlock() []ast.Decl {
	p.expect(lexer.TOKEN_INDENT)
	var decls []ast.Decl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		decl := p.parseDecl()
		if decl != nil {
			decls = append(decls, decl)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decls
}
