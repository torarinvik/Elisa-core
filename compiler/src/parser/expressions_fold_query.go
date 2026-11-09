package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// parseFoldComprehensionTail parses `for name in source [if filter] with acc [:T] = seed )`
// and lowers the whole fold — purely syntactically — into an ExprBlock so no new AST node or
// analysis/backend dispatch is needed (docs/79 Phase 1):
//
//	{ acc: mutable T = seed;  for name in source: (bindings...; if filter:) acc <- body;  acc }
//
// The filter is emitted as an in-body `if` (not the iterator's Filter field) so head bindings
// are in scope for it.
func (p *Parser) parseFoldComprehensionTail(pos lexer.Pos, bindings []ast.Stmt, body ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	var rangeEnd ast.Expr
	var rangeStep ast.Expr
	rangeOp := lexer.TOKEN_EOF
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT || p.peek() == lexer.TOKEN_RANGE_LE {
		rangeOp, _ = p.consumeValueRangeOp()
		rangeEnd = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		if p.match(lexer.TOKEN_RANGE) {
			rangeStep = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	}
	p.expect(lexer.TOKEN_WITH)
	accName := p.expect(lexer.TOKEN_IDENT).Text
	var accType ast.TypeExpr
	if p.match(lexer.TOKEN_COLON) {
		accType = p.parseTypeExprWithoutErrorUnionSuffix()
	}
	p.expect(lexer.TOKEN_ASSIGN)
	accInit := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	// Optional `by par` marker (docs/79 Part IV): lowers to a contention-free parallel reduction over
	// the source's disjoint bands. `by simd` was removed — a fold's reduction order is defined as a
	// vectorizable tree reduction by default (its accumulator update always carries reassociation).
	byMode := ""
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		byMode = p.expect(lexer.TOKEN_IDENT).Text
		switch byMode {
		case "simd":
			p.errorAt(byPos, "`by simd` was removed; a fold reduces in a vectorizable tree order by default (use `-ffast-math` only for further FP relaxation)")
			byMode = ""
		case "par":
			// handled below
		default:
			p.errorAt(byPos, "expected `par` after `by`, got %q", byMode)
		}
	}
	p.expect(lexer.TOKEN_RPAREN)

	if byMode == "par" {
		return p.buildParallelFoldReduce(pos, bindings, body, name, accName, source, rangeEnd, filter, accInit)
	}

	// Phase 2 (docs/79 Part IV): when the fold's shape lets us pin the reduction tree, emit a
	// fixed-width pinned reduction whose bracketing is fixed in the source — bit-reproducible across
	// every target and optimization level. Ineligible shapes fall through to the reassociating scalar
	// loop below (still vectorizes per-target, but its exact bits depend on the vectorizer's choices).
	if pinned, ok := p.buildPinnedFoldReduce(pos, bindings, body, name, accName, accType, source, rangeEnd, filter, accInit); ok {
		return pinned
	}

	// The accumulator update reassociates by default: comprehension folds are defined to reduce in a
	// vectorizable tree order, not strict left-to-right (docs/79 Part IV). For integer accumulators
	// this is a no-op (integer add/mul are associative); for FP it permits SIMD tree reduction.
	assign := ast.Stmt(&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: accName}, Value: body, FastMath: true})
	loopBody := append([]ast.Stmt{}, bindings...)
	if filter != nil {
		loopBody = append(loopBody, &ast.IfStmt{Position: pos, Cond: filter, Then: []ast.Stmt{assign}})
	} else {
		loopBody = append(loopBody, assign)
	}
	var loopStmt ast.Stmt
	if rangeEnd != nil {
		// A range fold falls back to the reassociating scalar loop (it cannot use the pinned path,
		// which needs an indexable darray). With no filter and a call-free body it is still shaped to
		// vectorize via reassociation, so mark it for the -Wperf auto-vectorization verifier — a range
		// reduction that fails to vectorize is a real perf defect worth surfacing. A filter or a body
		// call legitimately blocks vectorization, so those stay unmarked (no noise).
		autovec := filter == nil && foldBodyCallFree(body, bindings)
		loopStmt = &ast.ForStmt{Position: pos, Name: name, Start: source, End: rangeEnd, Step: rangeStep, Op: rangeOp, Body: loopBody, AutovecExpected: autovec, AutovecReason: "fold reduction"}
	} else {
		loopStmt = &ast.IterForStmt{Position: pos, Pattern: &ast.MoveBindNamePattern{Position: pos, Name: name}, Mode: ast.IterBindValue, Source: source, Body: loopBody}
	}
	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: accName, Mutable: true, Type: accType, Value: accInit},
			loopStmt,
		},
		Value: &ast.Ident{Position: pos, Name: accName},
	}
}

// buildParallelFoldReduce lowers a `by par` fold to a contention-free parallel reduction over the
// source's disjoint bands, by desugaring to the runtime `map_reduce(slice(&src), seed, transform,
// op)` combinator (each worker folds `transform(element)` values into a private partial, then the
// partials are merged with `op` — verified race-free and partition-independent). Because a parallel
// reduction reorders the combine, `by par` is gated to an associative-combine fold whose body is
// `acc <op> f(x)` (op ∈ {+, *}); the accumulator must appear as one whole operand of that top-level
// op, and the other operand `f(x)` is the per-element transform. Anything else (filter, head
// bindings, range source, non-identifier source, or a non-associative top-level op) is a parser
// error so the parallel contract is never silently violated.
func (p *Parser) buildParallelFoldReduce(pos lexer.Pos, bindings []ast.Stmt, body ast.Expr, name string, accName string, source ast.Expr, rangeEnd ast.Expr, filter ast.Expr, accInit ast.Expr) ast.Expr {
	fail := func(reason string) ast.Expr {
		p.errorAt(pos, "`by par` fold %s; it must be `( acc <op> f(%s) for %s in <darray> with acc = seed by par )` with an associative `op` (+ or *)", reason, name, name)
		return accInit // keep a valid expression so parsing continues
	}
	if len(bindings) > 0 {
		return fail("does not support head bindings")
	}
	if filter != nil {
		return fail("does not support an `if` filter")
	}
	if rangeEnd != nil {
		return fail("requires a darray source, not a range")
	}
	srcIdent, ok := source.(*ast.Ident)
	if !ok {
		return fail("source must be a plain darray variable")
	}
	bin, ok := body.(*ast.BinaryExpr)
	if !ok || (bin.Op != lexer.TOKEN_PLUS && bin.Op != lexer.TOKEN_STAR) {
		return fail("body must combine with an associative operator (+ or *)")
	}
	// The accumulator must be one whole operand of the top-level op; the other operand is the
	// per-element transform `f(x)`. Accept either order (op is commutative). For a bare `acc <op> x`
	// the transform is just `x` (identity).
	var transformExpr ast.Expr
	switch {
	case identName(bin.Left) == accName:
		transformExpr = bin.Right
	case identName(bin.Right) == accName:
		transformExpr = bin.Left
	default:
		return fail("the accumulator `" + accName + "` must be one whole operand of the top-level `<op>`")
	}

	// map_reduce(slice(&src), seed, (x) => f(x), (l, r) => l <op> r)
	sliceCall := &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "slice"}, Args: []ast.Expr{&ast.AddrOfExpr{Position: pos, Operand: srcIdent}}}
	transformLambda := &ast.LambdaExpr{
		Position:            pos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: pos, Name: name}},
		BodyExpr:            transformExpr,
	}
	opLambda := &ast.LambdaExpr{
		Position:            pos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: pos, Name: "__par_l"}, {Position: pos, Name: "__par_r"}},
		BodyExpr:            &ast.BinaryExpr{Position: pos, Op: bin.Op, Left: &ast.Ident{Position: pos, Name: "__par_l"}, Right: &ast.Ident{Position: pos, Name: "__par_r"}},
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "map_reduce"},
		Args:     []ast.Expr{sliceCall, accInit, transformLambda, opLambda},
	}
}

// pinnedFoldWidth is the fixed reduction lane count. The fold's tree bracketing is pinned to this
// width in the desugared source (W independent strict accumulators + a fixed strict pairwise combine),
// so the rounded result is bit-identical across every target and optimization level — the loop
// vectorizer may pack the W independent lanes into a SIMD register, but it can never reorder them
// (no reassociation flag rides on these ops), so the bits do not depend on its choices.
const pinnedFoldWidth = 8

// buildPinnedFoldReduce lowers an eligible fold to a fixed-width pinned reduction (docs/79 Part IV,
// Phase 2). Eligibility mirrors `by par` (no head bindings / filter / range; a plain darray source; an
// associative top-level op with the accumulator as one whole operand) plus a numeric-literal seed (so
// the per-lane identity is synthesizable) and a clonable per-element transform. The second return is
// false when any condition fails; the caller then falls back to the reassociating scalar loop. Unlike
// `by par` an ineligible shape is NOT an error — pinning is a best-effort reproducibility upgrade over
// the always-correct scalar default.
func (p *Parser) buildPinnedFoldReduce(pos lexer.Pos, bindings []ast.Stmt, body ast.Expr, name string, accName string, accType ast.TypeExpr, source ast.Expr, rangeEnd ast.Expr, filter ast.Expr, accInit ast.Expr) (ast.Expr, bool) {
	if len(bindings) != 0 || filter != nil || rangeEnd != nil {
		return nil, false
	}
	srcIdent, ok := source.(*ast.Ident)
	if !ok {
		return nil, false
	}
	bin, ok := body.(*ast.BinaryExpr)
	if !ok || (bin.Op != lexer.TOKEN_PLUS && bin.Op != lexer.TOKEN_STAR) {
		return nil, false
	}
	var transformExpr ast.Expr
	switch {
	case identName(bin.Left) == accName:
		transformExpr = bin.Right
	case identName(bin.Right) == accName:
		transformExpr = bin.Left
	default:
		return nil, false
	}
	// The per-lane identity must be a typed literal we can synthesize; only a bare numeric-literal seed
	// tells us the kind (int vs float) without type information at parse time.
	isMul := bin.Op == lexer.TOKEN_STAR
	isFloat := false
	switch accInit.(type) {
	case *ast.FloatLit:
		isFloat = true
	case *ast.IntLit:
		isFloat = false
	default:
		return nil, false
	}
	// The transform is spliced once per lane; it must be deep-clonable so the lanes never share a node.
	if ast.CloneExpr(transformExpr) == nil {
		return nil, false
	}

	id := func(s string) ast.Expr { return &ast.Ident{Position: pos, Name: s} }
	ilit := func(v string) ast.Expr { return &ast.IntLit{Position: pos, Value: v} }
	op := func(l, r ast.Expr) ast.Expr { return &ast.BinaryExpr{Position: pos, Op: bin.Op, Left: l, Right: r} }
	srcAt := func(ix ast.Expr) ast.Expr { return &ast.IndexExpr{Position: pos, Object: id(srcIdent.Name), Index: ix} }
	transform := func() ast.Expr { return ast.CloneExpr(transformExpr) }
	assign := func(target, val ast.Expr) ast.Stmt {
		return &ast.AssignStmt{Position: pos, Target: target, Value: val}
	}
	identityLit := func() ast.Expr {
		v := "0"
		if isMul {
			v = "1"
		}
		if isFloat {
			return &ast.FloatLit{Position: pos, Value: v + ".0"}
		}
		return ilit(v)
	}

	const (
		nName = "__fold_n"
		iName = "__fold_i"
	)
	laneNames := []string{"__fold_a0", "__fold_a1", "__fold_a2", "__fold_a3", "__fold_a4", "__fold_a5", "__fold_a6", "__fold_a7"}
	wLit := func() ast.Expr { return ilit("8") }

	// --- n >= W branch: W independent strict lanes, unrolled by W, then a strict pairwise combine. ---
	then := []ast.Stmt{
		// Reusable element binding (mutable). Valid to seed from src[0] because this branch needs n >= W.
		&ast.VarDeclStmt{Position: pos, Name: name, Mutable: true, Value: srcAt(ilit("0"))},
	}
	for j := 0; j < pinnedFoldWidth; j++ {
		seed := identityLit()
		if j == 0 {
			seed = ast.CloneExpr(accInit) // lane 0 carries the seed; the combine folds it in exactly once
		}
		then = append(then, &ast.VarDeclStmt{Position: pos, Name: laneNames[j], Mutable: true, Value: seed})
	}
	then = append(then, &ast.VarDeclStmt{Position: pos, Name: iName, Mutable: true, Type: &ast.NamedType{Position: pos, Name: "usize"}, Value: ilit("0")})

	mainBody := []ast.Stmt{}
	for j := 0; j < pinnedFoldWidth; j++ {
		// element <- src[i + j]; laneJ <- laneJ <op> transform(element)
		offset := ast.Expr(id(iName))
		if j != 0 {
			offset = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: ilit(itoa(j))}
		}
		mainBody = append(mainBody,
			assign(id(name), srcAt(offset)),
			assign(id(laneNames[j]), op(id(laneNames[j]), transform())),
		)
	}
	mainBody = append(mainBody, assign(id(iName), &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: wLit()}))
	// while i + W <= n
	then = append(then, &ast.WhileStmt{
		Position: pos,
		Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LTEQ, Left: &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: wLit()}, Right: id(nName)},
		Body:     mainBody,
	})
	// remainder: while i < n  { element <- src[i]; lane0 <- lane0 <op> transform; i <- i + 1 }
	then = append(then, &ast.WhileStmt{
		Position: pos,
		Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: id(iName), Right: id(nName)},
		Body: []ast.Stmt{
			assign(id(name), srcAt(id(iName))),
			assign(id(laneNames[0]), op(id(laneNames[0]), transform())),
			assign(id(iName), &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: id(iName), Right: ilit("1")}),
		},
	})
	// Strict pairwise combine: ((a0 a1)(a2 a3)) op ((a4 a5)(a6 a7)) — fixed bracketing, no reassociation.
	tree := op(
		op(op(id(laneNames[0]), id(laneNames[1])), op(id(laneNames[2]), id(laneNames[3]))),
		op(op(id(laneNames[4]), id(laneNames[5])), op(id(laneNames[6]), id(laneNames[7]))),
	)
	then = append(then, assign(id(accName), tree))

	// --- n < W branch: strict scalar fold into the (already seeded) result; reproducible at small n. ---
	els := []ast.Stmt{
		&ast.IterForStmt{
			Position: pos,
			Pattern:  &ast.MoveBindNamePattern{Position: pos, Name: name},
			Mode:     ast.IterBindValue,
			Source:   id(srcIdent.Name),
			Body:     []ast.Stmt{assign(id(accName), op(id(accName), transform()))},
		},
	}

	block := &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: nName, Value: &ast.FieldExpr{Position: pos, Object: id(srcIdent.Name), Field: "count"}},
			&ast.VarDeclStmt{Position: pos, Name: accName, Mutable: true, Type: accType, Value: ast.CloneExpr(accInit)},
			&ast.IfStmt{
				Position: pos,
				Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_GTEQ, Left: id(nName), Right: wLit()},
				Then:     then,
				Else:     els,
			},
		},
		Value: id(accName),
	}
	return block, true
}

// itoa renders a small non-negative int as a decimal string (lane offsets 0..7), avoiding an fmt
// import in this parser file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// foldBodyCallFree reports whether a fold's body expression and its head-binding values are all
// call-free — the precondition for marking the fold loop AutovecExpected (a call legitimately blocks
// auto-vectorization, so flagging it would be -Wperf noise rather than a real defect).
func foldBodyCallFree(body ast.Expr, bindings []ast.Stmt) bool {
	if ast.ExprContainsCall(body) {
		return false
	}
	for _, b := range bindings {
		if vd, ok := b.(*ast.VarDeclStmt); ok && ast.ExprContainsCall(vd.Value) {
			return false
		}
	}
	return true
}

// identName returns the name of an Ident expression, or "" if expr is not a plain identifier.
func identName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func (p *Parser) parseQueryExpr() ast.Expr {
	pos := p.cur().Pos
	kindText := p.expect(lexer.TOKEN_IDENT).Text
	kind := ast.QueryExprAny
	switch kindText {
	case "any":
		kind = ast.QueryExprAny
	case "all":
		kind = ast.QueryExprAll
	case "first":
		kind = ast.QueryExprFirst
	case "count":
		kind = ast.QueryExprCount
	case "each":
		kind = ast.QueryExprEach
	case "sum":
		kind = ast.QueryExprSum
	case "product":
		kind = ast.QueryExprProduct
	case "min":
		kind = ast.QueryExprMin
	case "max":
		kind = ast.QueryExprMax
	default:
		p.errorAt(pos, "unknown query expression %q", kindText)
	}
	name, pattern := p.parseQueryBindPattern()
	p.expect(lexer.TOKEN_IN)
	source := p.withInMembershipDisabled(func() ast.Expr {
		return p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	})
	var patternFilter ast.MatchPattern
	var patternFilterSubject string
	var filter ast.Expr
	// `where` is mandatory for the predicate/projection queries but optional for the monoid reducers
	// (`sum x in xs` / `product x in xs` with no filter is the common case).
	reducer := kind == ast.QueryExprSum || kind == ast.QueryExprProduct || kind == ast.QueryExprMin || kind == ast.QueryExprMax
	if !reducer || (p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "where") {
		p.expectIdentText("where")
		if subject, ok := p.peekQueryWhereSubjectPattern(name, pattern); ok {
			patternFilterSubject = subject
			p.expect(lexer.TOKEN_IDENT)
			p.expect(lexer.TOKEN_IS)
			patternFilter = p.parseMatchPattern()
			if p.match(lexer.TOKEN_COLON) {
				filter = p.parseExpr()
			}
		} else if p.peekWhereViewPatternFilter() {
			patternFilter = p.parseMatchPattern()
			if p.match(lexer.TOKEN_COLON) {
				filter = p.parseExpr()
			}
		} else {
			filter = p.parseExpr()
		}
	}
	var owner ast.Expr
	if p.match(lexer.TOKEN_WITH) {
		owner = p.withInMembershipDisabled(p.parseExpr)
	}
	return &ast.QueryExpr{Position: pos, Kind: kind, Name: name, Pattern: pattern, Source: source, Filter: filter, PatternFilter: patternFilter, PatternFilterSubject: patternFilterSubject, Owner: owner}
}
func (p *Parser) looksLikeQueryExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+3) {
		return false
	}
	switch p.cur().Text {
	case "any", "all", "first", "count", "each", "sum", "product", "min", "max":
	default:
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	index := p.pos + 2
	for p.hasToken(index) && p.tokenAt(index).Kind == lexer.TOKEN_COMMA {
		index++
		if !p.hasToken(index) || p.tokenAt(index).Kind != lexer.TOKEN_IDENT {
			return false
		}
		index++
	}
	return p.hasToken(index) && p.tokenAt(index).Kind == lexer.TOKEN_IN
}
