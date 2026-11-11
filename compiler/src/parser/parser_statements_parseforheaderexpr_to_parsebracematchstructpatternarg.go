package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

// parseForHeaderSlice sub-parses the header tokens [p.pos, end) as one expression and
// advances past them. The sub-parser must consume the WHOLE slice: unconsumed trailing
// tokens used to be silently dropped, which let junk in a for-loop header compile as a
// no-op (`for i in 0..<len step 2:` stepped by 1 — a silent miscompile). Anything the
// expression grammar leaves behind is now a parse error at the first leftover token.
func (p *Parser) parseForHeaderSlice(end int, boundaryPos lexer.Pos) ast.Expr {
	tokens := p.tokenSpan(p.pos, end)
	subTokens := make([]lexer.Token, len(tokens)+1)
	copy(subTokens, tokens)
	subTokens[len(tokens)] = lexer.Token{Kind: lexer.TOKEN_EOF, Pos: boundaryPos}
	sub := New(subTokens)
	sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
	expr := sub.parseExpr()
	p.errors = append(p.errors, sub.Errors()...)
	if len(sub.Errors()) == 0 && sub.peek() != lexer.TOKEN_EOF {
		p.errorAt(sub.cur().Pos, "unexpected trailing tokens in for-loop header, starting at %s", sub.cur())
	}
	p.seek(end)
	return expr
}

func (p *Parser) parseForHeaderExpr() ast.Expr {
	end := p.pos
	depth := 0
	for p.hasToken(end) {
		tok := p.tokenAt(end)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE, lexer.TOKEN_IF, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
		case lexer.TOKEN_PIPE:
			// docs/119 §3: `|acc = 0|` loop-header decls end the iterable expression.
			// Disambiguated from a bitwise `|` by the `IDENT =` lookahead (assignment
			// cannot appear inside an expression).
			if depth == 0 && p.loopHeaderDeclsAt(end) {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
		case lexer.TOKEN_IDENT:
			// A trailing `step N` ends the range-bound expression so the loop parser can reject it
			// with a directed diagnostic (the stride is spelled on the range: `lo..<hi..N`). Without
			// this boundary the slice sub-parse silently TRUNCATED `step N` — the loop compiled and
			// stepped by 1. Guarded to end > p.pos so a source expression named `step` still parses.
			if depth == 0 && tok.Text == "step" && end > p.pos && p.hasToken(end+1) && p.tokenAt(end+1).Kind != lexer.TOKEN_COLON {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
			// A trailing `by par` / `by simd` data-parallel marker ends the range-bound expression
			// (e.g. the `n` in `for i in 0 ..< n by par:`). Stop the header scan at `by` so the loop
			// parser can see and consume the marker.
			if depth == 0 && tok.Text == "by" && p.hasToken(end+1) && p.tokenAt(end+1).Kind == lexer.TOKEN_IDENT {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
			if depth == 0 && p.isForHeaderWhereClauseBoundary(end) {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
			if depth == 0 && p.looksLikeForStmtAt(end) {
				return p.parseForHeaderSlice(end, tok.Pos)
			}
		}
		end++
	}
	return p.parseExpr()
}

func (p *Parser) isForHeaderWhereClauseBoundary(index int) bool {
	if index <= p.pos || !p.hasToken(index) {
		return false
	}
	tok := p.tokenAt(index)
	if tok.Kind != lexer.TOKEN_IDENT || tok.Text != "where" {
		return false
	}
	return index == 0 || p.tokenAt(index-1).Kind != lexer.TOKEN_DOT
}

func unwrapReverseIterableSource(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return expr, false
	}
	if len(call.Args) != 1 {
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
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COMMA {
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
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "let" || !p.hasToken(p.pos+1) {
		return false
	}
	switch p.tokenAt(p.pos + 1).Kind {
	case lexer.TOKEN_LBRACE:
		return true
	case lexer.TOKEN_IDENT:
		i := p.pos + 2
		for p.hasToken(i+1) && (p.tokenAt(i).Kind == lexer.TOKEN_DOT || p.tokenAt(i).Kind == lexer.TOKEN_SCOPE) && p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT {
			i += 2
		}
		return p.hasToken(i) && p.tokenAt(i).Kind == lexer.TOKEN_LBRACE
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

// parseNotifyStmt recognizes the removed `notify one` / `notify all` condition-variable
// notification statement and rejects it. The sugar desugared to a raw `notify_one` /
// `notify_all` CondVar call, which the analyzer already rejects for user code as removed
// raw concurrency surface — so the statement form only ever produced a two-stage failure.
// Its wait counterpart (`cond_wait`) was likewise removed in favor of `predicate_wait`;
// structured `wait all` (task-group join) is a separate, retained construct. `notify`
// remains a usable ordinary identifier.
func (p *Parser) parseNotifyStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("notify")
	p.errorAt(pos, "the `notify one` / `notify all` statement has been removed; use `predicate_notify_one` / `predicate_notify_all` or a notifier tied to the protected-state predicate (structured `wait all` is unaffected)")
	_ = p.expect(lexer.TOKEN_IDENT) // `one` / `all`
	_ = p.parseExpr()
	p.expectNewline()
	return &ast.PassStmt{Position: pos}
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
		p.errorAt(pos, "`match?` is no longer supported; use `match ...:` with a `null` arm")
		p.skipRemovedQuestionStmtTail()
		return &ast.MatchStmt{Position: pos}
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
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "ok" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
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
	arms := p.parseMatchExprArms()
	return &ast.MatchExpr{Position: pos, Value: value, Store: store, Arms: arms}
}

// parseMatchExprArms parses the arms of a `match` used in expression position. Each arm is
// `Pattern: <value>` where <value> is a single inline expression that the arm yields (the whole
// match evaluates to the joined type of all arm values). For convenience an arm may also use an
// indented block body whose final statement is an expression — that form parses through the same
// statement-block machinery so all of statement-match's binding/narrowing semantics are reused. The
// arm Body is always normalized to a statement list ending in an ExprStmt, which is exactly what
// the analyzer (analyzeMatchExprArmBody) and backend (emitMatchExpr) already consume.
func (p *Parser) parseMatchExprArms() []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	arms := make([]ast.MatchArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMatchExprArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}

func (p *Parser) parseMatchExprArm() []ast.MatchArm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "case" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind != lexer.TOKEN_COLON {
		p.errorAt(pos, "match arms do not use a `case` keyword; write the pattern directly (e.g. `E.A(v: v):`)")
		p.advance()
	}
	alternatives := p.parseTopLevelMatchArmAlternatives()
	guard := p.parseOptionalMatchArmGuard()
	p.expect(lexer.TOKEN_COLON)
	var body []ast.Stmt
	if p.peek() == lexer.TOKEN_NEWLINE {
		// Indented block-body arm: reuse the statement-block parser; its final statement
		// supplies the arm value (enforced by the analyzer).
		p.expectNewline()
		// Convert a trailing `match`/`if` in the arm block into the arm's value, so nested
		// match-expressions compose (docs/119 §4). A plain expression tail is unchanged.
		body = p.valueBlockTail(p.parseBlock())
	} else {
		// Inline value arm: `Pattern: <expr>`. The value becomes the arm's yielded
		// expression. A top-level comma builds a TupleExpr so a tuple-bind can
		// destructure the match (`a, b = match k: 1: 10, 20`).
		value := p.parseExpr()
		if p.peek() == lexer.TOKEN_COMMA {
			elems := []ast.Expr{value}
			for p.match(lexer.TOKEN_COMMA) {
				elems = append(elems, p.parseExpr())
			}
			value = &ast.TupleExpr{Position: value.Pos(), Elems: elems}
		}
		p.expectNewline()
		body = []ast.Stmt{&ast.ExprStmt{Position: value.Pos(), Expr: value}}
	}
	arms := make([]ast.MatchArm, 0, len(alternatives))
	for _, alt := range alternatives {
		arms = append(arms, ast.MatchArm{Position: pos, Pattern: alt.pattern, Body: matchArmAlternativeBody(alt.withDecls, body), Guard: guard})
	}
	return arms
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
		return &ast.RegionStmt{Position: pos, Name: synthesizedAutoRegionName(pos), Lazy: true, UserAuto: true, Body: body}
	}
	return &ast.InStoreStmt{Position: pos, Store: store, Body: body}
}

// synthesizedAutoRegionName builds a unique, source-located name for an `in auto:`
// region. The reserved `__auto_` prefix plus the byte offset makes nested/sibling
// auto scopes distinct and avoids collision with any plausible user region name.
func synthesizedAutoRegionName(pos lexer.Pos) string {
	return fmt.Sprintf("__auto_%d", pos.Offset)
}

// maybeWrapFunctionBodyInAutoRegion implements inference-by-default: a function whose body
// contains a region-less allocation (a container local with no `@r` and no enclosing region
// scope) is wrapped in a compiler-synthesized lazy auto region, so the allocation just works
// without an explicit `region`/`in auto:`. The region is freed at function exit; an escaping
// value is still caught by the existing region escape checks. Functions that don't allocate
// region-lessly are left untouched (no overhead, no IR churn) — and since a bare allocation
// currently errors, no existing program is changed, only previously-rejected ones enabled.
func (p *Parser) maybeWrapFunctionBodyInAutoRegion(body []ast.Stmt, params []ast.ParamDecl, pos lexer.Pos) []ast.Stmt {
	// A function that explicitly receives an allocator (an `Arena` parameter) manages
	// allocation itself — its region-less container locals are hand-assembled headers
	// whose buffers it places via arena_alloc, not allocations needing inference. So
	// inference-by-default applies only to functions that DON'T thread an allocator.
	if hasArenaParam(params) || !functionBodyNeedsAutoRegion(body) {
		return body
	}
	return []ast.Stmt{&ast.RegionStmt{Position: pos, Name: synthesizedAutoRegionName(pos), Lazy: true, Body: body}}
}

// wrapReclaimableLoopBodies auto-tightens loops: a loop body whose fresh allocations are all
// iteration-local (and which doesn't grow an outer container) is wrapped in a synthesized lazy
// `in auto:` region, so those allocations are reclaimed each iteration (scope-exit free) instead
// of accumulating in the enclosing region for the whole loop. It is CONSERVATIVE — it leaves a
// loop untouched on any doubt. Safety: the escape checker remains the hard backstop (a too-eager
// wrap would surface as a compile error, never a use-after-free), and wrapping a body that turns
// out not to region-allocate is a harmless lazy no-op region.
func wrapReclaimableLoopBodies(stmts []ast.Stmt) []ast.Stmt {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			s.Body = tightenLoopBody(s.Body)
		case *ast.WhileStmt:
			s.Body = tightenLoopBody(s.Body)
		case *ast.IterForStmt:
			s.Body = tightenLoopBody(s.Body)
		case *ast.IfStmt:
			s.Then = wrapReclaimableLoopBodies(s.Then)
			s.Else = wrapReclaimableLoopBodies(s.Else)
		case *ast.CanStmt:
			s.Body = wrapReclaimableLoopBodies(s.Body)
		case *ast.ScopeStmt:
			s.Body = wrapReclaimableLoopBodies(s.Body)
		case *ast.MatchStmt:
			for i := range s.Arms {
				s.Arms[i].Body = wrapReclaimableLoopBodies(s.Arms[i].Body)
			}
		case *ast.RegionStmt:
			s.Body = wrapReclaimableLoopBodies(s.Body)
		case *ast.InStoreStmt:
			s.Body = wrapReclaimableLoopBodies(s.Body)
		}
	}
	return stmts
}

func tightenLoopBody(body []ast.Stmt) []ast.Stmt {
	body = wrapReclaimableLoopBodies(body) // tighten nested loops first
	if !loopBodyIsReclaimable(body) {
		return body
	}
	pos := body[0].Pos()
	return []ast.Stmt{&ast.RegionStmt{Position: pos, Name: synthesizedAutoRegionName(pos), Lazy: true, Body: body}}
}

// loopBodyIsReclaimable reports whether wrapping the loop body in `in auto:` is safe and useful:
// it has at least one top-level fresh region allocation, every such allocation is iteration-local,
// any growth of an outer container pushes only values that cannot reference iteration storage,
// and it is not already region/store-scoped.
func loopBodyIsReclaimable(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	if len(body) == 1 {
		switch b := body[0].(type) {
		case *ast.RegionStmt:
			if len(b.Body) != 0 {
				return false
			}
		case *ast.InStoreStmt:
			return false
		}
	}
	// Growing an OUTER container is fine: it grows in its own arena, not the per-iteration
	// region. Only growth with a value that may point into the iteration's storage blocks the
	// wrap (a 2M-iteration `local = [..]; out.push(local[3])` loop otherwise kept every
	// iteration's literal alive until function exit).
	// (Bodies the old outer-growth test already accepted keep their behavior unchanged.)
	if ast.LoopBodyGrowsOuterContainer(body) && ast.LoopBodyGrowsOuterContainerWithIterationData(body) {
		return false
	}
	found := false
	for _, stmt := range body {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		// A loop-local fresh allocation is either a container literal or a `new[auto]` struct — both
		// participate in the same per-iteration tightening when their lifetime is iteration-local.
		isContainer := isRegionlessContainerType(vd.Type) && isAllocatingLiteral(vd.Value)
		isAuto := isAutoAllocValue(vd.Value)
		if !isContainer && !isAuto {
			continue
		}
		if !ast.AllocationIsIterationLocal(vd.Name, body) {
			return false
		}
		found = true
	}
	return found
}

// hasArenaParam reports whether any parameter's type is an Arena (by value or by ref).
func hasArenaParam(params []ast.ParamDecl) bool {
	for _, param := range params {
		if typeMentionsArena(param.Type) {
			return true
		}
	}
	return false
}

func typeMentionsArena(typ ast.TypeExpr) bool {
	switch t := typ.(type) {
	case *ast.NamedType:
		return t.Name == "Arena"
	case *ast.MutableType:
		return typeMentionsArena(t.Elem)
	case *ast.RefType:
		return typeMentionsArena(t.Elem)
	case *ast.OwnedType:
		return typeMentionsArena(t.Elem)
	}
	return false
}
