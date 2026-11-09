package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	p.enterNesting()
	defer p.leaveNesting()
	// Quantifier prefix. Specifications retain quantified formulas; finite runtime
	// ranges lower to a short-circuiting loop. Bare identifiers keep their meaning.
	if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "forall" || p.cur().Text == "exists") && p.quantifierStartsHere() && (p.allowQuantifiers || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT) {
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

	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && (p.tokenAt(p.pos+1).Text == "first" || p.tokenAt(p.pos+1).Text == "each") {
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
		if p.stmtGuardArmed && p.peek() == lexer.TOKEN_COLON {
			cond := p.parseGuardCondition()
			p.stmtGuardArmed = false
			p.stmtGuardCond = cond
			return expr
		}
		cond := p.parseOr()
		// Generalized postfix statement guard (docs/125 §6b): while the statement parser has
		// armed the guard window, `EXPR if COND` ending at a statement terminator is a
		// do-or-skip guard on the enclosing statement, not a malformed ternary. The
		// statement-end test keeps bracketed contexts (`f(a if c)`) on the ternary path.
		if p.stmtGuardArmed && p.peek() != lexer.TOKEN_ELSE && p.stmtEndAhead() {
			p.stmtGuardArmed = false
			p.stmtGuardCond = cond
			return expr
		}
		// Suspend the guard window around the alt parse only: a chained
		// `a if c else b if c2` still demands its second `else` (the alt parses unarmed),
		// while a COMPLETED ternary can still take a statement guard afterwards
		// (`x <- (a if c else b) if cond`).
		savedGuardArmed := p.stmtGuardArmed
		p.stmtGuardArmed = false
		p.expect(lexer.TOKEN_ELSE)
		alt := p.parseExpr()
		p.stmtGuardArmed = savedGuardArmed
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
	// A dedent closes a block-valued expression. A following can block is a
	// sibling statement, not a postfix grant on that expression.
	if !p.prevTokenIsDedent() && p.matchIdentText("can") {
		permissions := p.parsePermissionRefs(false)
		canExpr := &ast.CanExpr{Position: expr.Pos(), Expr: expr, Permissions: permissions}
		// docs/125 §6b: a can-annotated call statement still admits a postfix guard —
		// `f(...) can E if COND` is do-or-skip. The base `if` branch above returned before
		// `can` could attach, so re-check the guard here while the window is still armed.
		if p.allowTernary && p.peek() == lexer.TOKEN_IF && !p.prevTokenIsDedent() {
			p.advance()
			if p.stmtGuardArmed && p.peek() == lexer.TOKEN_COLON {
				cond := p.parseGuardCondition()
				p.stmtGuardArmed = false
				p.stmtGuardCond = cond
				return canExpr
			}
			cond := p.parseOr()
			if p.stmtGuardArmed && p.peek() != lexer.TOKEN_ELSE && p.stmtEndAhead() {
				p.stmtGuardArmed = false
				p.stmtGuardCond = cond
				return canExpr
			}
			// A completed ternary over a can-value (`(f() can E) if c else d`) is unusual
			// but well-formed; build it so nothing is silently dropped.
			savedGuardArmed := p.stmtGuardArmed
			p.stmtGuardArmed = false
			p.expect(lexer.TOKEN_ELSE)
			alt := p.parseExpr()
			p.stmtGuardArmed = savedGuardArmed
			return &ast.TernaryExpr{Position: canExpr.Pos(), Value: canExpr, Cond: cond, Alt: alt}
		}
		return canExpr
	}

	return expr
}

// quantifierStartsHere confirms a `forall`/`exists` keyword is followed by a binder identifier (so a
// variable literally named `forall` used in expression position is not misparsed as a quantifier).
func (p *Parser) quantifierStartsHere() bool {
	return p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LPAREN)
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
		body := p.parseQuantifierBody()
		if !isIndexRange {
			if !p.allowQuantifiers {
				p.errorAt(pos, "runtime quantifiers require an explicit finite range")
				return &ast.BoolLit{Position: pos}
			}
			return &ast.QuantifierExpr{Position: pos, Exists: exists, Vars: vars, In: source, Body: body}
		} else if len(vars) != 1 {
			p.errorAt(pos, "`forall i in <range>:` binds exactly one index variable")
			return &ast.BoolLit{Position: pos}
		}
		if !p.allowQuantifiers {
			return p.runtimeRangeQuantifier(pos, exists, vars[0], lo, hi, body)
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
	body := p.parseQuantifierBody()
	if !p.allowQuantifiers {
		p.errorAt(pos, "unbounded quantifiers are specification-only; use a finite range at runtime")
		return &ast.BoolLit{Position: pos}
	}
	return &ast.QuantifierExpr{Position: pos, Exists: exists, Vars: vars, Body: body}
}

// parseQuantifierBody accepts a predicate on the header line or in an indented block.
func (p *Parser) parseQuantifierBody() ast.Expr {
	if p.peek() != lexer.TOKEN_NEWLINE {
		return p.parseExpr()
	}
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	body := p.parseExpr()
	p.expectNewlineAfterValueExpr(body)
	p.expect(lexer.TOKEN_DEDENT)
	return body
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
