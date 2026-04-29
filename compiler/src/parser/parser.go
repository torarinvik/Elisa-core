package parser

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

type Parser struct {
	tokens            []lexer.Token
	pos               int
	errors            []string
	poolScopes        []string
	allowAsCast       bool
	allowInMembership bool
	allowTernary      bool
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens, allowAsCast: true, allowInMembership: true, allowTernary: true}
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

func (p *Parser) withAsCastDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowAsCast
	p.allowAsCast = false
	defer func() {
		p.allowAsCast = saved
	}()
	return parse()
}

func (p *Parser) withInMembershipDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowInMembership
	p.allowInMembership = false
	defer func() {
		p.allowInMembership = saved
	}()
	return parse()
}

func (p *Parser) withTernaryDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowTernary
	p.allowTernary = false
	defer func() {
		p.allowTernary = saved
	}()
	return parse()
}

func tokenCanStartTypeExpr(tok lexer.Token) bool {
	switch tok.Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_TAIL,
		lexer.TOKEN_LPAREN, lexer.TOKEN_HEAP,
		lexer.TOKEN_STACK, lexer.TOKEN_STATIC:
		return true
	default:
		return false
	}
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
	p.errorAt(pos, format, args...)
}

func (p *Parser) errorAt(pos lexer.Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf("%s: %s", pos, fmt.Sprintf(format, args...))
	p.errors = append(p.errors, msg)
}

func (p *Parser) skipNewlines() {
	for p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
}

// ---------- Top-level parsing ----------

func (p *Parser) ParseFile(filename string) *ast.File {
	file := &ast.File{Filename: filename, Decls: make([]ast.Decl, 0, p.estimateTopLevelItemCount())}
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
	if p.peekIdentText("protocol") {
		return p.parseInterfaceDecl()
	}
	if p.peekIdentText("interface") {
		return p.parseInterfaceDecl()
	}
	if p.peek() == lexer.TOKEN_STATIC && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "interface" {
		return p.parseInterfaceDecl()
	}
	if p.peekIdentText("impl") {
		return p.parseImplDecl()
	}
	if p.peekIdentText("permission") {
		return p.parsePermissionDecl()
	}
	if p.peekIdentText("effectalias") {
		return p.parseEffectsDecl()
	}
	if p.peekIdentText("effect") {
		return p.parseEffectDecl()
	}
	if p.peekIdentText("effects") {
		return p.parseEffectsDecl()
	}
	if p.peekIdentText("params") {
		return p.parseParamsDecl()
	}
	if p.peekIdentText("bundle") {
		return p.parseBundleDecl()
	}
	if p.peek() == lexer.TOKEN_CONTEXT {
		return p.parseContextDecl()
	}
	if p.peekIdentText("namespace") {
		return p.parseNamespaceDecl()
	}
	if p.peekIdentText("using") {
		return p.parseUsingDecl()
	}
	if p.peekIdentText("type") {
		return p.parseTypeAliasDecl()
	}
	if p.peekIdentText("attribute") {
		return p.parseAttributeDecl()
	}
	if p.peekIdentText("grammar") {
		return p.parseGrammarDecl()
	}
	if p.peekIdentText("grammarenv") {
		return p.parseGrammarEnvDecl()
	}
	if p.peekIdentText("lexer") {
		return p.parseLexerDecl()
	}
	if p.peekIdentText("extend") {
		return p.parseGrammarDecl()
	}
	if p.peekIdentText("tree") {
		return p.parseTreeDecl()
	}
	if p.peekIdentText("store") {
		return p.parseStoreDecl()
	}
	if p.peekIdentText("affine") {
		return p.parseStructDecl()
	}
	if p.peek() == lexer.TOKEN_AT {
		annotations := p.parseFuncAnnotations()
		if p.peekIdentText("impl") {
			return p.parseImplDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("tree") {
			return p.parseTreeDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("store") {
			return p.parseStoreDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("affine") {
			return p.parseStructDeclWithAnnotations(annotations)
		}
		switch p.peek() {
		case lexer.TOKEN_DEF:
			return p.parseFuncDeclWithAnnotations(annotations)
		case lexer.TOKEN_EXTERN:
			return p.parseExternDeclWithAnnotations(annotations)
		case lexer.TOKEN_REPR:
			return p.parseStructDeclWithAnnotations(annotations)
		case lexer.TOKEN_PACKED:
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
				return p.parsePackedEnumDeclWithAnnotations(annotations)
			}
			p.errorf("declaration annotations must be followed by def, extern, struct, store, tree, enum, packed enum, or impl, got %s", p.cur())
			return nil
		case lexer.TOKEN_ENUM:
			return p.parseEnumDeclWithAnnotations(annotations)
		case lexer.TOKEN_STRUCT:
			return p.parseStructDeclWithAnnotations(annotations)
		default:
			p.errorf("declaration annotations must be followed by def, extern, struct, store, tree, enum, packed enum, or impl, got %s", p.cur())
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
	case lexer.TOKEN_CONTEXT:
		return p.parseContextDecl()
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

func (p *Parser) parseStoreDecl() *ast.StoreDecl {
	return p.parseStoreDeclWithAnnotations(nil)
}

func (p *Parser) parseTypeAliasDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	target := p.parseTypeExpr()
	p.expectNewline()
	return &ast.TypeAliasDecl{Position: pos, Name: name, Target: target}
}

func (p *Parser) parseStoreDeclWithAnnotations(annotations []ast.Annotation) *ast.StoreDecl {
	pos := p.cur().Pos
	p.expectIdentText("store")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.StoreDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Fields: fields}
}

func (p *Parser) parseAnnotations() []ast.Annotation {
	annotations := make([]ast.Annotation, 0, 1)
	for p.peek() == lexer.TOKEN_AT {
		pos := p.cur().Pos
		p.advance()
		name := p.expect(lexer.TOKEN_IDENT).Text
		var args []string
		if p.match(lexer.TOKEN_LPAREN) {
			for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
				args = append(args, p.parseAnnotationArg())
				if p.match(lexer.TOKEN_COMMA) {
					continue
				}
				if !annotationArgCanStart(p.peek()) {
					break
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		annotations = append(annotations, ast.Annotation{Position: pos, Name: name, Args: args})
		p.skipNewlines()
	}
	return annotations
}

func (p *Parser) parseFuncAnnotations() []ast.Annotation {
	return p.parseAnnotations()
}

func annotationArgCanStart(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT, lexer.TOKEN_IDENT:
		return true
	default:
		return false
	}
}

func (p *Parser) parseAnnotationArg() string {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		if tok.Suffix != "" {
			return tok.Text + tok.Suffix
		}
		return tok.Text
	case lexer.TOKEN_IDENT:
		// handled below
	default:
		p.errorf("expected annotation argument, got %s", p.cur())
		return p.advance().Text
	}
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

func (p *Parser) parseEffectsDecl() *ast.EffectsDecl {
	pos := p.cur().Pos
	if p.peekIdentText("effectalias") {
		p.expectIdentText("effectalias")
	} else {
		p.expectIdentText("effects")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	errorEffects, permissions := p.parseEffectsSpec()
	p.expectNewline()
	return &ast.EffectsDecl{Position: pos, Name: name, ErrorEffects: errorEffects, Permissions: permissions}
}

func (p *Parser) parsePermissionDecl() *ast.PermissionDecl {
	pos := p.cur().Pos
	p.expectIdentText("permission")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]string, 0, p.estimateIndentedItemCount())
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

func (p *Parser) parseEffectDecl() *ast.EffectDecl {
	pos := p.cur().Pos
	p.expectIdentText("effect")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)

	members := make([]string, 0)
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			if p.peek() == lexer.TOKEN_PASS {
				p.advance()
				p.expectNewline()
				continue
			}
			members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
			p.expectNewline()
		}
		p.expect(lexer.TOKEN_DEDENT)
		return &ast.EffectDecl{Position: pos, Name: name, Members: members}
	}

	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.EffectDecl{Position: pos, Name: name, Members: members}
	}
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
		p.match(lexer.TOKEN_COMMA)
	}
	p.expectNewline()
	return &ast.EffectDecl{Position: pos, Name: name, Members: members}
}

func (p *Parser) parseContextDecl() *ast.ContextDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONTEXT)
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.ParamDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseParam(false))
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.ContextDecl{Position: pos, Name: name, Fields: fields}
}

func (p *Parser) parseParamsDecl() *ast.ParamsDecl {
	pos := p.cur().Pos
	p.expectIdentText("params")
	name := p.parseQualifiedDeclName()
	params := p.parseParamDeclBlock(true)
	return &ast.ParamsDecl{Position: pos, Name: name, Params: params}
}

func (p *Parser) parseBundleDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("bundle")
	name := p.parseQualifiedDeclName()
	mode := p.expect(lexer.TOKEN_IDENT).Text
	switch mode {
	case "implicit":
		fields := p.parseParamDeclBlock(false)
		return &ast.ContextDecl{Position: pos, Name: name, Fields: fields}
	case "explicit":
		params := p.parseParamDeclBlock(true)
		return &ast.ParamsDecl{Position: pos, Name: name, Params: params}
	default:
		p.errorf("expected bundle mode `implicit` or `explicit`, got %q", mode)
		params := p.parseParamDeclBlock(true)
		return &ast.ParamsDecl{Position: pos, Name: name, Params: params}
	}
}

func (p *Parser) parseParamDeclBlock(allowDefaults bool) []ast.ParamDecl {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	params := make([]ast.ParamDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		params = append(params, p.parseParam(allowDefaults))
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return params
}

func (p *Parser) parseQualifiedDeclName() string {
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.match(lexer.TOKEN_DOT) {
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}

func (p *Parser) parseNamespaceDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("namespace")
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseDeclBlock()
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls}
}

func (p *Parser) parseUsingDecl() *ast.UsingDecl {
	pos := p.cur().Pos
	p.expectIdentText("using")
	name := p.parseQualifiedDeclName()
	p.expectNewline()
	return &ast.UsingDecl{Position: pos, Name: name}
}

func (p *Parser) parseAttributeDecl() *ast.AttributeDecl {
	pos := p.cur().Pos
	p.expectIdentText("attribute")
	target := p.parseQualifiedDeclName()
	dot := strings.LastIndex(target, ".")
	var receiver ast.TypeExpr
	name := target
	if dot <= 0 || dot == len(target)-1 {
		p.errorf("attribute declaration expects receiver.name, got %q", target)
	} else {
		receiver = &ast.NamedType{Position: pos, Name: target[:dot]}
		name = target[dot+1:]
	}
	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	arms := p.parseVisitArms()
	return &ast.AttributeDecl{Position: pos, Receiver: receiver, Name: name, ReturnType: retType, Arms: arms}
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

	itemCapacity := p.estimateIndentedItemCount()
	commonFields := make([]ast.FieldDecl, 0, itemCapacity/2)
	variants := make([]ast.EnumVariantDecl, 0, itemCapacity)
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

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
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
	payload := make([]ast.EnumPayloadDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
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
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		relationText := p.tokens[p.pos].Text
		var relation ast.EnumPayloadRelation
		switch relationText {
		case string(ast.EnumPayloadRelationChild):
			relation = ast.EnumPayloadRelationChild
		case string(ast.EnumPayloadRelationChildren):
			relation = ast.EnumPayloadRelationChildren
		case string(ast.EnumPayloadRelationLink):
			relation = ast.EnumPayloadRelationLink
		}
		colonIndex := p.pos + 2
		optionalNamed := false
		if colonIndex < len(p.tokens) && p.tokens[colonIndex].Kind == lexer.TOKEN_QUESTION {
			optionalNamed = true
			colonIndex++
		}
		if relation != ast.EnumPayloadRelationNone && colonIndex < len(p.tokens) && p.tokens[colonIndex].Kind == lexer.TOKEN_COLON {
			pos := p.cur().Pos
			p.expect(lexer.TOKEN_IDENT)
			nameTok := p.expect(lexer.TOKEN_IDENT)
			if optionalNamed {
				p.expect(lexer.TOKEN_QUESTION)
			}
			p.expect(lexer.TOKEN_COLON)
			typ := p.parseTypeExpr()
			if optionalNamed {
				typ = &ast.OptionalTypeExpr{Position: nameTok.Pos, Value: typ}
			}
			return ast.EnumPayloadDecl{Position: pos, Relation: relation, Name: nameTok.Text, Type: typ}
		}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) {
		optionalNamed := p.tokens[p.pos+1].Kind == lexer.TOKEN_QUESTION && p.pos+2 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
		if p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON || optionalNamed {
			pos := p.cur().Pos
			nameTok := p.expect(lexer.TOKEN_IDENT)
			if optionalNamed {
				p.expect(lexer.TOKEN_QUESTION)
			}
			p.expect(lexer.TOKEN_COLON)
			typ := p.parseTypeExpr()
			if optionalNamed {
				typ = &ast.OptionalTypeExpr{Position: nameTok.Pos, Value: typ}
			}
			return ast.EnumPayloadDecl{Position: pos, Name: nameTok.Text, Type: typ}
		}
	}
	typ := p.parseTypeExpr()
	return ast.EnumPayloadDecl{Position: typ.Pos(), Type: typ}
}

func (p *Parser) parseTreeDecl() *ast.TreeDecl {
	return p.parseTreeDeclWithAnnotations(nil)
}

func (p *Parser) parseTreeDeclWithAnnotations(annotations []ast.Annotation) *ast.TreeDecl {
	pos := p.cur().Pos
	p.expectIdentText("tree")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	itemCapacity := p.estimateIndentedItemCount()
	commonFields := make([]ast.FieldDecl, 0, itemCapacity/2)
	members := make([]ast.TreeMemberDecl, 0, itemCapacity)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		memberAnnotations := p.parseAnnotations()
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			if len(memberAnnotations) != 0 {
				for _, annotation := range memberAnnotations {
					p.errorf("tree common: block does not support declaration annotation @%s", annotation.Name)
				}
			}
			commonFields = append(commonFields, p.parseTreeCommonFields()...)
			continue
		}
		if p.peekTreeMemberHeader("block") {
			members = append(members, p.parseTreeBlockDecl(memberAnnotations))
			continue
		}
		if p.peek() == lexer.TOKEN_STRUCT {
			members = append(members, p.parseTreeStructMemberDecl(memberAnnotations))
			continue
		}
		if p.peekTreeMemberHeader("node") {
			members = append(members, p.parseTreeCategoryDecl(memberAnnotations))
			continue
		}
		p.errorf("expected tree member declaration, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Common: commonFields, Members: members}
}

func (p *Parser) parseTreeCommonFields() []ast.FieldDecl {
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
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

func (p *Parser) peekTreeMemberHeader(keyword string) bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == keyword && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
}

func (p *Parser) parseTreeCategoryDecl(annotations []ast.Annotation) *ast.TreeCategoryDecl {
	pos := p.cur().Pos
	p.expectIdentText("node")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	variants := make([]ast.EnumVariantDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		variants = append(variants, p.parseEnumVariantDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeCategoryDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Variants: variants}
}

func (p *Parser) parseTreeBlockDecl(annotations []ast.Annotation) *ast.TreeBlockDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeBlockDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Fields: fields}
}

func (p *Parser) parseTreeStructMemberDecl(annotations []ast.Annotation) *ast.TreeStructDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STRUCT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeStructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Fields: fields}
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
	p.expectNewlineAfterValueExpr(value)

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

	members := make([]ast.ConstEnumMemberDecl, 0, p.estimateIndentedItemCount())
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
	p.expectNewlineAfterValueExpr(value)
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
	p.expectNewlineAfterValueExpr(value)

	return &ast.GlobalDecl{Position: pos, Mutable: mutable, Name: name, Type: typ, Value: value}
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	return p.parseStructDeclWithAnnotations(nil)
}

func (p *Parser) parseStructDeclWithAnnotations(annotations []ast.Annotation) *ast.StructDecl {
	pos := p.cur().Pos
	affine := p.matchIdentText("affine")
	reprC := true
	if p.peek() == lexer.TOKEN_REPR {
		p.errorf("legacy `repr(c) struct` syntax is no longer supported; use `struct` instead")
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
	var namedStateCases []string
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
	} else if p.peekNamedStructStateBracket() {
		namedStateCases = p.parseNamedStructStateBracket(name)
		genericParams = append(genericParams, ast.GenericParam{Position: pos, Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), namedStateCases...), StateOwner: name})
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
		} else if p.peekNamedStructStateBracket() {
			namedStateCases = p.parseNamedStructStateBracket(name)
			genericParams = append(genericParams, ast.GenericParam{Position: pos, Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), namedStateCases...), StateOwner: name})
		}
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	derivedStates := make([]ast.DerivedStateDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "derive" {
			derivedStates = append(derivedStates, p.parseDerivedStateBlock()...)
			continue
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, NamedStateCases: append([]string(nil), namedStateCases...), DerivedStates: derivedStates, Affine: affine, ReprC: reprC, Fields: fields}
}

func (p *Parser) peekNamedStructStateBracket() bool {
	return p.peek() == lexer.TOKEN_LBRACKET && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "state"
}

func (p *Parser) parseNamedStructStateBracket(structName string) []string {
	p.expect(lexer.TOKEN_LBRACKET)
	p.expectIdentText("state")
	cases := make([]string, 0, 2)
	seen := map[string]bool{}
	for {
		name := p.expect(lexer.TOKEN_IDENT).Text
		if seen[name] {
			p.errorf("duplicate struct state %q in %q", name, structName)
		} else {
			seen[name] = true
			cases = append(cases, name)
		}
		if !p.match(lexer.TOKEN_PIPE) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return cases
}

func (p *Parser) parseDerivedStateBlock() []ast.DerivedStateDecl {
	p.expectIdentText("derive")
	p.expectIdentText("state")
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	derived := make([]ast.DerivedStateDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		pos := p.cur().Pos
		stateName := p.expect(lexer.TOKEN_IDENT).Text
		p.expectIdentText("when")
		cond := p.parseExpr()
		p.expectNewline()
		derived = append(derived, ast.DerivedStateDecl{Position: pos, StateName: stateName, Condition: cond})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return derived
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
	annotations := p.parseAnnotations()
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

	return ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Mutable: mutable, IsTail: isTail, Type: typ}
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

func (p *Parser) parseStaticIfDecl() *ast.StaticIfDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	thenBlock := p.parseDeclBlock()

	elifs := make([]ast.StaticElifDecl, 0, 2)
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
	decls := make([]ast.Decl, 0, p.estimateIndentedItemCount())
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
