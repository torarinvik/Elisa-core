package parser

import (
	"fmt"
	"reflect"
	"strconv"

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
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE, lexer.TOKEN_IF, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
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
			// A trailing `by par` / `by simd` data-parallel marker ends the range-bound expression
			// (e.g. the `n` in `for i in 0 ..< n by par:`). Stop the header scan at `by` so the loop
			// parser can see and consume the marker.
			if depth == 0 && tok.Text == "by" && end+1 < len(p.tokens) && p.tokens[end+1].Kind == lexer.TOKEN_IDENT {
				subTokens := append([]lexer.Token(nil), p.tokens[p.pos:end]...)
				subTokens = append(subTokens, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: tok.Pos})
				sub := New(subTokens)
				sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
				expr := sub.parseExpr()
				p.errors = append(p.errors, sub.Errors()...)
				p.pos = end
				return expr
			}
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
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "case" &&
		p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_COLON {
		p.errorAt(pos, "match arms do not use a `case` keyword; write the pattern directly (e.g. `E.A(v: v):`)")
		p.advance()
	}
	patterns := p.parseTopLevelMatchPatterns()
	p.expect(lexer.TOKEN_COLON)
	var body []ast.Stmt
	if p.peek() == lexer.TOKEN_NEWLINE {
		// Indented block-body arm: reuse the statement-block parser; its final statement
		// supplies the arm value (enforced by the analyzer).
		p.expectNewline()
		body = p.parseBlock()
	} else {
		// Inline value arm: `Pattern: <expr>`. The value becomes the arm's yielded expression.
		value := p.parseExpr()
		p.expectNewline()
		body = []ast.Stmt{&ast.ExprStmt{Position: value.Pos(), Expr: value}}
	}
	arms := make([]ast.MatchArm, 0, len(patterns))
	for _, pattern := range patterns {
		arms = append(arms, ast.MatchArm{Position: pos, Pattern: pattern, Body: body})
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
// it does not grow an outer container, and it is not already region/store-scoped.
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
	if ast.LoopBodyGrowsOuterContainer(body) {
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

// functionBodyNeedsAutoRegion reports whether a statement list contains a region-less
// allocation. It recurses through control flow but NOT into nested region scopes
// (`region NAME:` / `in <owner>:`), whose allocations already have an ambient owner.
func functionBodyNeedsAutoRegion(stmts []ast.Stmt) bool {
	if bodyContainsAutoAlloc(stmts) {
		return true
	}
	// A struct-constructed local whose address is forwarded to a call (`x = T(...)`/`x = Builder()`;
	// `grow(&x)`) has its region-less container fields grown INTERPROCEDURALLY by a (region-poly)
	// callee. The growth is invisible to the syntactic allocation checks above, so without an ambient
	// auto region the call site has no region to thread into the callee's `@r` struct-ref param —
	// the "cannot infer region parameter" gap. Synthesizing the region lets recordStructLocalAllocRegion
	// bind the local to it and the call thread it through; the escape checker remains the hard backstop.
	if bodyForwardsConstructedStructLocal(stmts) {
		return true
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.RegionStmt:
			if len(s.Body) != 0 {
				continue // scoped region owns its body's allocations
			}
		case *ast.InStoreStmt:
			continue // `in <owner>:` establishes an ambient owner
		case *ast.CanStmt:
			if functionBodyNeedsAutoRegion(s.Body) {
				return true
			}
		case *ast.IfStmt:
			if functionBodyNeedsAutoRegion(s.Then) || functionBodyNeedsAutoRegion(s.Else) {
				return true
			}
		case *ast.ForStmt:
			if functionBodyNeedsAutoRegion(s.Body) {
				return true
			}
		case *ast.IterForStmt:
			// `for x in coll:` (iterator form) — a container literal built inside such a loop that
			// the loop tightener leaves untightened (e.g. it forwards the container to a grower, so
			// it isn't iteration-local) still needs the function-body auto region as its home, just
			// like a ForStmt body. Missing this case left such a container with NO region at all.
			if functionBodyNeedsAutoRegion(s.Body) {
				return true
			}
		case *ast.WhileStmt:
			if functionBodyNeedsAutoRegion(s.Body) {
				return true
			}
		case *ast.ScopeStmt:
			if functionBodyNeedsAutoRegion(s.Body) {
				return true
			}
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				if functionBodyNeedsAutoRegion(arm.Body) {
					return true
				}
			}
		case *ast.VarDeclStmt:
			// Only an allocating literal initializer (`= []`, `= [a, b]`, `= {}`, a
			// comprehension) actually allocates and needs a region. A container local
			// assigned from a view/copy/call (`= other`) does not — excluding those avoids
			// false positives on inspection/forwarding code. Some builders (`[x for ...]`,
			// `each`) infer their darray result type from the expression itself, so they
			// should synthesize an auto region even when the local has no type annotation.
			if (s.Type == nil && (isInferredDArrayBuilder(s.Value) || blockLaterUsesUntypedListLiteralAsDArray(stmts, s.Name))) || (isRegionlessContainerType(s.Type) && isAllocatingLiteral(s.Value)) {
				return true
			}
			// A struct constructed with container-literal field arguments (`Bag([], [])`) allocates
			// those fields region-lessly exactly like a bare container local, so it likewise needs an
			// ambient auto region — both so the fields have a home and so a call site can thread that
			// region into a callee's struct-ref region param.
			if structLiteralAllocatesContainerArg(s.Value) {
				return true
			}
		case *ast.ReturnStmt:
			// A function that directly `return`s a region-less COMPREHENSION
			// (`return [x for x in xs]`) or an allocating `each` QUERY
			// (`return X{...} for each v in src`) needs a synthesized auto region so the
			// builder has an active scope — without it the scope check rejects the body
			// before the region-poly classifier (which adopts the region into the caller)
			// ever runs. A directly-returned plain list LITERAL (`return [a, ...rest]`)
			// does NOT need this: it allocates region-poly without a scope check, and
			// wrapping it would only change its parse shape, so it is deliberately
			// excluded here.
			if rv := s.Value; rv != nil {
				for {
					paren, ok := rv.(*ast.ParenExpr)
					if !ok {
						break
					}
					rv = paren.Inner
				}
				switch lit := rv.(type) {
				case *ast.ListComprehensionExpr:
					if lit.Owner == nil {
						return true
					}
				case *ast.QueryExpr:
					if lit.Kind == ast.QueryExprEach && lit.Owner == nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// bodyForwardsConstructedStructLocal reports whether the body declares a local from a struct
// construction (`x: T = T(...)` or `x = Builder()` — both parse as *ast.StructLitExpr with
// Brace==false, as does the brace form `T{...}`) and later passes `&x` (or the bare `x`) as a call
// argument, including as a UFCS receiver (`x.grow(...)`). That local's region-less container fields
// are grown interprocedurally by the callee, so the function needs an ambient auto region for the call
// to thread into the callee's struct-ref region param. CONSERVATIVE + SAFE: a non-allocating match
// gets a harmless lazy no-op region and the escape checker is the hard backstop against a too-eager wrap.
func unwrapParenExprAST(e ast.Expr) ast.Expr {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok || paren == nil {
			return e
		}
		e = paren.Inner
	}
}

func bodyForwardsConstructedStructLocal(stmts []ast.Stmt) bool {
	constructed := map[string]bool{}
	var collect func(v reflect.Value)
	collect = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if vd, ok := v.Interface().(*ast.VarDeclStmt); ok && vd != nil && vd.Name != "" {
				if _, isLit := unwrapParenExprAST(vd.Value).(*ast.StructLitExpr); isLit {
					constructed[vd.Name] = true
				}
			}
			collect(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			collect(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				collect(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				collect(v.Index(i))
			}
		}
	}
	collect(reflect.ValueOf(stmts))
	if len(constructed) == 0 {
		return false
	}
	argForwardsConstructed := func(args []ast.Expr) bool {
		for _, arg := range args {
			inner := unwrapParenExprAST(arg)
			if addr, ok := inner.(*ast.AddrOfExpr); ok && addr != nil {
				inner = unwrapParenExprAST(addr.Operand)
			}
			if id, ok := inner.(*ast.Ident); ok && id != nil && constructed[id.Name] {
				return true
			}
		}
		return false
	}
	found := false
	var scan func(v reflect.Value)
	scan = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if expr, ok := v.Interface().(ast.Expr); ok {
				_, args, isCall := ast.PrepassCallShape(expr)
				if isCall && argForwardsConstructed(args) {
					found = true
					return
				}
			}
			scan(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			scan(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				scan(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				scan(v.Index(i))
			}
		}
	}
	scan(reflect.ValueOf(stmts))
	return found
}

// isAutoAllocValue reports whether an initializer is a `new[auto] T(...)` allocation — a fresh
// inferred-region heap allocation, the AllocExpr analogue of a container literal. It makes the
// region-synthesis treat new[auto] exactly like a container: its lifetime is detected (function
// scope vs per-iteration) and it is placed in the matching inferred region, no explicit `in auto:`.
func isAutoAllocValue(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	alloc, ok := value.(*ast.AllocExpr)
	return ok && alloc != nil && (alloc.AutoRegion || alloc.Owner == nil)
}

// bodyContainsAutoAlloc reports whether any statement in the body contains a `new[auto]` allocation
// anywhere. Used to trigger the function-scope auto region (an over-wrap — e.g. a new[auto] nested
// in its own region — is a harmless lazy no-op outer region).
func bodyContainsAutoAlloc(stmts []ast.Stmt) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if alloc, ok := v.Interface().(*ast.AllocExpr); ok && alloc != nil && (alloc.AutoRegion || alloc.Owner == nil) {
				found = true
				return
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
	return found
}

// structLiteralAllocatesContainerArg reports whether value is a struct construction
// (`Bag([], [])`) one of whose arguments is an allocating container literal. Such a struct
// allocates region-less container fields and needs an ambient auto region, the same as a bare
// container local.
func structLiteralAllocatesContainerArg(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	lit, ok := value.(*ast.StructLitExpr)
	if !ok || lit == nil {
		return false
	}
	for _, arg := range lit.Args {
		if isAllocatingLiteral(arg) {
			return true
		}
	}
	return false
}

// isAllocatingLiteral reports whether an initializer is a container literal or
// builder — the forms that actually allocate a backing buffer.
func isAllocatingLiteral(value ast.Expr) bool {
	switch value.(type) {
	case *ast.ListLitExpr, *ast.ListComprehensionExpr:
		return true
	}
	return isInferredDArrayBuilder(value)
}

// isInferredDArrayBuilder reports whether value is an expression that constructs
// a darray without needing a declared container type as context.
func isInferredDArrayBuilder(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch v := value.(type) {
	case *ast.ListComprehensionExpr:
		return true
	case *ast.QueryExpr:
		return v.Kind == ast.QueryExprEach
	}
	return false
}

func blockLaterUsesUntypedListLiteralAsDArray(stmts []ast.Stmt, name string) bool {
	if name == "" {
		return false
	}
	seenDecl := false
	for _, stmt := range stmts {
		if !seenDecl {
			decl, ok := stmt.(*ast.VarDeclStmt)
			if !ok || decl.Name != name {
				continue
			}
			seenDecl = decl.Type == nil && isListLiteralExpr(decl.Value)
			continue
		}
		if stmtUsesDArrayBuilderLocal(stmt, name) {
			return true
		}
		if decl, ok := stmt.(*ast.VarDeclStmt); ok && decl.Name == name {
			return false
		}
	}
	return false
}

func isListLiteralExpr(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	_, ok := value.(*ast.ListLitExpr)
	return ok
}

func stmtUsesDArrayBuilderLocal(stmt ast.Stmt, name string) bool {
	found := false
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if found || expr == nil {
			return
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if field, ok := e.Func.(*ast.FieldExpr); ok && field != nil {
				if ident, ok := field.Object.(*ast.Ident); ok && ident.Name == name {
					switch field.Field {
					case "push", "extend", "reserve", "resize", "clear", "truncate", "as_sview", "as_cstr":
						found = true
						return
					}
				}
			}
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.FieldExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				switch e.Field {
				case "count", "capacity", "items":
					found = true
					return
				}
			}
			visitExpr(e.Object)
		case *ast.IndexExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				found = true
				return
			}
			visitExpr(e.Object)
			visitExpr(e.Index)
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		case *ast.UnaryExpr:
			visitExpr(e.Operand)
		case *ast.ParenExpr:
			visitExpr(e.Inner)
		}
	}
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			visitStmt(st)
		}
	}
	// Descend into nested control-flow BODIES, not just their headers: the canonical builder
	// shape fills the local inside a loop (`out = []; while ...: out.push(x)`). Without this
	// the auto-region wrap would miss a builder whose only use is loop-nested, leaving the
	// empty literal with no ambient region. Mirrors the semantic-side use-scan recursion.
	visitStmt = func(st ast.Stmt) {
		if found || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.VarDeclStmt:
			visitExpr(s.Value)
		case *ast.AssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.AugAssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.IfStmt:
			visitExpr(s.Cond)
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitExpr(elif.Cond)
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.ForStmt:
			visitExpr(s.Start)
			visitExpr(s.End)
			visitExpr(s.Step)
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitExpr(s.Source)
			visitExpr(s.Filter)
			visitStmts(s.Body)
		case *ast.WhileStmt:
			visitExpr(s.Cond)
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}

// isRegionlessContainerType reports whether a declared type is a growable container
// builtin with no `@r` region annotation — the signal that it will allocate and needs
// an ambient region.
func isRegionlessContainerType(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	// `dstr` is an owned string (the u8 specialization of darray, docs/26) but parses
	// as a NamedType, not a BuiltinTypeExpr — so a region-less `out: dstr = []` builder
	// must be recognized here too, or it never gets its inferred region (the auto-wrap
	// is what lets a string builder `push` bytes without an explicit `in <arena>:`).
	if nt, ok := typ.(*ast.NamedType); ok {
		return nt.Name == "dstr"
	}
	bt, ok := typ.(*ast.BuiltinTypeExpr)
	if !ok {
		return false
	}
	switch bt.Name {
	case "darray", "dict", "set", "dstr":
		return bt.Region == ""
	}
	return false
}

// desugarDStrStringLiteralInit rewrites `s: dstr = "abc"` into `s: dstr = [97, 98, 99]` — the
// byte-list literal a user would otherwise hand-write (`['a'.u8(), ...]`). `dstr` is the u8
// specialization of darray, so a bare string literal (otherwise a static `cstr`) does not assign to
// it. Desugaring at parse time — before functionBodyNeedsAutoRegion runs — means the result is an
// ordinary allocating list literal: it triggers the inferred auto-region wrap and flows through the
// identical growable-darray path (region backing, escape checks, codegen), adding no new surface.
//
// StringLit.Value is the already-decoded raw byte sequence (escapes resolved by the lexer; the
// backend emits these bytes verbatim), so each byte becomes a suffix-less IntLit whose type is
// driven to u8 by the darray[u8] list-literal context (no discouraged-suffix warning). An empty
// string yields an empty `[]`. The value is only rewritten when the declared type is a region-less
// `dstr`; any other type (including a `cstr`/view) is left untouched.
func desugarDStrStringLiteralInit(typ ast.TypeExpr, value ast.Expr) ast.Expr {
	if value == nil || !typeExprIsRegionlessDStr(typ) {
		return value
	}
	lit, ok := value.(*ast.StringLit)
	if !ok {
		return value
	}
	elems := make([]ast.Expr, 0, len(lit.Value))
	for i := 0; i < len(lit.Value); i++ {
		elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
	}
	return &ast.ListLitExpr{Position: lit.Position, Elems: elems}
}

// desugarDStrReturnLiterals rewrites every `return "..."` in a function body whose declared return
// type is a region-less `dstr` into the equivalent byte-list literal (`return [97, ...]`). Like the
// var-decl desugar, this must run at parse time: the region-polymorphic-return classification is a
// syntactic pre-pass that keys on a directly-returned list literal, so the StringLit must already be
// a list literal before it runs. Recurses through statement-level control flow (returns anywhere in
// the body still return from this function) but not into expressions, so a nested lambda's own
// returns — governed by a different return type — are untouched.
func desugarDStrReturnLiterals(body []ast.Stmt, retType ast.TypeExpr) {
	if !typeExprIsRegionlessDStr(retType) {
		return
	}
	rewriteDStrReturnsIn(body)
}

func rewriteDStrReturnsIn(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if lit, ok := s.Value.(*ast.StringLit); ok {
				elems := make([]ast.Expr, 0, len(lit.Value))
				for i := 0; i < len(lit.Value); i++ {
					elems = append(elems, &ast.IntLit{Position: lit.Position, Value: strconv.Itoa(int(lit.Value[i]))})
				}
				s.Value = &ast.ListLitExpr{Position: lit.Position, Elems: elems}
			}
		case *ast.CanStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.RegionStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.InStoreStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.ScopeStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.IfStmt:
			rewriteDStrReturnsIn(s.Then)
			rewriteDStrReturnsIn(s.Else)
		case *ast.ForStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.WhileStmt:
			rewriteDStrReturnsIn(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				rewriteDStrReturnsIn(arm.Body)
			}
		}
	}
}

// typeExprIsRegionlessDStr reports whether typ is the region-less owned `dstr` string (a NamedType,
// optionally behind `mutable`). cstr/views and annotated `dstr @r` are excluded.
func typeExprIsRegionlessDStr(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	nt, ok := typ.(*ast.NamedType)
	return ok && nt.Name == "dstr"
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

// parseCascadeStmt recognizes the removed `cascade <target>:` statement and rejects it. The cascade
// feature (method-cascade sugar) has been removed; the target and body are consumed for recovery.
func (p *Parser) parseCascadeStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("cascade")
	p.errorAt(pos, "the `cascade` statement has been removed")
	_ = p.parseExpr()
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		_ = p.parseBlock()
	}
	return &ast.PassStmt{Position: pos}
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
	// A leading `case` is a common habit from other languages; Elisa match arms are
	// the pattern itself. Diagnose it once and skip the keyword so the arm (pattern,
	// colon, body) still parses normally instead of cascading recovery errors.
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "case" &&
		p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_COLON {
		p.errorAt(pos, "match arms do not use a `case` keyword; write the pattern directly (e.g. `E.A(v: v):`)")
		p.advance()
	}
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
	// docs/77 §2 category arm with binder: `Statement s:` — two bare identifiers. Only the
	// top-level arm position accepts the binder; nested payload patterns stay single-ident.
	if bind, ok := pattern.(*ast.MatchBindPattern); ok && bind != nil && bind.Binder == "" &&
		p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "_" {
		bind.Binder = p.advance().Text
	}
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
	// A `::` or `.` after the first segment introduces a qualified variant path:
	// `Color.Red`, `M::Color.Red` (module-qualified). `::` namespaces the module
	// while `.` selects the variant; both join into the canonical dotted name.
	if !p.matchQualifiedNameSeparator() {
		return &ast.MatchBindPattern{Position: pos, Name: parts[0]}
	}
	parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	for p.matchQualifiedNameSeparator() {
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
