package parser

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseForHeaderExpr() ast.Expr {
	end := p.pos
	depth := 0
	for end < len(p.tokens) {
		tok := p.tokens[end]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_IF, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				subTokens := append([]lexer.Token(nil), p.tokens[p.pos:end]...)
				subTokens = append(subTokens, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: tok.Pos})
				sub := New(subTokens)
				sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
				expr := sub.parseExpr()
				p.errors = append(p.errors, sub.Errors()...)
				p.pos = end
				return expr
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && p.isForHeaderWhereClauseBoundary(end) {
				subTokens := append([]lexer.Token(nil), p.tokens[p.pos:end]...)
				subTokens = append(subTokens, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: tok.Pos})
				sub := New(subTokens)
				sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
				expr := sub.parseExpr()
				p.errors = append(p.errors, sub.Errors()...)
				p.pos = end
				return expr
			}
			if depth == 0 && p.looksLikeForStmtAt(end) {
				subTokens := append([]lexer.Token(nil), p.tokens[p.pos:end]...)
				subTokens = append(subTokens, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: tok.Pos})
				sub := New(subTokens)
				sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
				expr := sub.parseExpr()
				p.errors = append(p.errors, sub.Errors()...)
				p.pos = end
				return expr
			}
		}
		end++
	}
	return p.parseExpr()
}

func (p *Parser) isForHeaderWhereClauseBoundary(index int) bool {
	if index <= p.pos || index >= len(p.tokens) {
		return false
	}
	tok := p.tokens[index]
	if tok.Kind != lexer.TOKEN_IDENT || tok.Text != "where" {
		return false
	}
	return index == 0 || p.tokens[index-1].Kind != lexer.TOKEN_DOT
}

func unwrapReverseIterableSource(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return expr, false
	}
	if len(call.Args) != 1 || len(call.WithArgs) != 0 || len(call.WithBundles) != 0 || len(call.WithItemOrder) != 0 {
		return expr, false
	}
	if call.ArgName(0) != "" {
		return expr, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "rev" {
		return expr, false
	}
	return call.Args[0], true
}
func (p *Parser) parseIterLoopPattern() ast.MoveBindPattern {
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindPattern()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COMMA {
		pos := p.cur().Pos
		args := make([]ast.MoveBindArg, 0, 4)
		for {
			tok := p.expect(lexer.TOKEN_IDENT)
			args = append(args, ast.MoveBindArg{Position: tok.Pos, Name: tok.Text})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		return &ast.MoveBindTuplePattern{Position: pos, Args: args}
	}
	if p.peekQualifiedStructDestructurePattern() {
		pos := p.cur().Pos
		typeName := p.parseQualifiedDeclName()
		return p.parseMoveBindStructBracePattern(pos, typeName)
	}
	return p.parseMoveBindPattern()
}
func (p *Parser) looksLikeLetDestructureStmt() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "let" || p.pos+1 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_LBRACE:
		return true
	case lexer.TOKEN_IDENT:
		i := p.pos + 2
		for i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_DOT && p.tokens[i+1].Kind == lexer.TOKEN_IDENT {
			i += 2
		}
		return i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_LBRACE
	default:
		return false
	}
}
func (p *Parser) parseWaitAllStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("wait")
	p.expectIdentText("all")
	target := p.parseExpr()
	p.expectNewline()
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "task_group_wait_all"},
			Args: []ast.Expr{&ast.CastExpr{
				Position: pos,
				Operand:  &ast.AddrOfExpr{Position: pos, Operand: target},
				Target: &ast.RefType{
					Position: pos,
					Elem:     &ast.NamedType{Position: pos, Name: "TaskGroup"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}},
		},
	}
}
func (p *Parser) parseNotifyStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("notify")
	kind := p.expect(lexer.TOKEN_IDENT)
	target := p.parseExpr()
	p.expectNewline()
	callee := "notify_one"
	if kind.Text == "all" {
		callee = "notify_all"
	}
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: callee},
			Args: []ast.Expr{&ast.CastExpr{
				Position: pos,
				Operand:  &ast.AddrOfExpr{Position: pos, Operand: target},
				Target: &ast.RefType{
					Position: pos,
					Elem:     &ast.NamedType{Position: pos, Name: "CondVar"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}},
		},
	}
}
func (p *Parser) parseLockStmt() *ast.LockStmt {
	pos := p.cur().Pos
	p.expectIdentText("lock")
	mutex := p.parseExpr()
	p.expect(lexer.TOKEN_AS)
	guardName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.LockStmt{Position: pos, Mutex: mutex, GuardName: guardName, Body: body}
}
func (p *Parser) parseMatch() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	if p.match(lexer.TOKEN_QUESTION) {
		name := "__match_optional"
		var value ast.Expr
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
			name = p.cur().Text
			p.advance()
			p.advance()
			value = p.parseMatchHeadExpr()
		} else {
			value = p.parseMatchHeadExpr()
		}
		arms := p.parseMatchArms()
		return &ast.IfStmt{
			Position:              pos,
			Cond:                  &ast.OptionalBindExpr{Position: pos, Name: name, Value: value},
			Then:                  []ast.Stmt{&ast.MatchStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: name}, Arms: arms}},
			DeprecatedSyntax:      "match?",
			DeprecatedReplacement: "match maybe with a null arm",
		}
	}
	value := p.parseMatchHeadExpr()
	// `match <expr> as <ok>:` is sugar for a catch over an error union: the `ok:` arm
	// binds the success value and the remaining arms handle error variants. It desugars
	// to a CatchExpr so all of catch's semantics (ok-binding, payload binding,
	// exhaustiveness) are reused unchanged.
	if p.match(lexer.TOKEN_AS) {
		return p.parseMatchAsCatch(pos, value)
	}
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchStmt{Position: pos, Value: value, Store: store, Arms: arms}
}

// parseMatchAsCatch parses `match <value> as <okName>:` with an `ok:` success arm,
// `Err.Variant(binds):` error arms, and an optional `else:`/`_:` catch-all, building an
// equivalent CatchExpr (wrapped in an ExprStmt).
func (p *Parser) parseMatchAsCatch(pos lexer.Pos, value ast.Expr) ast.Stmt {
	okName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	p.skipNewlines()
	success := ast.CatchArm{Position: pos, Name: okName}
	hasOk := false
	var arms []ast.CatchArm
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		armPos := p.cur().Pos
		// `ok:` — the success arm. Its body becomes the catch success body; the value
		// is bound to okName from `as`.
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "ok" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			success.Body = p.parseCatchArmBody(armPos)
			success.Position = armPos
			hasOk = true
			continue
		}
		// `else:` / `_:` — a catch-all bound to the whole error (error-binding arm).
		if p.peek() == lexer.TOKEN_ELSE || (p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_") {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			arms = append(arms, ast.CatchArm{Position: armPos, Name: "_", ErrorBinding: true, Body: p.parseCatchArmBody(armPos)})
			continue
		}
		arms = append(arms, p.parseCatchArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	if !hasOk {
		p.errorf("`match ... as` requires an `ok:` arm")
	}
	return &ast.ExprStmt{Position: pos, Expr: &ast.CatchExpr{Position: pos, Value: value, Success: success, Arms: arms}}
}
func (p *Parser) parseMatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	value := p.parseMatchHeadExpr()
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchExpr{Position: pos, Value: value, Store: store, Arms: arms}
}
func (p *Parser) parseMatchHeadExpr() ast.Expr {
	first := p.withInMembershipDisabled(p.parseExpr)
	if p.peek() != lexer.TOKEN_COMMA {
		return first
	}
	elems := []ast.Expr{first}
	for p.match(lexer.TOKEN_COMMA) {
		elems = append(elems, p.withInMembershipDisabled(p.parseExpr))
	}
	return &ast.TupleExpr{Position: first.Pos(), Elems: elems}
}
func (p *Parser) parseInStore() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IN)
	store := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	// `in auto:` is region inference: it desugars to a compiler-synthesized scoped
	// region that owns every allocation in the block and is freed (O(1)) at block exit
	// (docs/68). A value that escapes the block is caught by the normal region
	// destroy/outlives checks — inference's slack becomes a diagnostic, not a leak.
	if ident, ok := store.(*ast.Ident); ok && ident.Name == "auto" {
		return &ast.RegionStmt{Position: pos, Name: synthesizedAutoRegionName(pos), Lazy: true, Body: body}
	}
	return &ast.InStoreStmt{Position: pos, Store: store, Body: body}
}

// synthesizedAutoRegionName builds a unique, source-located name for an `in auto:`
// region. The reserved `__auto_` prefix plus the byte offset makes nested/sibling
// auto scopes distinct and avoids collision with any plausible user region name.
func synthesizedAutoRegionName(pos lexer.Pos) string {
	return fmt.Sprintf("__auto_%d", pos.Offset)
}
func (p *Parser) parseCanStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("can")
	permissions := p.parsePermissionRefs(false)
	var asTarget string
	if p.match(lexer.TOKEN_AS) {
		asTarget = p.expect(lexer.TOKEN_IDENT).Text
		for p.match(lexer.TOKEN_DOT) {
			asTarget += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, As: asTarget}
}
func (p *Parser) parseTrustedStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("trusted")
	permissions := p.parsePermissionRefs(false)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body, SuppressPermissionInference: true}
}
func (p *Parser) parseArgsScopeItems() ([]ast.WithArg, []ast.ParamPackUse, []ast.ArgsScopeItem) {
	args := make([]ast.WithArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	packs := make([]ast.ParamPackUse, 0, 1)
	items := make([]ast.ArgsScopeItem, 0, cap(args))
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil, nil
	}
	sawPack := false
	sawArg := false
	for {
		if p.matchIdentText("use") {
			if sawPack {
				p.errorf("args-scope parameter-pack application may appear at most once")
			}
			if sawArg {
				p.errorf("args-scope parameter-pack application must appear before ordinary named arguments")
			}
			pack := p.parseValueParamPackUse()
			packs = append(packs, pack)
			items = append(items, ast.ArgsScopeItem{Position: pack.Position, Pack: pack, IsPack: true})
			sawPack = true
		} else {
			pos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			arg := ast.WithArg{Position: pos, Name: name}
			switch {
			case p.match(lexer.TOKEN_ASSIGN):
				arg.Value = p.parseExpr()
			case p.match(lexer.TOKEN_COLON):
				if p.peek() == lexer.TOKEN_COMMA || p.peek() == lexer.TOKEN_RPAREN {
					arg.Value = &ast.Ident{Position: pos, Name: name}
					arg.Shorthand = true
				} else {
					arg.Value = p.parseExpr()
				}
			default:
				p.errorf("expected `:` or `=` after args-scope binding %q", name)
				arg.Value = &ast.Ident{Position: pos, Name: name}
				arg.Shorthand = true
			}
			args = append(args, arg)
			items = append(items, ast.ArgsScopeItem{Position: pos, Arg: arg})
			sawArg = true
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RPAREN {
			break
		}
	}
	if len(packs) == 0 {
		items = nil
	}
	return args, packs, items
}
func (p *Parser) parseArgsScopeStmt() *ast.ArgsScopeStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WITH)
	p.expectIdentText("args")
	p.expect(lexer.TOKEN_LPAREN)
	args, packs, items := p.parseArgsScopeItems()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.ArgsScopeStmt{Position: pos, Args: args, ParamPacks: packs, ItemOrder: items, Body: body}
}
func (p *Parser) parseWithStmt() *ast.WithStmt {
	pos := p.cur().Pos
	args, bundles, items := p.parseWithValueClause()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.WithStmt{Position: pos, Args: args, Bundles: bundles, WithItemOrder: items, Body: body}
}
func (p *Parser) parseWithArenaStmt() *ast.RegionStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WITH)
	p.expectIdentText("arena")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var capacity ast.Expr
	if p.match(lexer.TOKEN_LPAREN) {
		capacity = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_AS)
	ownerName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity, OwnerName: ownerName, Body: body}
}
func (p *Parser) parseCascadeStmt() *ast.CascadeStmt {
	pos := p.cur().Pos
	p.expectIdentText("cascade")
	target := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CascadeStmt{Position: pos, Target: target, Body: body}
}
func (p *Parser) parseMatchArms() []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.MatchArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMatchArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}
func (p *Parser) parseMatchArm() []ast.MatchArm {
	pos := p.cur().Pos
	patterns := p.parseTopLevelMatchPatterns()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	arms := make([]ast.MatchArm, 0, len(patterns))
	for _, pattern := range patterns {
		arms = append(arms, ast.MatchArm{Position: pos, Pattern: pattern, Body: body})
	}
	return arms
}
func (p *Parser) parseTopLevelMatchPatterns() []ast.MatchPattern {
	patterns := []ast.MatchPattern{p.parseMatchPatternNoOr()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		patterns = append(patterns, p.parseMatchPatternNoOr())
	}
	return patterns
}
func (p *Parser) parseMatchPattern() ast.MatchPattern {
	return p.parseNestedOrMatchPattern()
}
func (p *Parser) parseNestedOrMatchPattern() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	if p.peek() != lexer.TOKEN_PIPE {
		return pattern
	}
	options := []ast.MatchPattern{pattern}
	for p.match(lexer.TOKEN_PIPE) {
		options = append(options, p.parseNestedMatchPattern())
	}
	orPattern := &ast.MatchOrPattern{Position: pattern.Pos(), Options: options}
	return orPattern
}
func (p *Parser) parseMatchPatternNoOr() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	if p.peek() == lexer.TOKEN_COMMA {
		elems := []ast.MatchPattern{pattern}
		for p.match(lexer.TOKEN_COMMA) {
			elems = append(elems, p.parseNestedOrMatchPattern())
		}
		pattern = &ast.MatchTuplePattern{Position: pattern.Pos(), Elems: elems}
	}
	switch pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern, *ast.MatchBindPattern, *ast.MatchVariantPattern, *ast.MatchStructPattern, *ast.MatchTuplePattern, *ast.MatchListPattern:
		return pattern
	default:
		p.errorf("top-level match arm must use Enum.Variant(...), Struct(...), a literal, a binding, a tuple pattern, a list pattern, or _")
		return pattern
	}
}
func (p *Parser) parseNestedMatchPattern() ast.MatchPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_DOT {
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseMatchValuePatternExpr()}
	}
	if p.peek() == lexer.TOKEN_CARET {
		p.advance()
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseExpr(), Pinned: true}
	}
	if p.peek() == lexer.TOKEN_STRING_LIT {
		return &ast.MatchStringLiteralPattern{Position: pos, Value: p.advance().Text}
	}
	if p.peek() == lexer.TOKEN_LBRACKET {
		return p.parseMatchListPattern(pos)
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMatchStructPatternAfterName(pos, "")
	}
	if p.peek() == lexer.TOKEN_INT_LIT || p.peek() == lexer.TOKEN_FLOAT_LIT || p.peek() == lexer.TOKEN_HEX_LIT ||
		p.peek() == lexer.TOKEN_CHAR_LIT || p.peek() == lexer.TOKEN_TRUE || p.peek() == lexer.TOKEN_FALSE ||
		p.peek() == lexer.TOKEN_NULL || p.peek() == lexer.TOKEN_MINUS || p.peek() == lexer.TOKEN_LPAREN {
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseMatchValuePatternExpr()}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		return &ast.MatchWildcardPattern{Position: pos}
	}
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	if p.peek() == lexer.TOKEN_LPAREN {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	if !p.match(lexer.TOKEN_DOT) {
		return &ast.MatchBindPattern{Position: pos, Name: parts[0]}
	}
	parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	name := strings.Join(parts[:len(parts)-1], ".")
	variant := parts[len(parts)-1]
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseMatchPatternArg())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
				if p.peek() == lexer.TOKEN_RPAREN {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return &ast.MatchVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
}
func (p *Parser) parseMatchListPattern(pos lexer.Pos) ast.MatchPattern {
	p.expect(lexer.TOKEN_LBRACKET)
	elems := make([]ast.MatchPattern, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	if p.peek() != lexer.TOKEN_RBRACKET {
		for {
			if p.peek() == lexer.TOKEN_ELLIPSIS {
				restPos := p.advance().Pos
				elems = append(elems, &ast.MatchRestPattern{Position: restPos})
				if p.peek() != lexer.TOKEN_RBRACKET {
					p.errorf("list pattern rest marker must be the final element")
				}
				break
			}
			elems = append(elems, p.parseNestedOrMatchPattern())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACKET {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.MatchListPattern{Position: pos, Elems: elems}
}
func buildQualifiedMatchValueExpr(pos lexer.Pos, parts []string) ast.Expr {
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: "<error>"}
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for i := 1; i < len(parts); i++ {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: parts[i]}
	}
	return expr
}
func (p *Parser) parseMatchStructPatternAfterName(pos lexer.Pos, typeName string) ast.MatchPattern {
	brace := false
	open := p.peek()
	close := lexer.TOKEN_RPAREN
	switch open {
	case lexer.TOKEN_LPAREN:
		p.advance()
	case lexer.TOKEN_LBRACE:
		p.advance()
		brace = true
		close = lexer.TOKEN_RBRACE
	default:
		p.errorf("struct pattern expects (...) or {...}")
	}
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(close))
	if p.peek() != close {
		for {
			if brace {
				args = append(args, p.parseBraceMatchStructPatternArg())
			} else if typeName == "count" {
				args = append(args, p.parseMatchPatternArg())
			} else {
				args = append(args, p.parseMatchStructPatternArg())
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == close {
				break
			}
		}
	}
	p.expect(close)
	return &ast.MatchStructPattern{Position: pos, TypeName: typeName, Args: args, Brace: brace}
}
func (p *Parser) parseMatchStructPatternArg() ast.MatchPatternArg {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_COLON {
		pos := p.cur().Pos
		p.errorf("struct pattern fields must use name: pattern")
		pattern := p.parseNestedOrMatchPattern()
		return ast.MatchPatternArg{Position: pos, Pattern: pattern}
	}
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	pattern := p.parseNestedOrMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}
func (p *Parser) parseBraceMatchStructPatternArg() ast.MatchPatternArg {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COLON) {
		return ast.MatchPatternArg{
			Position: pos,
			Name:     name,
			Pattern:  &ast.MatchBindPattern{Position: pos, Name: name},
		}
	}
	pattern := p.parseNestedOrMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}
