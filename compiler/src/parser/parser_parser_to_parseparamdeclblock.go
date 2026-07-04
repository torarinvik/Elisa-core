package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

type Parser struct {
	tokens             []lexer.Token
	pos                int
	errors             []string
	notices            []string
	exprBlockDepth     int
	poolScopes         []string
	nurseryGroupByPool map[string]string
	nurseryCounter     int
	declVisibility     map[ast.Decl]string
	currentVisibility  string
	allowInMembership  bool
	allowTernary       bool
	allowWhereExpr     bool
	// allowQuantifiers enables `forall`/`exists` prefix and `implies` infix in expression position.
	// Set only while parsing law/spec bodies (docs/90 brick 90-4), so ordinary code may keep using
	// `forall`/`exists`/`implies` as identifiers.
	allowQuantifiers     bool
	disallowIsComparison bool
	staticFunctionDepth  int
	// pendingStmts buffers extra statements produced by a single parseStmt call that desugars to
	// MORE than one statement (the grouped `ghost:` block). parseBlock drains this buffer after each
	// parseStmt so the children land flat in the enclosing block — keeping the AST free of any
	// wrapper node and letting every existing walker see plain `VarDeclStmt`s.
	pendingStmts []ast.Stmt
	// byParSeq makes each `for x in xs by par:` lowering's synthesized Slice local unique, so two
	// such loops in the same block do not collide on the `__by_par_src` name.
	byParSeq int
	// pendingDecls buffers extra TOP-LEVEL declarations produced by a single parseDecl call that
	// desugars to MORE than one decl (the `protocol` typestate sugar, docs/96: one declaration expands
	// to a state-bearing struct PLUS one free function per transition). ParseFile drains this buffer
	// after each parseDecl so the synthesized decls land flat at file scope.
	pendingDecls []ast.Decl
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens, declVisibility: map[ast.Decl]string{}, nurseryGroupByPool: map[string]string{}, allowInMembership: true, allowTernary: true, allowWhereExpr: true}
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

// Notices returns non-fatal parse-time diagnostics (deprecation warnings).
func (p *Parser) Notices() []string { return p.notices }

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

// withIsComparisonDisabled parses without treating a top-level `is` as a
// comparison operator, so `value in store is Pattern` parses the store
// expression up to (but not including) the `is` — letting the store-pattern
// clause own the `is` (docs/80 Phase B). `in` binds tighter than `is`.
func (p *Parser) withIsComparisonDisabled(parse func() ast.Expr) ast.Expr {
	saved := p.disallowIsComparison
	p.disallowIsComparison = true
	defer func() {
		p.disallowIsComparison = saved
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

// noticeAt records a non-fatal parse-time diagnostic (deprecation warning),
// surfaced to stderr via Notices().
func (p *Parser) noticeAt(pos lexer.Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf("%s: warning: %s", pos, fmt.Sprintf(format, args...))
	p.notices = append(p.notices, msg)
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
		if len(p.pendingDecls) > 0 {
			file.Decls = append(file.Decls, p.pendingDecls...)
			p.pendingDecls = nil
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
	// Only record EXPLICIT visibility (a `public`/`private` prefix or an active
	// `public:`/`private:` section). Unmarked decls stay absent from the map so the
	// analyzer can tell "default" apart from "explicitly public" — an explicit
	// public mark overrides an enclosing `private module` default, a default does not.
	if visibility == "" {
		return decl
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
	if p.peekIdentText("typestate") {
		return p.parseTypestateDecl()
	}
	if (p.peekIdentText("linear") || p.peekIdentText("affine")) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "typestate" {
		return p.parseTypestateDecl()
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
	if p.peekIdentText("alias") {
		return p.parseAliasDecl()
	}
	if p.peekIdentText("module") {
		return p.parseNamespaceDecl()
	}
	// `extend Foo:` extends a module; `extend grammar Name` (handled later) extends
	// a grammar. Disambiguate on the token after `extend`.
	if p.peekIdentText("extend") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text != "grammar" {
		return p.parseExtendDecl()
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
	if p.peekIdentText("refine") && p.looksLikeRefineDecl() {
		return p.parseRefineDecl()
	}
	if p.peekIdentText("law") && p.looksLikeLawDecl() {
		return p.parseLawDecl()
	}
	if p.peekIdentText("lemma") && p.looksLikeLemmaDecl() {
		return p.parseLemmaDecl()
	}
	if p.peekIdentText("ghost") && p.looksLikeGhostFuncDecl() {
		return p.parseGhostFuncDecl()
	}
	if p.peekIdentText("contract") && p.looksLikeContractDecl() {
		return p.parseContractDecl()
	}
	if p.peekIdentText("tokenset") {
		return p.parseTokenSetDecl()
	}
	if p.peekIdentText("charset") {
		return p.parseCharsetDecl()
	}
	if p.peekIdentText("layout") && p.looksLikeLayoutDecl() {
		return p.parseLayoutDecl()
	}
	if p.peekIdentText("keywordmap") {
		return p.parseKeywordMapDecl()
	}
	if p.peek() == lexer.TOKEN_ENUM && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "map" {
		return p.parseEnumMapDecl()
	}
	if p.peekIdentText("attribute") {
		p.errorf("`attribute` declarations have been removed with the `tree` construct (docs/81); write a plain function over the `enum … is` hierarchy instead")
		p.skipRejectedDecl()
		return nil
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
		p.errorf("the `tree` construct has been removed (docs/81); model the AST as a sealed `enum … is` hierarchy")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("layout") {
		return p.parseLayoutStructDecl()
	}
	if p.peekIdentText("store") {
		p.errorf("`store Name:` declarations have been removed; use an explicit struct with darray fields, or `struct Name layout(soa):` only when compiler-known SoA layout is required")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("soa") {
		p.errorf("`soa Name:` declarations have been removed; use `struct Name layout(soa):` for the remaining compiler-known SoA form")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("linear") || p.peekIdentText("affine") {
		return p.parseStructDecl()
	}
	if p.peek() == lexer.TOKEN_AT {
		annotations := p.parseFuncAnnotations()
		if p.peekIdentText("impl") {
			return p.parseImplDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("layout") {
			return p.parseLayoutStructDeclWithAnnotations(annotations)
		}
		if p.peekIdentText("store") {
			p.errorf("`store Name:` declarations have been removed; use an explicit struct with darray fields, or `struct Name layout(soa):` only when compiler-known SoA layout is required")
			p.skipRejectedDecl()
			return nil
		}
		if p.peekIdentText("soa") {
			p.errorf("`soa Name:` declarations have been removed; use `struct Name layout(soa):` for the remaining compiler-known SoA form")
			p.skipRejectedDecl()
			return nil
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
			p.errorf("declaration annotations must be followed by def, extern, struct, store, soa, tree, enum, packed enum, or impl, got %s", p.cur())
			return nil
		case lexer.TOKEN_ENUM:
			return p.parseEnumDeclWithAnnotations(annotations)
		case lexer.TOKEN_STRUCT:
			return p.parseStructDeclWithAnnotations(annotations)
		default:
			p.errorf("declaration annotations must be followed by def, extern, struct, store, soa, tree, enum, packed enum, or impl, got %s", p.cur())
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
func (p *Parser) parseLayoutStructDecl() ast.Decl {
	return p.parseLayoutStructDeclWithAnnotations(nil)
}

// looksLikeLawDecl distinguishes a `law` declaration from an identifier named `law`. At decl
// position a law is `law <name> [generics] ( … )`, so require an identifier name followed by
// `(` or `[`.
func (p *Parser) looksLikeLawDecl() bool {
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokens[p.pos+2].Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	case lexer.TOKEN_IDENT:
		// Subject-free function-level law with no param list: effect law `law NoAlloc forbids ...`
		// (docs/85 §4) or composite law `law Leaf includes ...` (docs/85 §6).
		return p.tokens[p.pos+2].Text == "forbids" || p.tokens[p.pos+2].Text == "includes"
	default:
		return false
	}
}

// looksLikeLemmaDecl distinguishes a `lemma` declaration (`lemma Name(...)` / `lemma Name[...]`) from
// an identifier that merely happens to be spelled `lemma` (contextual keyword, like `law`).
func (p *Parser) looksLikeLemmaDecl() bool {
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokens[p.pos+2].Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	default:
		return false
	}
}

// parseLemmaDecl parses a ghost lemma (`lemma Name[generics](params) requires … ensure …:` body). It
// reuses the entire function grammar via parseFuncDeclRest and only sets IsLemma — the analyzer then
// verifies the body proves the ensures and erases both the decl and its call sites from codegen.
func (p *Parser) parseLemmaDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("lemma")
	fn := p.parseFuncDeclRest(pos, nil, false)
	if fn != nil {
		fn.IsLemma = true
	}
	return fn
}

func (p *Parser) looksLikeGhostFuncDecl() bool {
	return p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_DEF
}

func (p *Parser) parseGhostFuncDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("ghost")
	p.expect(lexer.TOKEN_DEF)
	fn := p.parseFuncDeclRest(pos, nil, false)
	fn.IsGhost = true
	return fn
}

// parseLawDecl parses a predicate law (docs/85 Stage 1): `law Name[generics](self: T, …) = <bool-expr>`.
// It is represented as a bool-returning FuncDecl with IsLaw set, so it reuses every bit of the
// function machinery (generics, modules, calls, type checking); the analyzer separately enforces
// purity, totality, and the bool return that make it a sound predicate.
func (p *Parser) parseLawDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("law")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
	// An EFFECT law (docs/85 §4, Stage 4) is subject-free: `law NoAlloc forbids Memory.Allocate`. It
	// constrains the whole function, not a value or a place, so it takes no parameter list. Value and
	// frame laws keep their `(subject ...)` list.
	var params []ast.ParamDecl
	if p.match(lexer.TOKEN_LPAREN) {
		params, _ = p.parseExplicitSignatureParamList(true, false)
		p.expect(lexer.TOKEN_RPAREN)
	}
	if p.peekIdentText("forbids") {
		p.matchIdentText("forbids")
		var forbids []ast.PermissionRef
		for {
			forbids = append(forbids, p.parsePermissionRefGroup()...)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Forbids:          forbids,
			IsLaw:            true,
		}
	}
	// A COMPOSITE law (docs/85 §6): `law Leaf includes NoAlloc, NoBoundsChecks` — a function-level
	// law whose obligation is the union of its named members'. Members are bare law-name identifiers.
	if p.peekIdentText("includes") {
		p.matchIdentText("includes")
		var includes []string
		for {
			includes = append(includes, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Includes:         includes,
			IsLaw:            true,
		}
	}
	// A FRAME law (docs/88) carries a `changes`/`preserves` clause instead of an `= <bool-expr>`
	// body: `law MovesPlayerOnly(self: Render&) changes self.px, self.py`. It is a named, reusable
	// frame applied with `fulfills`; it has no predicate body.
	if p.peekIdentText("changes") || p.peekIdentText("preserves") {
		var changes, preserves []ast.EnsuresPath
		if p.matchIdentText("changes") {
			changes = p.parseChangesPathsAfterKeyword()
		}
		if p.matchIdentText("preserves") {
			preserves = p.parseChangesPathsAfterKeyword()
		}
		p.expectNewline()
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			Changes:          changes,
			Preserves:        preserves,
			IsLaw:            true,
		}
	}
	// A BLOCK law (docs/90 brick 90-15): `law Name(...):` with an indented body of zero or more
	// `name = <expr>` sub-predicate bindings followed by a single `return <bool-expr>`. The bindings
	// are inlined into the return expression at parse time, so the law is represented exactly like the
	// `= <expr>` form (a single predicate) and every downstream tier (SMT, linear, flow) is unchanged.
	// Lets an author name the parts of a complex law (e.g. `sorted`, `firstMin`) instead of packing it
	// onto one line.
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		savedQuant := p.allowQuantifiers
		p.allowQuantifiers = true
		body := p.parseBlock()
		p.allowQuantifiers = savedQuant
		predicate, ok := desugarLawBlock(body)
		if !ok {
			p.errorf("a law block body must be zero or more `name = <expr>` bindings followed by a single `return <bool-expr>`")
			predicate = &ast.BoolLit{Position: pos, Value: false}
		}
		return &ast.FuncDecl{
			Position:         pos,
			Name:             name,
			TypeParams:       typeParams,
			RegionParams:     regionParams,
			PermissionParams: permissionParams,
			GenericParams:    genericParams,
			Params:           params,
			ReturnType:       &ast.NamedType{Position: pos, Name: "bool"},
			Body:             []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: predicate}},
			IsLaw:            true,
		}
	}
	p.expect(lexer.TOKEN_ASSIGN)
	savedQuant := p.allowQuantifiers
	p.allowQuantifiers = true
	predicate := p.parseExpr()
	p.allowQuantifiers = savedQuant
	p.expectNewline()
	return &ast.FuncDecl{
		Position:         pos,
		Name:             name,
		TypeParams:       typeParams,
		RegionParams:     regionParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		Params:           params,
		ReturnType:       &ast.NamedType{Position: pos, Name: "bool"},
		Body:             []ast.Stmt{&ast.ReturnStmt{Position: pos, Value: predicate}},
		IsLaw:            true,
	}
}

// looksLikeRefineDecl distinguishes a `refine` named-refinement-alias declaration from an identifier
// merely spelled `refine` (contextual keyword, like `law`). At decl position a refine alias is
// `refine <name> [generics]? (params)? = <base> where <pred>`, so require an identifier name followed
// by `[`, `(`, or `=`.
func (p *Parser) looksLikeRefineDecl() bool {
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokens[p.pos+2].Kind {
	case lexer.TOKEN_LBRACKET, lexer.TOKEN_LPAREN, lexer.TOKEN_ASSIGN:
		return true
	default:
		return false
	}
}

// parseRefineDecl parses a NAMED refinement alias (docs/85 "Level 2"):
//
//	refine Positive = i64 where self > 0
//	refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count
//
// It is pure desugaring sugar; the analyzer expands a binder-position use of `Name`/`Name[..](..)`
// into the equivalent anonymous `Base where PRED` (a WhereRefinementTypeExpr). No new type family.
func (p *Parser) parseRefineDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("refine")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, _, _, _ := p.parseFuncGenericParams()
	var params []ast.ParamDecl
	if p.match(lexer.TOKEN_LPAREN) {
		params, _ = p.parseExplicitSignatureParamList(false, false)
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_ASSIGN)
	savedQuant := p.allowQuantifiers
	p.allowQuantifiers = true
	// parseTypeExpr already folds a trailing `where <pred>` into a WhereRefinementTypeExpr, so the
	// `= Base where Pred` body is parsed in one shot; unwrap it back into the RefineDecl's Base +
	// Predicate fields. A base with no `where` suffix is a malformed refine alias.
	parsed := p.parseTypeExpr()
	p.allowQuantifiers = savedQuant
	wt, ok := parsed.(*ast.WhereRefinementTypeExpr)
	if !ok || wt == nil || wt.Predicate == nil {
		p.errorf("a `refine` alias must be `refine Name[..](..) = Base where <predicate>`")
		return &ast.RefineDecl{Position: pos, Name: name, TypeParams: typeParams, Params: params, Base: parsed}
	}
	base := wt.Base
	predicate := wt.Predicate
	p.expectNewline()
	return &ast.RefineDecl{
		Position:   pos,
		Name:       name,
		TypeParams: typeParams,
		Params:     params,
		Base:       base,
		Predicate:  predicate,
	}
}

// looksLikeContractDecl distinguishes a `contract Name(...)` declaration (docs/97) from an identifier
// merely spelled `contract`. At decl position a contract is `contract <name> [generics] ( … ):`, so
// require an identifier name followed by `(` or `[`.
func (p *Parser) looksLikeContractDecl() bool {
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	switch p.tokens[p.pos+2].Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
		return true
	default:
		return false
	}
}

// parseContractDecl parses a named composable contract (docs/97):
//
//	contract Name[generics](params):
//	    requires <bool>
//	    ensure   <bool>
//	    changes  <path>, …
//	    preserves <path>, …
//
// It is represented as a bool-less FuncDecl with IsContract set, carrying the bundled value contracts
// in Requires/EnsureValues and the frame conditions in Changes/Preserves. The body is exactly a run
// of requires/ensure/changes/preserves clauses — no executable statements, no `decreases`.
func (p *Parser) parseContractDecl() ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("contract")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_LPAREN)
	params, _ := p.parseExplicitSignatureParamList(true, false)
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	var requires, ensures []ast.Expr
	var requireProofs, ensureProofs []*ast.ProofBlockStmt
	var changes, preserves []ast.EnsuresPath
	var includes []ast.ContractInclude
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("requires"):
			p.advance()
			saved := p.allowQuantifiers
			p.allowQuantifiers = true
			cond := p.parseExpr()
			p.allowQuantifiers = saved
			proof, scoped := p.parseOptionalContractScopedProof()
			requires = append(requires, cond)
			if scoped {
				requireProofs = append(requireProofs, &ast.ProofBlockStmt{Position: cond.Pos(), Goal: cond, Proof: proof})
			} else {
				requireProofs = append(requireProofs, nil)
			}
		case p.peekIdentText("ensure"):
			p.advance()
			saved := p.allowQuantifiers
			p.allowQuantifiers = true
			cond := p.parseExpr()
			p.allowQuantifiers = saved
			proof, scoped := p.parseOptionalContractScopedProof()
			ensures = append(ensures, cond)
			if scoped {
				ensureProofs = append(ensureProofs, &ast.ProofBlockStmt{Position: cond.Pos(), Goal: cond, Proof: proof})
			} else {
				ensureProofs = append(ensureProofs, nil)
			}
		case p.matchIdentText("changes"):
			changes = append(changes, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
		case p.matchIdentText("preserves"):
			preserves = append(preserves, p.parseChangesPathsAfterKeyword()...)
			p.expectNewline()
		case p.peekIdentText("includes"):
			incPos := p.cur().Pos
			p.advance()
			law := p.expect(lexer.TOKEN_IDENT).Text
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
			includes = append(includes, ast.ContractInclude{Position: incPos, Law: law, Args: args})
			p.expectNewline()
		case p.peekIdentText("decreases"):
			p.errorf("a `contract` body may not contain `decreases`: a termination measure is a property of a recursion, not a reusable contract (docs/97 §5)")
			p.advance()
			p.parseExpr()
			p.expectNewline()
		default:
			p.errorf("a `contract` body may only contain requires/ensure/changes/preserves/includes clauses, got %s", p.cur())
			for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
				p.advance()
			}
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.FuncDecl{
		Position:         pos,
		Name:             name,
		TypeParams:       typeParams,
		RegionParams:     regionParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		Params:           params,
		Requires:         requires,
		RequiresProofs:   requireProofs,
		EnsureValues:     ensures,
		EnsureProofs:     ensureProofs,
		Changes:          changes,
		Preserves:        preserves,
		ContractIncludes: includes,
		IsContract:       true,
	}
}

// desugarLawBlock inlines a law block's `name = <expr>` bindings into its final `return <expr>`,
// yielding the single predicate expression the rest of the pipeline expects. Bindings are resolved in
// order (a later binding may reference an earlier one). Returns false if the block is not the
// expected shape (some non-binding/non-return statement, no trailing return, or a binding without a
// value). A binding name that shadows a quantifier binder inside an expression is left untouched there
// (the binder wins), so the inline is capture-safe.
func desugarLawBlock(stmts []ast.Stmt) (ast.Expr, bool) {
	subst := map[string]ast.Expr{}
	for i, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			// Only immutable, untyped/typed value bindings — a law is pure, no mutation.
			if n.Mutable || n.Value == nil || n.Name == "" || i == len(stmts)-1 {
				return nil, false
			}
			subst[n.Name] = substituteLawIdents(n.Value, subst, nil)
		case *ast.ReturnStmt:
			if n.Value == nil || i != len(stmts)-1 {
				return nil, false
			}
			return substituteLawIdents(n.Value, subst, nil), true
		default:
			return nil, false
		}
	}
	return nil, false
}

// substituteLawIdents replaces each free identifier named in `subst` with a parenthesized clone of
// its bound expression, over the expression fragment laws use. `bound` is the set of quantifier
// binders currently in scope; a name in `bound` is NOT substituted (the binder shadows the binding).
// An unrecognized node is returned unchanged — any surviving binding-name reference then surfaces as
// a normal undefined-identifier error downstream, never a silent miscompile.
func substituteLawIdents(expr ast.Expr, subst map[string]ast.Expr, bound map[string]bool) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		if bound != nil && bound[n.Name] {
			return n
		}
		if repl, ok := subst[n.Name]; ok {
			return &ast.ParenExpr{Position: n.Position, Inner: ast.CloneExpr(repl)}
		}
		return n
	case *ast.ParenExpr:
		return &ast.ParenExpr{Position: n.Position, Inner: substituteLawIdents(n.Inner, subst, bound)}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: substituteLawIdents(n.Operand, subst, bound)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op,
			Left:  substituteLawIdents(n.Left, subst, bound),
			Right: substituteLawIdents(n.Right, subst, bound)}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: n.Position, Object: substituteLawIdents(n.Object, subst, bound), Field: n.Field}
	case *ast.IndexExpr:
		return &ast.IndexExpr{Position: n.Position,
			Object: substituteLawIdents(n.Object, subst, bound),
			Index:  substituteLawIdents(n.Index, subst, bound)}
	case *ast.CallExpr:
		out := *n
		out.Func = substituteLawIdents(n.Func, subst, bound)
		out.Args = make([]ast.Expr, len(n.Args))
		for i, arg := range n.Args {
			out.Args[i] = substituteLawIdents(arg, subst, bound)
		}
		return &out
	case *ast.QuantifierExpr:
		inner := bound
		if len(n.Vars) > 0 {
			inner = map[string]bool{}
			for k := range bound {
				inner[k] = true
			}
			for _, v := range n.Vars {
				inner[v] = true
			}
		}
		return &ast.QuantifierExpr{Position: n.Position, Exists: n.Exists, Vars: n.Vars,
			In:   substituteLawIdents(n.In, subst, bound),
			Body: substituteLawIdents(n.Body, subst, inner)}
	default:
		return n
	}
}
func (p *Parser) parseTypeAliasDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	// Optional `[param, ...]` for parametric refinement aliases: `type Name[cap] = base is Law[..cap..]`.
	// parseFuncGenericParams already peeks for `[` itself, so call it unconditionally.
	typeParams, _, _, _ := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_ASSIGN)
	target := p.parseTypeExpr()
	p.expectNewline()
	return &ast.TypeAliasDecl{Position: pos, Name: name, TypeParams: typeParams, Target: target}
}
// parseLayoutStructDeclWithAnnotations handles the REMOVED prefix form
// `layout soa struct Name:` — the layout clause is a postfix header clause now
// (`struct Name layout(soa):`, one grammar across struct/enum/overlay). Directed
// error, recover-as-canonical (same mode, member block parses, no cascade).
func (p *Parser) parseLayoutStructDeclWithAnnotations(annotations []ast.Annotation) ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("layout")
	mode := p.cur()
	if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
		p.errorf("expected layout mode before struct, got %s", mode)
		return p.parseStructDeclWithLeadingLayout(annotations, ast.StructLayoutDefault, false, pos)
	}
	p.advance()
	p.errorf("`layout %s struct Name:` has been removed; use the postfix clause `struct Name layout(%s):`", mode.Text, mode.Text)
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
func (p *Parser) parseAnnotations() []ast.Annotation {
	annotations := make([]ast.Annotation, 0, 1)
	for p.peek() == lexer.TOKEN_AT {
		p.advance() // @
		// Bracketed list form `@[a, b(args), ...]` — a comma-separated group of annotations that
		// desugars to the same flat list as stacking them one per `@` line. Self-delimiting, so it
		// composes cleanly with arg-bearing entries. Commas may be followed by newlines (multiline list).
		if p.match(lexer.TOKEN_LBRACKET) {
			p.skipNewlines()
			for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
				annotations = append(annotations, p.parseAnnotationEntry())
				if p.match(lexer.TOKEN_COMMA) {
					p.skipNewlines()
					continue
				}
				break
			}
			p.skipNewlines()
			p.expect(lexer.TOKEN_RBRACKET)
		} else {
			annotations = append(annotations, p.parseAnnotationEntry())
		}
		p.skipNewlines()
	}
	return annotations
}

// parseAnnotationEntry parses a single annotation `name` with an optional `(args)` suffix, shared by
// the per-`@` form and the bracketed `@[...]` list form.
func (p *Parser) parseAnnotationEntry() ast.Annotation {
	pos := p.cur().Pos
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
	return ast.Annotation{Position: pos, Name: name, Args: args}
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
				payload = append(payload, p.parseErrorPayloadDecl())
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

// parseErrorPayloadDecl parses one error-variant payload field. `name: T` is a
// named field; a bare `T` (e.g. `LexerError(LexError)`) is positional — error
// payloads are matched by position, so the name is optional.
func (p *Parser) parseErrorPayloadDecl() ast.ParamDecl {
	pos := p.cur().Pos
	i := p.pos
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_MUTABLE {
		i++
	}
	if i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_IDENT && p.tokens[i+1].Kind == lexer.TOKEN_COLON {
		return p.parseParam(false)
	}
	return ast.ParamDecl{Position: pos, Type: p.parseTypeExpr()}
}
func (p *Parser) parseAliasDecl() *ast.AliasDecl {
	pos := p.cur().Pos
	// `alias Name = cap1, cap2, …` declares a capability set, referenced via `can`.
	// (The AST node is still named AliasDecl for historical reasons.)
	p.expectIdentText("alias")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	refs := p.parsePermissionRefs(false)
	p.expectNewline()
	return &ast.AliasDecl{Position: pos, Name: name, Refs: refs}
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
