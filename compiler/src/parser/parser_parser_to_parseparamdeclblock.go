package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

type Parser struct {
	tokens              []lexer.Token
	pos                 int
	errors              []string
	poolScopes          []string
	nurseryGroupByPool  map[string]string
	nurseryCounter      int
	declVisibility      map[ast.Decl]string
	currentVisibility   string
	allowInMembership   bool
	allowTernary        bool
	allowWhereExpr      bool
	staticFunctionDepth int
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens, declVisibility: map[ast.Decl]string{}, nurseryGroupByPool: map[string]string{}, currentVisibility: "public", allowInMembership: true, allowTernary: true, allowWhereExpr: true}
}

// nurseryGroupForActivePool returns the implicit task-group variable name for the innermost
// active nursery (if the innermost active pool is a nursery), so `submit` inside a nursery can
// auto-collect the task into that group for the auto-join at scope exit.
func (p *Parser) nurseryGroupForActivePool() (string, bool) {
	name := p.activePoolName()
	if name == "" {
		return "", false
	}
	grp, ok := p.nurseryGroupByPool[name]
	return grp, ok
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
func (p *Parser) peekAt(offset int) lexer.TokenKind {
	i := p.pos + offset
	if i >= len(p.tokens) {
		return lexer.TOKEN_EOF
	}
	return p.tokens[i].Kind
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
func (p *Parser) withInMembershipDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowInMembership
	p.allowInMembership = false
	defer func() {
		p.allowInMembership = saved
	}()
	return parse()
}
func (p *Parser) withInMembershipEnabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowInMembership
	p.allowInMembership = true
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
func (p *Parser) withWhereExprDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.allowWhereExpr
	p.allowWhereExpr = false
	defer func() {
		p.allowWhereExpr = saved
	}()
	return parse()
}
func (p *Parser) withStaticFunctionBody(parse func() []ast.Stmt) []ast.Stmt {
	p.staticFunctionDepth++
	defer func() {
		p.staticFunctionDepth--
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

func (p *Parser) skipRejectedDecl() {
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		p.advance()
	}
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
	if p.peek() != lexer.TOKEN_INDENT {
		return
	}
	depth := 0
	for p.peek() != lexer.TOKEN_EOF {
		switch p.peek() {
		case lexer.TOKEN_INDENT:
			depth++
		case lexer.TOKEN_DEDENT:
			depth--
			p.advance()
			if depth <= 0 {
				return
			}
			continue
		}
		p.advance()
	}
}

func (p *Parser) ParseFile(filename string) *ast.File {
	file := &ast.File{Filename: filename, Decls: make([]ast.Decl, 0, p.estimateTopLevelItemCount()), DeclVisibility: p.declVisibility}
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

func (p *Parser) markDeclVisibility(decl ast.Decl, visibility string) ast.Decl {
	if decl == nil {
		return nil
	}
	if p.declVisibility == nil {
		p.declVisibility = map[ast.Decl]string{}
	}
	if visibility == "" {
		visibility = p.currentVisibility
	}
	if visibility == "" {
		visibility = "public"
	}
	if _, exists := p.declVisibility[decl]; !exists {
		p.declVisibility[decl] = visibility
	}
	return decl
}

func (p *Parser) parseVisibilityPrefixedDecl() ast.Decl {
	visibility := p.cur().Text
	p.advance()
	if p.peekIdentText("module") {
		module := p.parseNamespaceDecl()
		module.Private = visibility == "private"
		return p.markDeclVisibility(module, visibility)
	}
	decl := p.parseDecl()
	return p.markDeclVisibility(decl, visibility)
}

func (p *Parser) parseDecl() ast.Decl {
	if p.peekIdentText("public") || p.peekIdentText("private") {
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			p.errorf("visibility section %q is only valid inside declaration blocks", p.cur().Text+":")
			p.advance()
			p.advance()
			p.expectNewline()
			return nil
		}
		return p.parseVisibilityPrefixedDecl()
	}
	if p.peekIdentText("protocol") {
		return p.parseInterfaceDecl()
	}
	if p.peek() == lexer.TOKEN_STATIC && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "interface" {
		p.errorf("`static interface` has been removed; use `protocol`")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("interface") {
		p.errorf("`interface` has been removed; use `protocol`")
		p.skipRejectedDecl()
		return nil
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
	if p.peekIdentText("grant") {
		return p.parseGrantAliasDecl()
	}
	if p.peekIdentText("effect") {
		return p.parseEffectPermissionCompatDecl()
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
	if p.peekIdentText("namespace") || p.peekIdentText("module") {
		return p.parseNamespaceDecl()
	}
	if p.peekIdentText("using") {
		return p.parseUsingDecl()
	}
	if p.peekIdentText("from") {
		return p.parseImportDecl()
	}
	if p.peekIdentText("type") {
		return p.parseTypeAliasDecl()
	}
	if p.peekIdentText("tokenset") {
		return p.parseTokenSetDecl()
	}
	if p.peekIdentText("charset") {
		return p.parseCharsetDecl()
	}
	if p.peekIdentText("keywordmap") {
		return p.parseKeywordMapDecl()
	}
	if p.peek() == lexer.TOKEN_ENUM && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "map" {
		return p.parseEnumMapDecl()
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
	if p.peekIdentText("layout") {
		return p.parseLayoutStructDecl()
	}
	if p.peekIdentText("store") {
		return p.parseStoreDecl()
	}
	if p.peekIdentText("soa") {
		return p.parseSoaDecl()
	}
	if p.peekIdentText("linear") || p.peekIdentText("affine") {
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
		if p.peekIdentText("layout") {
			return p.parseLayoutStructDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("store") {
			return p.parseStoreDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("soa") {
			return p.parseSoaDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("linear") || p.peekIdentText("affine") {
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
			p.errorf("declaration annotations must be followed by def, extern, struct, layout struct, store, soa, tree, enum, packed enum, or impl, got %s", p.cur())
			return nil
		case lexer.TOKEN_ENUM:
			return p.parseEnumDeclWithAnnotations(annotations)
		case lexer.TOKEN_STRUCT:
			return p.parseStructDeclWithAnnotations(annotations)
		default:
			p.errorf("declaration annotations must be followed by def, extern, struct, layout struct, store, soa, tree, enum, packed enum, or impl, got %s", p.cur())
			return nil
		}
	}
	if p.peek() == lexer.TOKEN_PACKED && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
		return p.parsePackedEnumDecl()
	}
	switch p.peek() {
	case lexer.TOKEN_CONST:
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "module" {
			return p.parseConstModuleDecl()
		}
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ENUM {
			return p.parseConstEnumDecl()
		}
		return p.parseConstDecl()
	case lexer.TOKEN_ERROR:
		return p.parseErrorDecl()
	case lexer.TOKEN_CONTEXT:
		return p.parseContextDecl()
	case lexer.TOKEN_ENUM:
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "map" {
			return p.parseEnumMapDecl()
		}
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
		return p.parseStaticDecl()
	default:
		p.errorf("unexpected token %s at top level", p.cur())
		p.advance()
		return nil
	}
}
func (p *Parser) parseStoreDecl() *ast.StoreDecl {
	return p.parseStoreDeclWithAnnotations(nil)
}
func (p *Parser) parseLayoutStructDecl() *ast.StructDecl {
	return p.parseLayoutStructDeclWithAnnotations(nil)
}
func (p *Parser) parseSoaDecl() *ast.StoreDecl {
	return p.parseSoaDeclWithAnnotations(nil)
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
	return p.parseColumnStoreDeclWithAnnotations("store", false, annotations)
}
func (p *Parser) parseLayoutStructDeclWithAnnotations(annotations []ast.Annotation) *ast.StructDecl {
	pos := p.cur().Pos
	p.expectIdentText("layout")
	mode := p.cur()
	if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
		p.errorf("expected layout mode before struct, got %s", mode)
		return p.parseStructDeclWithLeadingLayout(annotations, ast.StructLayoutDefault, false, pos)
	}
	p.advance()
	layout := ast.StructLayoutDefault
	reprC := false
	switch mode.Text {
	case "aos":
		layout = ast.StructLayoutAOS
	case "soa":
		layout = ast.StructLayoutSOA
	case "c":
		layout = ast.StructLayoutC
		reprC = true
	case "packed":
		layout = ast.StructLayoutPacked
	default:
		p.errorf("unsupported layout-prefixed struct mode %q; expected `aos`, `soa`, `c`, or `packed`", mode.Text)
	}
	return p.parseStructDeclWithLeadingLayout(annotations, layout, reprC, pos)
}
func (p *Parser) parseSoaDeclWithAnnotations(annotations []ast.Annotation) *ast.StoreDecl {
	return p.parseColumnStoreDeclWithAnnotations("soa", true, annotations)
}
func (p *Parser) parseColumnStoreDeclWithAnnotations(keyword string, soa bool, annotations []ast.Annotation) *ast.StoreDecl {
	pos := p.cur().Pos
	p.expectIdentText(keyword)
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
	return &ast.StoreDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Soa: soa, Fields: fields}
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
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT, lexer.TOKEN_STRING_LIT, lexer.TOKEN_IDENT:
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
	case lexer.TOKEN_STRING_LIT:
		return p.advance().Text
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

	var tags []ast.ErrorVariantDecl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		tagTok := p.expect(lexer.TOKEN_IDENT)
		var payload []ast.ParamDecl
		if p.match(lexer.TOKEN_LPAREN) {
			for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
				payload = append(payload, p.parseParam(false))
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		tags = append(tags, ast.ErrorVariantDecl{Position: tagTok.Pos, Name: tagTok.Text, Payload: payload})
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
func (p *Parser) parseGrantAliasDecl() *ast.GrantAliasDecl {
	pos := p.cur().Pos
	p.expectIdentText("grant")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	refs := p.parsePermissionRefs(false)
	p.expectNewline()
	return &ast.GrantAliasDecl{Position: pos, Name: name, Refs: refs}
}
func (p *Parser) parsePermissionDecl() *ast.PermissionDecl {
	pos := p.cur().Pos
	p.expectIdentText("permission")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.PermissionDecl{Position: pos, Name: name}
	}
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]string, 0, p.estimateIndentedItemCount())
	var includes []string
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
		// `includes Disk, FileSystem` — families this permission subsumes.
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "includes" {
			p.advance()
			for {
				includes = append(includes, p.expect(lexer.TOKEN_IDENT).Text)
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expectNewline()
			continue
		}
		members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.PermissionDecl{Position: pos, Name: name, Members: members, Includes: includes}
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
func (p *Parser) parseEffectPermissionCompatDecl() *ast.PermissionDecl {
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
		return &ast.PermissionDecl{Position: pos, Name: name, Members: members, DeprecatedSyntax: "effect " + name + ":", DeprecatedReplacement: "permission " + name + ":"}
	}

	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.PermissionDecl{Position: pos, Name: name, Members: members, DeprecatedSyntax: "effect " + name + ":", DeprecatedReplacement: "permission " + name + ":"}
	}
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
		p.match(lexer.TOKEN_COMMA)
	}
	p.expectNewline()
	return &ast.PermissionDecl{Position: pos, Name: name, Members: members, DeprecatedSyntax: "effect " + name + ":", DeprecatedReplacement: "permission " + name + ":"}
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
	return &ast.ParamsDecl{Position: pos, Name: name, Params: params, DeprecatedSyntax: "params " + name + ":", DeprecatedReplacement: "bundle " + name + " explicit:"}
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
