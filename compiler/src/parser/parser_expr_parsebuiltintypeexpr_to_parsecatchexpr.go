package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseBuiltinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	switch name {
	case "id", "Id", "ID", "ptrid", "PtrId", "PTRID", "RowId", "GuestVAddr", "HostPtr", "NativeMappedGuestPtr":
		p.advance()
		tag := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		if name == "RowId" {
			return &ast.BuiltinTypeExpr{Position: pos, Name: "RowId", TypeArgs: []ast.TypeExpr{tag}}
		}
		if name == "GuestVAddr" || name == "HostPtr" || name == "NativeMappedGuestPtr" {
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{tag}}
		}
		if name == "ptrid" || name == "PtrId" || name == "PTRID" {
			return &ast.BuiltinTypeExpr{Position: pos, Name: "ptrid", TypeArgs: []ast.TypeExpr{tag}}
		}
		return &ast.BuiltinTypeExpr{Position: pos, Name: "id", TypeArgs: []ast.TypeExpr{tag}}
	case "array", "darray":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			if name == "darray" {
				return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
			}
			p.errorf("array expects 2 arguments, got 1")
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		p.expect(lexer.TOKEN_COMMA)
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}, ValueArgs: []ast.Expr{size}}
	case "dict":
		p.advance()
		key := p.parseTypeExpr()
		p.expect(lexer.TOKEN_COMMA)
		value := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{key, value}}
	case "set":
		p.advance()
		elem := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
	case "cstr":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "cstr", ValueArgs: []ast.Expr{size}}
	case "treeview":
		// `treeview[T]` has been removed: it was a redundant compatibility alias for the bare
		// concrete tree-variant type `T` (both resolve to the same TreeVariantViewType). Reject it
		// and recover with the bare inner type so the rest of the signature still resolves.
		p.advance()
		elem := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		p.errorAt(pos, "`treeview[T]` has been removed; write the bare concrete variant type `T` (e.g. `Lua.Expr.Binary`)")
		return elem
	case "view", "packedview":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		if name == "view" || name == "packedview" {
			p.errorf("%s expects 1 argument, got 3", name)
			for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
				p.advance()
			}
			p.expect(lexer.TOKEN_RBRACKET)
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		p.expect(lexer.TOKEN_COMMA)
		begin := p.parseExpr()
		p.expect(lexer.TOKEN_COMMA)
		end := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}, ValueArgs: []ast.Expr{begin, end}}
	case "sview":
		p.advance()
		begin := p.parseExpr()
		p.expect(lexer.TOKEN_COMMA)
		end := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, ValueArgs: []ast.Expr{begin, end}}
	default:
		return nil
	}
}
func (p *Parser) parseExpr() ast.Expr {
	// Quantifier prefix (docs/90 brick 90-4), only in law/spec bodies: `forall i: <body>` /
	// `exists i: <body>`. Gated on allowQuantifiers so ordinary code can name a variable `forall`.
	if p.allowQuantifiers && p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "forall" || p.cur().Text == "exists") && p.quantifierStartsHere() {
		return p.parseQuantifier()
	}
	expr := p.parseOr()
	// `implies` infix (lowest precedence, right-associative), only in law/spec bodies. Desugared to
	// `(not a) or b` so the analyzer/SMT/backend need no new node. `=>` is an accepted spelling of the
	// same operator (docs/118-B: `ensure GUARD => POST` for conditional postconditions), sharing the
	// identical desugaring. `=>` is gated on the same allowQuantifiers flag, so it stays inert in
	// ordinary expression position where it belongs to lambdas / catch arms / enum maps.
	if p.allowQuantifiers && (p.peek() == lexer.TOKEN_FATARROW ||
		(p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "implies")) {
		opPos := p.cur().Pos
		p.advance()
		right := p.parseExpr() // right-assoc; also catches nested implies / quantifiers
		expr = &ast.BinaryExpr{
			Position: opPos,
			Op:       lexer.TOKEN_OR,
			Left:     &ast.UnaryExpr{Position: opPos, Op: lexer.TOKEN_NOT, Operand: expr},
			Right:    right,
		}
		return expr
	}

	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && (p.tokens[p.pos+1].Text == "first" || p.tokens[p.pos+1].Text == "each") {
		return p.parseProjectionQueryExpr(expr)
	}
	if p.allowWhereExpr && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "where" {
		return p.parseWhereViewExpr(expr)
	}
	// Postfix ternary `A if C else B` — but NOT when the expression just closed an
	// indented block form (a multi-line match-expression's arms end in a DEDENT that
	// sits directly before the next statement's tokens). In that stream shape an `if`
	// is the NEXT STATEMENT, not a ternary condition:
	//
	//	s: sview = match k:
	//	    1: "one"
	//	    _: v
	//	if s == "":        # statement, previously swallowed as `v if s == "" else ...`
	if p.allowTernary && p.peek() == lexer.TOKEN_IF && !p.prevTokenIsDedent() {
		pos := p.cur().Pos
		p.advance()
		cond := p.parseOr()
		p.expect(lexer.TOKEN_ELSE)
		alt := p.parseExpr()
		return &ast.TernaryExpr{Position: pos, Value: expr, Cond: cond, Alt: alt}
	}
	if p.peek() == lexer.TOKEN_ELSE {
		pos := p.cur().Pos
		p.advance()
		recovery := p.parseRecoveryClause(pos)
		fallback := recoveryFallbackExpr(recovery)
		if tryExpr, ok := expr.(*ast.TryExpr); ok && tryExpr.Recovery == nil && tryExpr.Fallback == nil {
			tryExpr.Recovery = recovery
			tryExpr.Fallback = fallback
			return tryExpr
		}
		if getExpr, ok := expr.(*ast.GetExpr); ok && getExpr.Recovery == nil && getExpr.Fallback == nil {
			getExpr.Recovery = recovery
			getExpr.Fallback = fallback
			return getExpr
		}
		p.errorAt(pos, "implicit `else` unwrap has been removed; write `get <expr> else ...` to make the absence check explicit")
		return &ast.UnwrapElseExpr{Position: pos, Value: expr, Fallback: fallback, Recovery: recovery, LegacyImplicitElse: true}
	}
	if p.matchIdentText("can") {
		permissions := p.parsePermissionRefs(false)
		return &ast.CanExpr{Position: expr.Pos(), Expr: expr, Permissions: permissions}
	}

	return expr
}

// quantifierStartsHere confirms a `forall`/`exists` keyword is followed by a binder identifier (so a
// variable literally named `forall` used in expression position is not misparsed as a quantifier).
func (p *Parser) quantifierStartsHere() bool {
	return p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT || p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN)
}

// parseQuantifier parses `forall i, j: <body>` / `exists i: <body>` (docs/90 brick 90-4). Binders are
// comma-separated identifiers ranging over the integers; the body is a boolean expression (commonly a
// guarded implication). Only reached when allowQuantifiers is set.
func (p *Parser) parseQuantifier() ast.Expr {
	pos := p.cur().Pos
	exists := p.cur().Text == "exists"
	p.advance() // forall / exists
	var vars []string
	if p.match(lexer.TOKEN_LPAREN) {
		for {
			vars = append(vars, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	} else {
		for {
			vars = append(vars, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	// Concept-sugar range binder (docs/100): `forall i in <range>: P`. Pure desugaring to the canonical
	// guarded form `forall i: (lo <= i and i < hi) implies P`. The range source is either an explicit
	// half-open range `lo ..< hi` or the index sugar `a.indices` (≡ `0 ..< a.count`). Single binder only.
	if p.peek() == lexer.TOKEN_IN {
		p.advance() // in
		source, lo, hi, isIndexRange := p.parseQuantifierInSource()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseExpr()
		if !isIndexRange {
			return &ast.QuantifierExpr{Position: pos, Exists: exists, Vars: vars, In: source, Body: body}
		} else if len(vars) != 1 {
			p.errorAt(pos, "`forall i in <range>:` binds exactly one index variable")
		}
		ident := &ast.Ident{Position: pos, Name: vars[0]}
		guard := &ast.BinaryExpr{
			Position: pos,
			Op:       lexer.TOKEN_AND,
			Left:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LTEQ, Left: lo, Right: ident},
			Right:    &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: ident, Right: hi},
		}
		var guarded ast.Expr
		if exists {
			guarded = &ast.BinaryExpr{
				Position: pos,
				Op:       lexer.TOKEN_AND,
				Left:     guard,
				Right:    body,
			}
		} else {
			// `(0 <= i and i < hi) implies body` desugars (like the `implies` infix) to `(not guard) or body`.
			guarded = &ast.BinaryExpr{
				Position: pos,
				Op:       lexer.TOKEN_OR,
				Left:     &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: guard},
				Right:    body,
			}
		}
		return &ast.QuantifierExpr{Position: pos, Exists: exists, Vars: vars, Body: guarded}
	}

	p.expect(lexer.TOKEN_COLON)
	body := p.parseExpr()
	return &ast.QuantifierExpr{Position: pos, Exists: exists, Vars: vars, Body: body}
}

// parseQuantifierInSource parses the source of a `forall i in <source>:` binder. Range forms return
// their half-open [lo, hi) bounds. Any other expression is a value-binding collection source.
func (p *Parser) parseQuantifierInSource() (source ast.Expr, lo ast.Expr, hi ast.Expr, isIndexRange bool) {
	pos := p.cur().Pos
	first := p.parseOr()
	if p.match(lexer.TOKEN_RANGE_LT) {
		return first, first, p.parseOr(), true
	}
	if p.peek() == lexer.TOKEN_RANGE_LE || p.peek() == lexer.TOKEN_RANGE {
		if p.peek() == lexer.TOKEN_RANGE {
			// Bare `..` as a range is ambiguous about its endpoint; require an explicit operator (same
			// rule as the for-loop range). Recover as the inclusive form so a single error is reported.
			p.errorAt(p.cur().Pos, ambiguousBareRangeMsg)
		}
		// Inclusive `lo ..= hi` desugars to the half-open `lo ..< (hi + 1)`, reusing the guarded-range
		// machinery (guard becomes `lo <= i and i < hi+1`, i.e. `i <= hi` for integers).
		p.advance()
		hiInclusive := p.parseOr()
		hiPlusOne := &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: hiInclusive, Right: &ast.IntLit{Position: pos, Value: "1"}}
		return first, first, hiPlusOne, true
	}
	// `a.indices` → 0 ..< a.count
	if fe, ok := first.(*ast.FieldExpr); ok && fe.Field == "indices" {
		zero := &ast.IntLit{Position: pos, Value: "0"}
		count := &ast.FieldExpr{Position: pos, Object: fe.Object, Field: "count"}
		return fe.Object, zero, count, true
	}
	return first, nil, nil, false
}

func (p *Parser) parseProjectionQueryExpr(projection ast.Expr) ast.Expr {
	pos := projection.Pos()
	p.expectIdentText("for")
	kindText := p.expect(lexer.TOKEN_IDENT).Text
	kind := ast.QueryExprFirst
	switch kindText {
	case "first":
		kind = ast.QueryExprFirst
	case "each":
		kind = ast.QueryExprEach
	default:
		p.errorAt(pos, "projection query expression expects first or each, got %q", kindText)
	}
	name, pattern := p.parseQueryBindPattern()
	p.expect(lexer.TOKEN_IN)
	source := p.withInMembershipDisabled(func() ast.Expr {
		return p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	})
	var patternFilter ast.MatchPattern
	var patternFilterSubject string
	var filter ast.Expr
	if p.matchIdentText("where") {
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
	return &ast.QueryExpr{Position: pos, Kind: kind, Name: name, Pattern: pattern, Source: source, Filter: filter, PatternFilter: patternFilter, PatternFilterSubject: patternFilterSubject, Projection: projection, Owner: owner}
}

func (p *Parser) parseWhereViewExpr(source ast.Expr) ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("where")
	if p.peekWhereTupleSubjectPattern() {
		return p.parseWhereTupleSubjectPatternViewExpr(source, pos)
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IS {
		return p.parseWhereSubjectPatternViewExpr(source, pos)
	}
	if p.peekWhereViewPatternFilter() {
		return p.parseWherePatternViewExpr(source, pos)
	}
	binderPos := p.cur().Pos
	binders := make([]string, 0, 2)
	binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_COMMA) {
		binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	}
	p.expect(lexer.TOKEN_COLON)
	filter := p.parseExpr()
	predicateParam := binders[0]
	if len(binders) > 1 {
		predicateParam = "__where_item"
		filter = rewriteWhereTupleBinderExpr(filter, predicateParam, binders)
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: predicateParam}},
		BodyExpr:            filter,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWhereTupleSubjectPatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	binderPos := p.cur().Pos
	binders := make([]string, 0, 2)
	binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_COMMA) {
		binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	}
	subject := binders[len(binders)-1]
	p.expect(lexer.TOKEN_IS)
	pattern := p.parseMatchPattern()
	subjectField := "value"
	for i, binder := range binders {
		if binder != subject {
			continue
		}
		switch i {
		case 0:
			subjectField = "index"
		case 1:
			subjectField = "value"
		}
	}
	tupleItem := &ast.Ident{Position: binderPos, Name: "__where_item"}
	subjectExpr := &ast.FieldExpr{Position: binderPos, Object: tupleItem, Field: subjectField}
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: subjectExpr, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := rewriteWhereTupleBinderExpr(p.parseExpr(), "__where_item", binders)
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.FieldExpr{Position: binderPos, Object: &ast.Ident{Position: binderPos, Name: "__where_item"}, Field: subjectField},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: "__where_item"}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWhereSubjectPatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	binderPos := p.cur().Pos
	binder := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IS)
	pattern := p.parseMatchPattern()
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	item := &ast.Ident{Position: binderPos, Name: binder}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: item, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := p.parseExpr()
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.Ident{Position: binderPos, Name: binder},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: binder}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWherePatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	pattern := p.parseMatchPattern()
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	item := &ast.Ident{Position: pattern.Pos(), Name: "__where_item"}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: item, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := p.parseExpr()
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.Ident{Position: pattern.Pos(), Name: "__where_item"},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            pattern.Pos(),
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: pattern.Pos(), Name: "__where_item"}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) peekExplicitWhereSubjectPattern(name string) bool {
	return name != "" &&
		p.peek() == lexer.TOKEN_IDENT &&
		p.cur().Text == name &&
		p.pos+1 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_IS
}

func (p *Parser) parseQueryBindPattern() (string, ast.MoveBindPattern) {
	pos := p.cur().Pos
	first := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COMMA) {
		return first, nil
	}
	args := []ast.MoveBindArg{{Position: pos, Name: first}}
	for {
		tok := p.expect(lexer.TOKEN_IDENT)
		args = append(args, ast.MoveBindArg{Position: tok.Pos, Name: tok.Text})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return first, &ast.MoveBindTuplePattern{Position: pos, Args: args}
}

func (p *Parser) peekQueryWhereSubjectPattern(name string, pattern ast.MoveBindPattern) (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IS {
		return "", false
	}
	subject := p.cur().Text
	if pattern == nil {
		return subject, name != "" && name == subject
	}
	switch bind := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return subject, bind.Name != "" && bind.Name == subject
	case *ast.MoveBindTuplePattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	case *ast.MoveBindStructPattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	}
	return "", false
}

func (p *Parser) peekWhereTupleSubjectPattern() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_COMMA {
		return false
	}
	index := p.pos + 2
	for index < len(p.tokens) {
		if p.tokens[index].Kind != lexer.TOKEN_IDENT {
			return false
		}
		index++
		if index >= len(p.tokens) {
			return false
		}
		switch p.tokens[index].Kind {
		case lexer.TOKEN_IS:
			return true
		case lexer.TOKEN_COMMA:
			index++
		default:
			return false
		}
	}
	return false
}

func (p *Parser) peekWhereViewPatternFilter() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1].Kind
	if next == lexer.TOKEN_LPAREN || next == lexer.TOKEN_LBRACE {
		return forWhereIdentLooksLikePatternType(p.cur().Text)
	}
	if next != lexer.TOKEN_DOT {
		return false
	}
	index := p.pos + 1
	for index+1 < len(p.tokens) && p.tokens[index].Kind == lexer.TOKEN_DOT && p.tokens[index+1].Kind == lexer.TOKEN_IDENT {
		index += 2
	}
	if index >= len(p.tokens) || !forWhereIdentLooksLikePatternType(p.cur().Text) {
		return false
	}
	if index >= len(p.tokens) {
		return true
	}
	switch p.tokens[index].Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_COLON, lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_COMMA, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
		return true
	default:
		return false
	}
}

func matchPatternAsIsTargetExpr(pattern ast.MatchPattern) ast.Expr {
	switch n := pattern.(type) {
	case *ast.MatchVariantPattern:
		return &ast.VariantTestExpr{Position: n.Position, Pattern: n}
	case *ast.MatchStructPattern:
		return &ast.StructTestExpr{Position: n.Position, Pattern: n}
	default:
		return nil
	}
}

// elseIntroducesRecoveryClause reports whether the token sequence immediately
// after an `else` is a recovery clause (`return`, `raise`, `void`, or a
// `binding:` block) rather than a plain value fallback. The caller is positioned
// on the `else` token.
func (p *Parser) elseIntroducesRecoveryClause() bool {
	if p.peek() != lexer.TOKEN_ELSE {
		return false
	}
	next := p.pos + 1
	if next >= len(p.tokens) {
		return false
	}
	switch p.tokens[next].Kind {
	case lexer.TOKEN_RETURN, lexer.TOKEN_RAISE:
		return true
	case lexer.TOKEN_IDENT:
		if p.tokens[next].Text == "void" {
			return true
		}
		// `else binding:` block recovery.
		return next+1 < len(p.tokens) && p.tokens[next+1].Kind == lexer.TOKEN_COLON
	}
	return false
}

func (p *Parser) parseRecoveryClause(pos lexer.Pos) *ast.RecoveryClause {
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		p.advance()
		return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryReturn, Value: p.parseOptionalRecoveryExpr()}
	case lexer.TOKEN_RAISE:
		p.advance()
		return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryRaise, Value: p.parseOr()}
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "void" {
			p.advance()
			return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryVoid}
		}
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			binding := p.cur().Text
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryBlock, Binding: binding, Body: p.parseBlock()}
		}
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: p.parseExpr()}
}

func (p *Parser) parseOptionalRecoveryExpr() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_DEDENT, lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_COMMA:
		return nil
	default:
		return p.parseExpr()
	}
}

func recoveryFallbackExpr(recovery *ast.RecoveryClause) ast.Expr {
	if recovery == nil {
		return nil
	}
	switch recovery.Kind {
	case ast.RecoveryValue:
		return recovery.Value
	case ast.RecoveryRaise:
		return &ast.RaiseExpr{Position: recovery.Position, Error: recovery.Value}
	default:
		return nil
	}
}
func (p *Parser) parseOr() ast.Expr {
	left := p.parseAnd()
	for p.peek() == lexer.TOKEN_OR {
		pos := p.cur().Pos
		p.advance()
		right := p.parseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_OR, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAnd() ast.Expr {
	left := p.parseNot()
	for p.peek() == lexer.TOKEN_AND {
		pos := p.cur().Pos
		p.advance()
		right := p.parseNot()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AND, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseNot() ast.Expr {
	if p.peek() == lexer.TOKEN_NOT {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNot()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: operand}
	}
	if p.matchIdentText("let") {
		pos := p.tokens[p.pos-1].Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseNot()
		return &ast.OptionalBindExpr{Position: pos, Name: name, Value: value}
	}
	return p.parseComparison()
}
func (p *Parser) parseComparison() ast.Expr {
	left := p.parseAs()
	for p.peek() == lexer.TOKEN_EQEQ || p.peek() == lexer.TOKEN_BANGEQ ||
		p.peek() == lexer.TOKEN_LT || p.peek() == lexer.TOKEN_GT ||
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ ||
		(!p.disallowIsComparison && p.peek() == lexer.TOKEN_IS) || p.membershipLiteralAhead() || p.notInMembershipAhead() {
		pos := p.cur().Pos
		if p.notInMembershipAhead() {
			p.advance()
			inToken := p.advance()
			right := p.parseMembershipRight()
			membership := &ast.BinaryExpr{Position: inToken.Pos, Op: lexer.TOKEN_IN, Left: left, Right: right}
			left = &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: membership}
			continue
		}
		op := p.advance()
		var right ast.Expr
		if op.Kind == lexer.TOKEN_IS {
			if p.peek() == lexer.TOKEN_NOT {
				notToken := p.advance()
				right = p.parseIsTestExpr()
				test := &ast.BinaryExpr{Position: op.Pos, Op: op.Kind, Left: left, Right: right}
				left = &ast.UnaryExpr{Position: notToken.Pos, Op: lexer.TOKEN_NOT, Operand: test}
				continue
			}
			if name, ok := p.peekIsBareBindingTarget(); ok {
				// docs/80: `x is value` with a bare, unqualified, lowercase
				// identifier that is not a builtin type name is a refutable
				// binding, not a type test. It reuses the `let value = x`
				// optional-bind node so the whole optional-binding pipeline
				// (analysis + scoping + lowering) applies unchanged.
				p.advance()
				left = &ast.OptionalBindExpr{Position: op.Pos, Name: name, Value: left, FromIs: true}
				continue
			}
			right = p.parseIsTestExpr()
		} else if op.Kind == lexer.TOKEN_IN {
			right = p.parseMembershipRight()
		} else {
			right = p.parseAs()
		}
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) membershipLiteralAhead() bool {
	return p.allowInMembership && p.peek() == lexer.TOKEN_IN
}
func (p *Parser) notInMembershipAhead() bool {
	return p.allowInMembership && p.peek() == lexer.TOKEN_NOT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IN
}
func (p *Parser) parseMembershipRight() ast.Expr {
	start := p.parseAs()
	if !p.membershipRangeOpAhead() {
		return start
	}
	opKind, opPos := p.consumeMembershipRangeOp()
	end := p.parseAs()
	return &ast.MembershipRangeExpr{Position: opPos, Start: start, End: end, Op: opKind}
}
func (p *Parser) membershipRangeOpAhead() bool {
	switch p.peek() {
	case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE:
		return true
	default:
		return false
	}
}
func (p *Parser) parseAs() ast.Expr {
	return p.parseBitwiseOr()
}

// isBuiltinBareTypeName mirrors the lowercase builtin type names registered in
// semantic/analyzer_builtins.go (plus the string views cstr/dstr). A bare
// `is`-target naming one of these is a TYPE test; any other bare unqualified
// lowercase identifier is a refutable BINDING (docs/80). Keep in sync with the
// builtin registration; a missing entry turns an `is <builtin>` type test into a
// binding and the suite's many `is`-type-test fixtures catch it immediately.
var isBuiltinBareTypeName = map[string]bool{
	"void": true, "bool": true, "char": true, "int": true,
	"i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true, "uintptr": true,
	"f32": true, "f64": true, "cstr": true, "dstr": true,
}

// peekIsBareBindingTarget reports whether the upcoming `is`-target is a bare,
// unqualified, lowercase, non-builtin identifier — i.e. a refutable binding name
// (docs/80) rather than a type/variant/struct test. It must NOT be continued by a
// token that would make it a qualified name, a variant/struct payload, a union
// alternative, or an alias.
func (p *Parser) peekIsBareBindingTarget() (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT {
		return "", false
	}
	name := p.cur().Text
	if name == "" || name[0] < 'a' || name[0] > 'z' || isBuiltinBareTypeName[name] {
		return "", false
	}
	if p.pos+1 < len(p.tokens) {
		switch p.tokens[p.pos+1].Kind {
		case lexer.TOKEN_DOT, lexer.TOKEN_SCOPE, lexer.TOKEN_LPAREN,
			lexer.TOKEN_LBRACE, lexer.TOKEN_LBRACKET, lexer.TOKEN_PIPE, lexer.TOKEN_AS:
			return "", false
		}
	}
	return name, true
}
func (p *Parser) parseIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	brackets := false
	if p.peek() == lexer.TOKEN_LBRACKET {
		brackets = true
		p.advance()
	}
	targets := make([]ast.Expr, 0, 1)
	targets = append(targets, p.parseSingleIsTestTargetExpr())
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		targets = append(targets, p.parseSingleIsTestTargetExpr())
	}
	if brackets {
		p.expect(lexer.TOKEN_RBRACKET)
	}
	if len(targets) == 1 && !brackets {
		return targets[0]
	}
	return &ast.IsPatternExpr{Position: pos, Targets: targets, Brackets: brackets}
}
func (p *Parser) parseSingleIsTestTargetExpr() ast.Expr {
	target := p.parseSingleIsTestTargetExprWithoutAlias()
	if p.peek() == lexer.TOKEN_AS && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		pos := p.cur().Pos
		p.advance()
		alias := p.expect(lexer.TOKEN_IDENT).Text
		return &ast.IsAliasExpr{Position: pos, Target: target, Alias: alias}
	}
	// docs/77 §2 binder sugar: `e is Statement s` ≡ `e is Statement as s` — a bare
	// lowercase identifier directly after the target binds the matched value (for a
	// category target, at the narrowed type). No valid continuation starts with a
	// bare lowercase ident here, so this is unambiguous.
	if p.peek() == lexer.TOKEN_IDENT {
		name := p.cur().Text
		if name != "" && name != "_" && name[0] >= 'a' && name[0] <= 'z' {
			pos := p.cur().Pos
			alias := p.advance().Text
			return &ast.IsAliasExpr{Position: pos, Target: target, Alias: alias}
		}
	}
	return target
}
func (p *Parser) parseSingleIsTestTargetExprWithoutAlias() ast.Expr {
	if p.peekQualifiedVariantTargetWithPayload() {
		return p.parseVariantIsTestExpr()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) &&
		(p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACE) {
		return p.parseStructIsTestExpr()
	}
	if p.peek() == lexer.TOKEN_DOT {
		return p.parsePrimary()
	}
	if p.peek() == lexer.TOKEN_LPAREN {
		pos := p.cur().Pos
		p.advance()
		inner := p.parseIsTestExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: pos, Inner: inner}
	}
	// docs/77 §2 binder form: `is Statement s` — a bare type name followed by a lowercase
	// binder identifier. Intercepted BEFORE the type parser, whose legacy region-prefix
	// grammar (`r T&`) would otherwise swallow both identifiers. The binder form never
	// continues with `&` / `.` / `[` after the second identifier.
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		second := p.tokens[p.pos+1].Text
		cont := lexer.TOKEN_EOF
		if p.pos+2 < len(p.tokens) {
			cont = p.tokens[p.pos+2].Kind
		}
		if second != "" && second != "_" && second[0] >= 'a' && second[0] <= 'z' &&
			cont != lexer.TOKEN_AMPERSAND && cont != lexer.TOKEN_DOT && cont != lexer.TOKEN_LBRACKET {
			pos := p.cur().Pos
			name := p.advance().Text
			return &ast.TypeExprExpr{Position: pos, Type: &ast.NamedType{Position: pos, Name: name}}
		}
	}
	target := p.parseTypeExprWithoutErrorUnionSuffix()
	return &ast.TypeExprExpr{Position: target.Pos(), Type: target}
}
func (p *Parser) peekQualifiedVariantTargetWithPayload() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	i := p.pos
	sawDot := false
	for i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_DOT {
		if i+2 >= len(p.tokens) || p.tokens[i+2].Kind != lexer.TOKEN_IDENT {
			return false
		}
		sawDot = true
		i += 2
	}
	return sawDot && i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_LPAREN
}
func (p *Parser) parseQualifiedVariantTarget() (string, string, lexer.Pos) {
	pos := p.cur().Pos
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	if len(parts) < 2 {
		return "", "", pos
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], pos
}
func (p *Parser) parseVariantIsTestExpr() ast.Expr {
	enumName, variant, pos := p.parseQualifiedVariantTarget()
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	p.expect(lexer.TOKEN_LPAREN)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			args = append(args, p.parseMatchPatternArg())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.VariantTestExpr{Position: pos, Pattern: &ast.MatchVariantPattern{Position: pos, EnumName: enumName, Variant: variant, Args: args}}
}
func (p *Parser) parseStructIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	pattern, ok := p.parseMatchStructPatternAfterName(pos, name).(*ast.MatchStructPattern)
	if !ok {
		return &ast.StructTestExpr{Position: pos}
	}
	return &ast.StructTestExpr{Position: pos, Pattern: pattern}
}
func (p *Parser) parseBitwiseOr() ast.Expr {
	left := p.parseBitwiseXor()
	for p.peek() == lexer.TOKEN_PIPE {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseXor()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PIPE, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseXor() ast.Expr {
	left := p.parseBitwiseAnd()
	for p.peek() == lexer.TOKEN_CARET {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_CARET, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseAnd() ast.Expr {
	left := p.parseShift()
	for p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		right := p.parseShift()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AMPERSAND, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseShift() ast.Expr {
	left := p.parseAddSub()
	for p.peek() == lexer.TOKEN_LSHIFT || p.peek() == lexer.TOKEN_RSHIFT {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseAddSub()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAddSub() ast.Expr {
	left := p.parseMulDiv()
	for p.peek() == lexer.TOKEN_PLUS || p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseMulDiv()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseMulDiv() ast.Expr {
	left := p.parseUnary()
	for p.peek() == lexer.TOKEN_STAR || p.peek() == lexer.TOKEN_SLASH || p.peek() == lexer.TOKEN_PERCENT {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseUnary()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseUnary() ast.Expr {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "new" {
		return p.parseAllocExpr()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "node" && p.looksLikeNodeAllocExpr() {
		pos := p.cur().Pos
		p.errorAt(pos, "`node[...] Tree.Member(...)` has been removed; use `new[owner] Enum.Variant(...)` or an ordinary enum constructor")
		p.advance()
		return &ast.Ident{Position: pos, Name: "__invalid_removed_node_sugar"}
	}
	if p.matchIdentText("submit") {
		pos := p.tokens[p.pos-1].Pos
		var explicitPool ast.Expr
		hasExplicitPool := false
		if p.match(lexer.TOKEN_LBRACKET) {
			hasExplicitPool = true
			explicitPool = p.parseExpr()
			p.expect(lexer.TOKEN_RBRACKET)
		}
		callExpr := p.parseUnary()
		call, ok := callExpr.(*ast.CallExpr)
		if !ok {
			p.errorf("submit expects a call like submit work(arg) or submit[pool] work(arg)")
			return callExpr
		}
		if len(call.Args) != 1 || call.NamedArgCount() != 0 {
			p.errorf("submit currently expects exactly one positional argument")
			return callExpr
		}
		poolArg := explicitPool
		if !hasExplicitPool {
			activePool := p.activePoolName()
			if activePool == "" {
				p.errorf("submit requires an active pool scope like \"pool workers(...):\" or an explicit pool like \"submit[pool] work(arg)\"")
				return callExpr
			}
			poolPos := call.Func.Pos()
			poolArg = &ast.CastExpr{
				Position: poolPos,
				Operand:  &ast.AddrOfExpr{Position: poolPos, Operand: &ast.Ident{Position: poolPos, Name: activePool}},
				Target: &ast.RefType{
					Position: poolPos,
					Elem:     &ast.NamedType{Position: poolPos, Name: "ThreadPool"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}
		}
		submitCall := &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_submit1"},
			Args: []ast.Expr{
				poolArg,
				call.Func,
				call.Args[0],
			},
			// Synthesized from the safe `submit` / `pool` sugar, not a hand-written
			// raw pool_submit1 call — exempt from the raw-surface-removed diagnostic.
			SafeConcurrencySugar: true,
		}
		// Inside a nursery (implicit pool), auto-collect the task into the nursery's group so
		// it is joined at scope exit. The submit then yields void (fire-into-nursery): for an
		// individually awaitable handle, use a `pool workers(...)` scope instead.
		if !hasExplicitPool {
			if grpName, ok := p.nurseryGroupForActivePool(); ok {
				return &ast.CallExpr{
					Position: pos,
					Func:     &ast.Ident{Position: pos, Name: "task_group_add"},
					Args:     []ast.Expr{nurseryGroupRef(pos, grpName), submitCall},
				}
			}
		}
		return submitCall
	}
	if p.matchIdentText("await") {
		pos := p.tokens[p.pos-1].Pos
		operand := p.parseUnary()
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_await"},
			Args:     []ast.Expr{&ast.MoveExpr{Position: pos, Operand: operand}},
		}
	}
	if p.matchIdentText("move") {
		pos := p.tokens[p.pos-1].Pos
		operand := p.parseUnary()
		return &ast.MoveExpr{Position: pos, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_TILDE {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_TILDE, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_BANG {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_BANG, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.AddrOfExpr{Position: pos, Operand: operand}
	}
	return p.parsePostfix()
}
func (p *Parser) parseAllocExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	var owner ast.Expr
	autoRegion := false
	explicitAutoRegion := false
	if p.match(lexer.TOKEN_LBRACKET) {
		owner = p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		// `new[auto]`: infer the region (native stack arena) like a container's backing. Represent
		// it with the AutoRegion flag and a nil Owner so no pass tries to resolve `auto` as an
		// identifier; the flag disambiguates the nil Owner from a bracket-less packed `new`.
		if ident, ok := owner.(*ast.Ident); ok && ident != nil && ident.Name == "auto" {
			owner = nil
			autoRegion = true
			explicitAutoRegion = true
		}
	}
	value := p.parseExpr()
	return &ast.AllocExpr{Position: pos, Owner: owner, Value: value, AutoRegion: autoRegion, ExplicitAutoRegion: explicitAutoRegion}
}
func (p *Parser) looksLikeNodeAllocExpr() bool {
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		return true
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
			if depth == 0 {
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_IDENT
			}
		case lexer.TOKEN_EOF, lexer.TOKEN_NEWLINE:
			return false
		}
	}
	return false
}
func (p *Parser) parseCatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CATCH)
	value := p.withInMembershipDisabled(p.parseExpr)
	success, arms := p.parseCatchArms()
	return &ast.CatchExpr{Position: pos, Value: value, Success: success, Arms: arms}
}

// prevTokenIsDedent reports whether the token consumed immediately before the current
// position was a DEDENT — the boundary a multi-line block expression (match arms, an
// indented value block) leaves behind. A postfix-ternary `if` directly after that
// boundary belongs to the NEXT statement.
func (p *Parser) prevTokenIsDedent() bool {
	return p.pos > 0 && p.tokens[p.pos-1].Kind == lexer.TOKEN_DEDENT
}
