package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func isPostfixShorthandCastTarget(name string) bool {
	switch name {
	case "void", "bool", "char", "int",
		"i8", "i16", "i32", "i64", "isize",
		"s8", "s16", "s32", "s64",
		"u8", "u16", "u32", "u64", "usize", "uintptr",
		"f32", "f64",
		// lowercase builtin string types: enable the postfix-shorthand cast
		// (`bytes.sview()` / `bytes.cstr?()`) so a __cast__ hook (e.g. raw
		// C-string -> view) can drive ergonomic string content equality.
		"sview", "cstr":
		return true
	}
	if name == "" {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

func isConstantStyleArraySizeIdentifier(name string) bool {
	if name == "" {
		return false
	}
	hasUnderscore := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
			hasUnderscore = true
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return hasUnderscore
}

func isSingleUppercaseIdentifier(name string) bool {
	return len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z'
}

// peekOwnedQualifier reports whether the cursor is the `owned` ownership
// qualifier in type position (e.g. `owned Arena`). It only triggers when a
// type plausibly follows, so a value/type incidentally named "owned" is not
// misread.
func (p *Parser) peekOwnedQualifier() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "owned" {
		return false
	}
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_TAIL, lexer.TOKEN_LPAREN:
		return true
	default:
		return false
	}
}

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	if p.peekOwnedQualifier() {
		p.advance()
		elem := p.parseTypeExpr()
		return &ast.OwnedType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_MUTABLE) {
		elem := p.parseTypeExpr()
		return &ast.MutableType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_TAIL) {
		elem := p.parseTypeExpr()
		return &ast.TailType{Position: elem.Pos(), Elem: elem}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "func" {
		return p.parseFuncTypeExpr()
	}
	var typ ast.TypeExpr
	if p.peek() == lexer.TOKEN_LPAREN {
		typ = p.parseTupleTypeExpr()
	} else {
		storage, explicit, label, region := p.parseRefStorageQualifier()
		typ = p.parseBaseType(storage, explicit, label, region)
	}
	// Explicit `@r` region-provenance suffix (docs/68 §5): the canonical use-site
	// notation on a container (`darray[T] @r`) or a reference (`T& @r`, `T&? @r`).
	// On a reference it is equivalent to the legacy region prefix `r T&` -- both
	// populate the same Region field, so the analyzer treats them identically.
	if p.peek() == lexer.TOKEN_AT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		p.advance() // '@'
		regionName := p.advance().Text
		switch t := typ.(type) {
		case *ast.BuiltinTypeExpr:
			if t != nil {
				t.Region = regionName
			}
		case *ast.GenericType:
			if t != nil {
				t.Region = regionName
			}
		case *ast.RefType:
			if t != nil {
				if t.Region != "" {
					p.errorf("region given twice: prefix `%s` and suffix `@%s` on the same reference", t.Region, regionName)
				}
				t.Region = regionName
			}
		default:
			p.errorf("region annotation `@%s` is only supported on container, generic, and reference types", regionName)
		}
	}
	if p.match(lexer.TOKEN_PIPE) {
		errType := p.parseTypeExpr()
		p.errorf("legacy fallible return syntax `T | ErrorSet` is no longer supported; use `T error[SomeSet]` instead")
		return &ast.ErrorUnionTypeExpr{Position: typ.Pos(), Value: typ, Errors: errType}
	}
	if p.peek() == lexer.TOKEN_ERROR {
		errType := p.parseErrorSetExpr()
		return &ast.ErrorUnionTypeExpr{Position: typ.Pos(), Value: typ, Errors: errType}
	}
	return typ
}
func (p *Parser) parseTypeExprWithoutErrorUnionSuffix() ast.TypeExpr {
	if p.peekOwnedQualifier() {
		p.advance()
		elem := p.parseTypeExprWithoutErrorUnionSuffix()
		return &ast.OwnedType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_MUTABLE) {
		elem := p.parseTypeExprWithoutErrorUnionSuffix()
		return &ast.MutableType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_TAIL) {
		elem := p.parseTypeExprWithoutErrorUnionSuffix()
		return &ast.TailType{Position: elem.Pos(), Elem: elem}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "func" {
		return p.parseFuncTypeExpr()
	}
	if p.peek() == lexer.TOKEN_LPAREN {
		return p.parseTupleTypeExpr()
	}
	storage, explicit, label, region := p.parseRefStorageQualifier()
	return p.parseBaseType(storage, explicit, label, region)
}
func (p *Parser) parseTupleTypeExpr() ast.TypeExpr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_LPAREN)
	fields := make([]ast.TupleTypeField, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			fieldPos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_COLON)
			fields = append(fields, ast.TupleTypeField{Position: fieldPos, Name: name, Type: p.parseTypeExpr()})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RPAREN {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	if len(fields) == 0 {
		p.errorf("tuple type requires at least one field")
	}
	return &ast.TupleTypeExpr{Position: pos, Fields: fields}
}
func (p *Parser) parseTupleExprFromFirst(pos lexer.Pos, first ast.Expr) ast.Expr {
	elems := []ast.Expr{first}
	for p.match(lexer.TOKEN_COMMA) {
		elems = append(elems, p.parseExpr())
	}
	return &ast.TupleExpr{Position: pos, Elems: elems}
}
func (p *Parser) parseListComprehensionFromFirst(pos lexer.Pos, value ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	var rangeEnd ast.Expr
	var rangeStep ast.Expr
	rangeOp := lexer.TOKEN_EOF
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		op := p.advance()
		rangeOp = op.Kind
		rangeEnd = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		if p.match(lexer.TOKEN_RANGE) {
			rangeStep = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	// Optional `by simd` / `by par` marker on a list-map comprehension (docs/79 Part IV). `by simd`
	// emits the body under full fast-math FP; `by par` lowers to a parallel map over the source's
	// disjoint bands (the analyzer does the desugar, once the element type is known).
	fastMath := false
	parallel := false
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		mode := p.expect(lexer.TOKEN_IDENT).Text
		switch mode {
		case "simd":
			fastMath = true
		case "par":
			parallel = true
		default:
			p.errorAt(byPos, "expected `simd` or `par` after `by`, got %q", mode)
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	var owner ast.Expr
	if p.match(lexer.TOKEN_IN) {
		// The comprehension allocation owner `[...] in <owner>` is removed (docs/68 §7):
		// annotate the binding type with `@<owner>` instead. Recover to keep parsing.
		owner = p.withInMembershipDisabled(p.parseExpr)
		p.errorf("comprehension allocation owner `[...] in <owner>` is no longer supported; annotate the binding type instead (e.g. `xs: darray[T] @<owner> = [...]`)")
	}
	return &ast.ListComprehensionExpr{Position: pos, Value: value, Name: name, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter, Owner: owner, FastMath: fastMath, Parallel: parallel}
}

// parseDictComprehensionFromFirst parses a dict comprehension
//
//	{ key: value  for name in source  [if filter] }
//
// where `key`/`value` are already parsed. It produces a ListComprehensionExpr with
// Key set (Key != nil marks the dict form; result type dict[K,V]).
func (p *Parser) parseDictComprehensionFromFirst(pos lexer.Pos, key ast.Expr, value ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	var rangeEnd ast.Expr
	var rangeStep ast.Expr
	rangeOp := lexer.TOKEN_EOF
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		op := p.advance()
		rangeOp = op.Kind
		rangeEnd = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		if p.match(lexer.TOKEN_RANGE) {
			rangeStep = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.ListComprehensionExpr{Position: pos, Key: key, Value: value, Name: name, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter}
}

// parseSetComprehensionFromFirst parses a set comprehension
//
//	{ value  for name in source  [if filter] }
//
// producing a ListComprehensionExpr with Set=true (result type set[V]).
func (p *Parser) parseSetComprehensionFromFirst(pos lexer.Pos, value ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	var rangeEnd ast.Expr
	var rangeStep ast.Expr
	rangeOp := lexer.TOKEN_EOF
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		op := p.advance()
		rangeOp = op.Kind
		rangeEnd = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		if p.match(lexer.TOKEN_RANGE) {
			rangeStep = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.ListComprehensionExpr{Position: pos, Set: true, Value: value, Name: name, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter}
}

// parseFoldComprehensionFromFirst parses a fold/reduce comprehension with no head
// bindings (the common case): `( body  for name in source [if filter] with acc [:T] = seed )`.
func (p *Parser) parseFoldComprehensionFromFirst(pos lexer.Pos, body ast.Expr) ast.Expr {
	return p.parseFoldComprehensionTail(pos, nil, body)
}

// parseFoldComprehensionWithBindings handles a fold whose head begins with one or
// more comma-separated bindings before the body (docs/79):
//
//	( y = f(x), n: i64 = g(y), acc + y*n  for x in src if y > 0 with acc = 0 )
//
// `firstName` is the already-consumed name of the first binding (its `:`/`=` is next).
// Bindings are per-element `let`s; an item is a binding iff it is `IDENT [:T] =`.
func (p *Parser) parseFoldComprehensionWithBindings(pos lexer.Pos, firstName string) ast.Expr {
	bindings := p.parseComprehensionHeadBindings(pos, firstName, true)
	body := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	return p.parseFoldComprehensionTail(pos, bindings, body)
}

// parseComprehensionHeadBindings parses the comma-separated `name [:T] = e` binding
// prefix of a comprehension head, given the already-consumed `firstName` (its `:`/`=`
// is next). It consumes through the comma after the last binding, leaving the parser at
// the body. An item is a binding iff it is `IDENT [:T] =`. When allowColonStart is false
// (inside `{...}`, where `IDENT :` is a dict key:value), continuation bindings are
// detected only by `IDENT =`, so a trailing dict key is not mistaken for a typed binding.
func (p *Parser) parseComprehensionHeadBindings(pos lexer.Pos, firstName string, allowColonStart bool) []ast.Stmt {
	var bindings []ast.Stmt
	name := firstName
	for {
		var typ ast.TypeExpr
		if p.match(lexer.TOKEN_COLON) {
			typ = p.parseTypeExprWithoutErrorUnionSuffix()
		}
		p.expect(lexer.TOKEN_ASSIGN)
		val := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		bindings = append(bindings, &ast.VarDeclStmt{Position: pos, Name: name, Type: typ, Value: val})
		p.expect(lexer.TOKEN_COMMA) // a binding is always followed by another head item
		nextIsBinding := p.peek() == lexer.TOKEN_IDENT && (p.peekAt(1) == lexer.TOKEN_ASSIGN || (allowColonStart && p.peekAt(1) == lexer.TOKEN_COLON))
		if nextIsBinding {
			name = p.expect(lexer.TOKEN_IDENT).Text
			continue
		}
		break
	}
	return bindings
}

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
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		op := p.advance()
		rangeOp = op.Kind
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
	// Optional `by simd` / `by par` marker (docs/79 Part IV). `by simd` opts this one fold into the
	// reassociated (vectorizable) tree reduction (full fast-math FP on its accumulator update);
	// `by par` lowers it to a contention-free parallel reduction over the source's disjoint bands.
	byMode := ""
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		byMode = p.expect(lexer.TOKEN_IDENT).Text
		if byMode != "simd" && byMode != "par" {
			p.errorAt(byPos, "expected `simd` or `par` after `by`, got %q", byMode)
		}
	}
	p.expect(lexer.TOKEN_RPAREN)

	if byMode == "par" {
		return p.buildParallelFoldReduce(pos, bindings, body, name, accName, source, rangeEnd, filter, accInit)
	}

	assign := ast.Stmt(&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: accName}, Value: body, FastMath: byMode == "simd"})
	loopBody := append([]ast.Stmt{}, bindings...)
	if filter != nil {
		loopBody = append(loopBody, &ast.IfStmt{Position: pos, Cond: filter, Then: []ast.Stmt{assign}})
	} else {
		loopBody = append(loopBody, assign)
	}
	var loopStmt ast.Stmt
	if rangeEnd != nil {
		loopStmt = &ast.ForStmt{Position: pos, Name: name, Start: source, End: rangeEnd, Step: rangeStep, Op: rangeOp, Body: loopBody}
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
	if p.peek() != lexer.TOKEN_IDENT || p.pos+3 >= len(p.tokens) {
		return false
	}
	switch p.cur().Text {
	case "any", "all", "first", "count", "each", "sum", "product", "min", "max":
	default:
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	index := p.pos + 2
	for index < len(p.tokens) && p.tokens[index].Kind == lexer.TOKEN_COMMA {
		index++
		if index >= len(p.tokens) || p.tokens[index].Kind != lexer.TOKEN_IDENT {
			return false
		}
		index++
	}
	return index < len(p.tokens) && p.tokens[index].Kind == lexer.TOKEN_IN
}
func (p *Parser) parseFuncTypeExpr() ast.TypeExpr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_LPAREN)
	params := make([]ast.TypeExpr, 0)
	variadic := false
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			if p.peek() == lexer.TOKEN_ELLIPSIS {
				p.advance()
				variadic = true
				break
			}
			params = append(params, p.parseTypeExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
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

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	return &ast.FuncTypeExpr{Position: pos, Params: params, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, Return: retType, Permissions: permissions, Variadic: variadic}
}
func (p *Parser) parseErrorSetExpr() *ast.ErrorSetExpr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ERROR)
	p.expect(lexer.TOKEN_LBRACKET)

	tags := make([]ast.ErrorTagExpr, 0)
	hasEllipsis := false
	for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
		if p.peek() == lexer.TOKEN_ELLIPSIS {
			hasEllipsis = true
			p.advance()
			break
		}
		tags = append(tags, p.parseErrorSetItemGroup()...)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_ELLIPSIS {
			hasEllipsis = true
			p.advance()
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.ErrorSetExpr{Position: pos, Tags: tags, HasEllipsis: hasEllipsis}
}
func (p *Parser) parseErrorSetItem() ast.ErrorTagExpr {
	pos := p.cur().Pos
	setName := p.expect(lexer.TOKEN_IDENT).Text
	tag := ""
	if p.match(lexer.TOKEN_DOT) {
		if p.peek() == lexer.TOKEN_STAR {
			tag = p.advance().Text
		} else {
			tag = p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	return ast.ErrorTagExpr{Position: pos, SetName: setName, Tag: tag}
}

// parseErrorSetItemGroup parses one error-set item, expanding the member-brace sugar
// `Set{Tag1, Tag2, ...}` into one ErrorTagExpr per tag (equivalent to repeating
// `Set.Tag1, Set.Tag2`). `Set` and `Set.Tag` yield a single tag.
func (p *Parser) parseErrorSetItemGroup() []ast.ErrorTagExpr {
	pos := p.cur().Pos
	setName := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_LBRACE) {
		var tags []ast.ErrorTagExpr
		for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
			tpos := p.cur().Pos
			tag := p.expect(lexer.TOKEN_IDENT).Text
			tags = append(tags, ast.ErrorTagExpr{Position: tpos, SetName: setName, Tag: tag})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACE)
		return tags
	}
	tag := ""
	if p.match(lexer.TOKEN_DOT) {
		if p.peek() == lexer.TOKEN_STAR {
			tag = p.advance().Text
		} else {
			tag = p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	return []ast.ErrorTagExpr{{Position: pos, SetName: setName, Tag: tag}}
}
func (p *Parser) parseRefStorageQualifier() (ast.RefStorage, bool, string, string) {
	switch p.peek() {
	case lexer.TOKEN_HEAP:
		tok := p.advance()
		return ast.RefStorageHeap, true, tok.Text, ""
	case lexer.TOKEN_STACK:
		tok := p.advance()
		return ast.RefStorageStack, true, tok.Text, ""
	case lexer.TOKEN_STATIC:
		tok := p.advance()
		return ast.RefStorageStatic, true, tok.Text, ""
	default:
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "any" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text != "can" && p.tokens[p.pos+1].Text != "effects" && p.tokens[p.pos+1].Text != "ensures" {
			// The legacy `<ident> T&` region prefix is removed (docs/68 §7): use the
			// canonical `T& @r` suffix. Now that refstorage params are gone this prefix is
			// unambiguously a region, so we can reject it. Recover by treating the ident as
			// the region so the remainder of the type still parses and only this surfaces.
			name := p.advance().Text
			p.errorf("region prefix `%s` on a reference is no longer supported; use the `@%s` suffix instead (e.g. `T& @%s`)", name, name, name)
			return ast.RefStorageAny, true, name, name
		}
		return ast.RefStorageAny, false, "", ""
	}
}
func (p *Parser) parseRefTypeSuffixes(base ast.TypeExpr, pos lexer.Pos, storage ast.RefStorage, explicit bool, region string) (ast.TypeExpr, int) {
	typ := base
	count := 0
	for {
		switch p.peek() {
		case lexer.TOKEN_AMPERSAND:
			p.advance()
			state := ast.RefStateNonNull
			if p.match(lexer.TOKEN_QUESTION) {
				state = ast.RefStateNullable
			}
			typ = &ast.RefType{Position: pos, Elem: typ, State: state, Storage: storage, Region: region, Explicit: explicit}
			count++
		case lexer.TOKEN_BANG:
			p.advance()
			typ = &ast.RefType{Position: pos, Elem: typ, State: ast.RefStateNull, Storage: storage, Region: region, Explicit: explicit}
			count++
		default:
			return typ, count
		}
	}
}
func (p *Parser) parseGenericTypeArgExpr() ast.TypeExpr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT:
		pos := p.cur().Pos
		return &ast.GenericValueArgTypeExpr{Position: pos, Value: p.parseExpr()}
	case lexer.TOKEN_AMPERSAND:
		pos := p.cur().Pos
		p.advance()
		return &ast.RefStateLiteralTypeExpr{Position: pos, State: ast.RefStateNonNull}
	case lexer.TOKEN_QUESTION:
		pos := p.cur().Pos
		p.advance()
		return &ast.RefStateLiteralTypeExpr{Position: pos, State: ast.RefStateNullable}
	case lexer.TOKEN_BANG:
		pos := p.cur().Pos
		p.advance()
		return &ast.RefStateLiteralTypeExpr{Position: pos, State: ast.RefStateNull}
	case lexer.TOKEN_HEAP, lexer.TOKEN_STACK, lexer.TOKEN_STATIC:
		if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
			pos := p.cur().Pos
			switch p.advance().Kind {
			case lexer.TOKEN_HEAP:
				return &ast.RefStorageLiteralTypeExpr{Position: pos, Storage: ast.RefStorageHeap}
			case lexer.TOKEN_STACK:
				return &ast.RefStorageLiteralTypeExpr{Position: pos, Storage: ast.RefStorageStack}
			case lexer.TOKEN_STATIC:
				return &ast.RefStorageLiteralTypeExpr{Position: pos, Storage: ast.RefStorageStatic}
			default:
				return &ast.RefStorageLiteralTypeExpr{Position: pos, Storage: ast.RefStorageAny}
			}
		}
	}
	if p.peekStateSetTypeExpr() {
		return p.parseStateSetTypeExpr()
	}
	return p.parseTypeExpr()
}
func (p *Parser) peekStateSetTypeExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_PIPE {
		return false
	}
	return p.tokens[p.pos+2].Kind == lexer.TOKEN_IDENT
}
func (p *Parser) parseStateSetTypeExpr() ast.TypeExpr {
	pos := p.cur().Pos
	cases := make([]string, 0, 2)
	for {
		cases = append(cases, p.expect(lexer.TOKEN_IDENT).Text)
		if !p.match(lexer.TOKEN_PIPE) {
			break
		}
	}
	return &ast.StateSetTypeExpr{Position: pos, Cases: cases}
}
func (p *Parser) peekAggregateStateBracketList() ([]ast.RefState, bool) {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return nil, false
	}
	states := make([]ast.RefState, 0, 1)
	i := p.pos + 1
	expectState := true
	for i < len(p.tokens) {
		tok := p.tokens[i]
		if expectState {
			switch tok.Kind {
			case lexer.TOKEN_AMPERSAND:
				states = append(states, ast.RefStateNonNull)
			case lexer.TOKEN_QUESTION:
				states = append(states, ast.RefStateNullable)
			case lexer.TOKEN_BANG:
				states = append(states, ast.RefStateNull)
			default:
				return nil, false
			}
			i++
			expectState = false
			continue
		}
		if tok.Kind == lexer.TOKEN_RBRACKET {
			if len(states) == 0 {
				return nil, false
			}
			return states, true
		}
		if tok.Kind != lexer.TOKEN_COMMA {
			return nil, false
		}
		i++
		expectState = true
	}
	return nil, false
}
func (p *Parser) parseAggregateStateBracketList() []ast.RefState {
	p.expect(lexer.TOKEN_LBRACKET)
	states := make([]ast.RefState, 0, 1)
	for {
		switch p.peek() {
		case lexer.TOKEN_AMPERSAND:
			p.advance()
			states = append(states, ast.RefStateNonNull)
		case lexer.TOKEN_QUESTION:
			p.advance()
			states = append(states, ast.RefStateNullable)
		case lexer.TOKEN_BANG:
			p.advance()
			states = append(states, ast.RefStateNull)
		default:
			p.errorf("expected aggregate state marker &, ?, or !, got %s", p.cur())
			states = append(states, ast.RefStateNullable)
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return states
}
func newAggregateStateTypeExpr(pos lexer.Pos, base ast.TypeExpr, states []ast.RefState) *ast.AggregateStateTypeExpr {
	expr := &ast.AggregateStateTypeExpr{Position: pos, Base: base}
	if len(states) > 0 {
		expr.State = states[0]
		expr.States = append([]ast.RefState(nil), states...)
	}
	return expr
}
func canApplyAggregateState(typ ast.TypeExpr) bool {
	switch typ.(type) {
	case *ast.NamedType, *ast.GenericType:
		return true
	default:
		return false
	}
}
func (p *Parser) parseBaseType(storage ast.RefStorage, explicit bool, label string, region string) ast.TypeExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.matchQualifiedNameSeparator() {
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	var typ ast.TypeExpr = &ast.NamedType{Position: pos, Name: name}

	if canApplyAggregateState(typ) {
		if states, ok := p.peekAggregateStateBracketList(); ok {
			typ = newAggregateStateTypeExpr(pos, typ, states)
			p.parseAggregateStateBracketList()
		}
	}

	if p.peek() == lexer.TOKEN_LBRACKET {
		if builtin := p.parseBuiltinTypeExpr(pos, name); builtin != nil {
			typ = builtin
		} else {
			hasTopLevelComma := p.peekTopLevelCommaInCurrentBracketList()
			afterBracket := lexer.TOKEN_EOF
			if p.pos+1 < len(p.tokens) {
				afterBracket = p.tokens[p.pos+1].Kind
			}
			isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
			if !hasTopLevelComma && afterBracket == lexer.TOKEN_IDENT && p.pos+2 < len(p.tokens) {
				afterIdent := p.tokens[p.pos+2].Kind
				isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET && afterIdent != lexer.TOKEN_PIPE && afterIdent != lexer.TOKEN_DOT
				if afterIdent == lexer.TOKEN_RBRACKET {
					argName := p.tokens[p.pos+1].Text
					isArray = isSingleUppercaseIdentifier(name) || isConstantStyleArraySizeIdentifier(argName)
				}
			}
			if hasTopLevelComma {
				isArray = false
			}

			if isArray {
				p.advance()
				size := p.parseExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				typ = &ast.ArrayType{Position: pos, Elem: typ, Size: size}
			} else {
				p.advance()
				var args []ast.TypeExpr
				for {
					args = append(args, p.parseGenericTypeArgExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				typ = &ast.GenericType{Position: pos, Name: name, Args: args}
			}
		}
	}

	if canApplyAggregateState(typ) {
		if states, ok := p.peekAggregateStateBracketList(); ok {
			typ = newAggregateStateTypeExpr(pos, typ, states)
			p.parseAggregateStateBracketList()
		}
	}

	refCount := 0
	typ, refCount = p.parseRefTypeSuffixes(typ, pos, storage, explicit, region)
	if explicit && refCount == 0 {
		if region != "" {
			p.errorf("region qualifier %q requires a pointer type", label)
		} else {
			p.errorf("storage qualifier %q requires a pointer type", label)
		}
	}

	if p.peek() == lexer.TOKEN_LBRACKET {
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		typ = &ast.ArrayType{Position: pos, Elem: typ, Size: size}
	}
	if p.match(lexer.TOKEN_QUESTION) {
		typ = &ast.OptionalTypeExpr{Position: pos, Value: typ}
	}

	return typ
}
func (p *Parser) peekTopLevelCommaInCurrentBracketList() bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	bracketDepth := 0
	parenDepth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LBRACKET:
			bracketDepth++
		case lexer.TOKEN_RBRACKET:
			bracketDepth--
			if bracketDepth == 0 {
				return false
			}
		case lexer.TOKEN_LPAREN:
			parenDepth++
		case lexer.TOKEN_RPAREN:
			if parenDepth > 0 {
				parenDepth--
			}
		case lexer.TOKEN_COMMA:
			if bracketDepth == 1 && parenDepth == 0 {
				return true
			}
		}
	}
	return false
}
