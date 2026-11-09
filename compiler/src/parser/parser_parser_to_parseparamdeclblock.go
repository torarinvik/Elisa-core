package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

type Parser struct {
	tokens         []lexer.Token
	pos            int
	errors         []string
	notices        []string
	exprBlockDepth int
	// nestDepth counts the recursive parsing levels currently open; see MaxNestingDepth.
	nestDepth          int
	rebindCounter      int
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
	// contentNames lists the content keys of the machines parsed so far, each
	// followed by ';'. It numbers machines with identical token text, keeping
	// content-derived names unique (see contentNameSuffix); a string so syntax
	// checkpoints copy and compare it like the other counters.
	contentNames string
	// pendingDecls buffers extra TOP-LEVEL declarations produced by a single parseDecl call that
	// desugars to MORE than one decl (the `protocol` typestate sugar, docs/96: one declaration expands
	// to a state-bearing struct PLUS one free function per transition). ParseFile drains this buffer
	// after each parseDecl so the synthesized decls land flat at file scope.
	pendingDecls []ast.Decl
	// foreignEnumErrorSetEmitted records that this file already carries the synthesized
	// `error ForeignEnum` set, so several `extern enum` declarations share one (docs/127 D9).
	foreignEnumErrorSetEmitted bool
	// pendingStateDecls collects function-local `state Foo` / `state Bar(baz: T)` declarations
	// (docs/125 §5 local-state sugar) until a `start Foo:` consumes them: the `start` synthesizes
	// an enum from the accumulated states, hoists it to pendingDecls, and lowers to
	// `machine from <synth>.Foo`. Cleared when a `start` consumes it and reset per-function so
	// states never leak across a `start` or a function boundary.
	pendingStateDecls []machineState
	// stmtGuardArmed enables the generalized postfix statement guard (docs/125 §6b): while armed,
	// a postfix `if COND` with NO `else` whose condition ends at a statement terminator is not a
	// malformed ternary but a do-or-skip guard on the enclosing statement. Armed only around the
	// statement-level expression parses that admit guards (expression statements, `<-` RHS,
	// compound-assign RHS) — never around declarations, which must bind unconditionally. The flag
	// disarms on the FIRST ternary engagement (guard recorded OR `else` seen), so a statement has
	// at most one guard and `a if c else b if c2` stays an error rather than a surprising guard.
	stmtGuardArmed bool
	// stmtGuardCond holds the recorded guard condition until the statement parser wraps the
	// finished statement in `if COND: <stmt>` (parse-time desugar, like break/continue/return-if).
	stmtGuardCond ast.Expr
	// A condition-list block already consumed the statement-ending dedent.
	stmtGuardBlockEnd bool
	// Innermost loop's simple yielded accumulator, or "" for a statement loop.
	loopBreakTargets []string
	// Keep the original hot parser fields at their previous offsets.
	trace          *SyntaxTrace
	readThrough    int
	readLength     bool
	readFrom       int
	suffixReplay   *syntaxSuffixReplay
	borrowedTokens bool
}

// takeStmtGuard consumes a recorded postfix statement guard, if any, wrapping the finished
// statement in the equivalent IfStmt. Also disarms the guard window unconditionally.
func (p *Parser) takeStmtGuard(stmt ast.Stmt) ast.Stmt {
	p.stmtGuardArmed = false
	if p.stmtGuardCond == nil {
		return stmt
	}
	cond := p.stmtGuardCond
	p.stmtGuardCond = nil
	return &ast.IfStmt{Position: stmt.Pos(), Cond: cond, Then: []ast.Stmt{stmt}}
}

// stmtEndAhead reports whether the next token terminates a statement — the disambiguator that
// keeps the postfix statement guard from ever firing inside a bracketed context (there the next
// token is always `)`/`,`/`]`/... instead).
func (p *Parser) stmtEndAhead() bool {
	switch p.peek() {
	case lexer.TOKEN_NEWLINE, lexer.TOKEN_DEDENT, lexer.TOKEN_EOF:
		return true
	}
	return false
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens, declVisibility: map[ast.Decl]string{}, nurseryGroupByPool: map[string]string{}, allowInMembership: true, allowTernary: true, allowWhereExpr: true}
}

// NewWithBorrowedTokens borrows a read-only slice for the duration of parsing.
// The owner must keep it immutable. Recovery detaches before the first write.
func NewWithBorrowedTokens(tokens []lexer.Token) *Parser {
	p := New(tokens)
	p.borrowedTokens = true
	return p
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
func (p *Parser) expect(kind lexer.TokenKind) lexer.Token {
	tok := p.cur()
	if tok.Kind != kind {
		if kind == lexer.TOKEN_IDENT && isReservedWordToken(tok) {
			p.reportReservedWordAsName(tok)
			p.advance()
			tok.Kind = lexer.TOKEN_IDENT
			return tok
		}
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

func (p *Parser) ParseFile(filename string) *ast.File { return p.parseFile(filename, nil) }
func (p *Parser) parseFile(filename string, file *ast.File) *ast.File {
	if file == nil {
		file = &ast.File{Filename: filename, Decls: make([]ast.Decl, 0, p.estimateTopLevelItemCount()), DeclVisibility: p.declVisibility}
	}
	if p.trace != nil {
		defer func() { p.trace.complete = len(p.errors) == 0 && len(p.notices) == 0 && !p.trace.invalid }()
	}
	defer p.recoverNestingOverflow()
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
		if p.trace != nil {
			p.recordSyntaxBoundary(file)
			if p.trySyntaxSuffix(file) {
				break
			}
		}
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
		if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
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
	if p.peekIdentText("effect") {
		return p.parseEffectDecl()
	}
	if p.peekIdentText("typestate") {
		return p.parseTypestateDecl()
	}
	if (p.peekIdentText("linear") || p.peekIdentText("affine")) && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "typestate" {
		return p.parseTypestateDecl()
	}
	if p.peek() == lexer.TOKEN_STATIC && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "interface" {
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
	if p.peekIdentText("handler") {
		return p.parseHandlerDecl()
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
	if p.peekIdentText("extend") && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "grammar" {
		p.errorf("grammar and lexer DSL declarations have been removed")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("extend") {
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
	if p.peekIdentText("layout") && p.looksLikeLayoutDecl() {
		return p.parseLayoutDecl()
	}
	if p.peek() == lexer.TOKEN_ENUM && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "map" {
		return p.parseEnumMapDecl()
	}
	if p.peekIdentText("attribute") {
		p.errorf("`attribute` declarations have been removed with the `tree` construct (docs/81); write a plain function over the `enum … is` hierarchy instead")
		p.skipRejectedDecl()
		return nil
	}
	if p.peekIdentText("grammar") || p.peekIdentText("grammarenv") || p.peekIdentText("lexer") || p.peekIdentText("tokenset") || p.peekIdentText("charset") || p.peekIdentText("keywordmap") {
		p.errorf("grammar and lexer DSL declarations have been removed")
		p.skipRejectedDecl()
		return nil
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
		case lexer.TOKEN_EXPORT:
			return p.parseExportDeclWithAnnotations(annotations)
		case lexer.TOKEN_REPR:
			return p.parseStructDeclWithAnnotations(annotations)
		case lexer.TOKEN_PACKED:
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ENUM {
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
	if p.peek() == lexer.TOKEN_PACKED && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ENUM {
		return p.parsePackedEnumDecl()
	}
	switch p.peek() {
	case lexer.TOKEN_CONST:
		if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "module" {
			return p.parseConstModuleDecl()
		}
		if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ENUM {
			return p.parseConstEnumDecl()
		}
		return p.parseConstDecl()
	case lexer.TOKEN_ERROR:
		return p.parseErrorDecl()
	case lexer.TOKEN_ENUM:
		if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "map" {
			return p.parseEnumMapDecl()
		}
		return p.parseEnumDecl()
	case lexer.TOKEN_GLOBAL:
		if p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "region" && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_IDENT {
			return p.parseGlobalRegionDecl()
		}
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
