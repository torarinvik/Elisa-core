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
	if !p.hasToken(p.pos + 1) {
		return false
	}
	switch p.tokenAt(p.pos + 1).Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_TAIL, lexer.TOKEN_LPAREN:
		return true
	default:
		return false
	}
}

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	p.enterNesting()
	defer p.leaveNesting()
	if p.peekOwnedQualifier() {
		p.advance()
		elem := p.parseTypeExpr()
		return &ast.OwnedType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_MUTABLE) {
		elem := p.parseTypeExpr()
		// `mutable T& error[E]`: the qualifier belongs to the success type. Wrapping the whole
		// union dropped it, since only a reference or a view can be made mutable.
		if union, ok := elem.(*ast.ErrorUnionTypeExpr); ok && union != nil && union.Value != nil {
			union.Value = &ast.MutableType{Position: union.Value.Pos(), Elem: union.Value}
			return union
		}
		return &ast.MutableType{Position: elem.Pos(), Elem: elem}
	}
	// `lmut T` — linear-mutable mode. Layout/codegen identical to `mutable T&`, so we
	// desugar to a mutable reference here (injecting the `&` the source omits, since
	// `lmut` is move-semantics-in-the-source, in-place-in-codegen) and tag it Linear for
	// the linear checker. If the user already wrote a reference (`lmut T&`), don't double-wrap.
	if p.match(lexer.TOKEN_LMUT) {
		elem := p.parseTypeExpr()
		if _, isRef := elem.(*ast.RefType); !isRef {
			elem = &ast.RefType{Position: elem.Pos(), Elem: elem, State: ast.RefStateNonNull, Storage: ast.RefStorageAny}
		}
		return &ast.MutableType{Position: elem.Pos(), Elem: elem, Linear: true}
	}
	if p.match(lexer.TOKEN_TAIL) {
		elem := p.parseTypeExpr()
		return &ast.TailType{Position: elem.Pos(), Elem: elem}
	}
	if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "fn" || p.cur().Text == "func") {
		return p.parseFuncTypeExpr()
	}
	var typ ast.TypeExpr
	if p.peek() == lexer.TOKEN_LPAREN {
		typ = p.parseTupleTypeExpr()
		// parseBaseType normally consumes the reference suffix. A grouped type
		// is already a complete type expression, so consume its suffix here:
		// `(T&) &` must add a reference around, not inside, the parentheses.
		typ, _ = p.parseRefTypeSuffixes(typ, typ.Pos(), ast.RefStorageAny, false, "")
	} else {
		storage, explicit, label, region := p.parseRefStorageQualifier()
		typ = p.parseBaseType(storage, explicit, label, region)
	}
	// Explicit `@r` region-provenance suffix (docs/68 §5): the canonical use-site
	// notation on a container (`darray[T] @r`), reference (`T& @r`), or tuple
	// aggregate whose region-carrying fields all share the same lifetime.
	// On a reference it is equivalent to the legacy region prefix `r T&` -- both
	// populate the same Region field, so the analyzer treats them identically.
	if p.peek() == lexer.TOKEN_AT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		p.advance() // '@'
		regionName := p.advance().Text
		switch t := typ.(type) {
		case *ast.NamedType:
			if t != nil {
				t.Region = regionName
			}
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
		case *ast.TupleTypeExpr:
			if t != nil {
				if t.Region != "" {
					p.errorf("region given twice: `@%s` and `@%s` on the same tuple type", t.Region, regionName)
				}
				t.Region = regionName
			}
		default:
			p.errorf("region annotation `@%s` is only supported on container, generic, reference, and tuple types", regionName)
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
	// docs/85: a refinement-type suffix `Base is Pred[…], …`. Only `is` directly followed by a
	// predicate name in type position; the expression-level `is` operator never reaches here.
	if p.peek() == lexer.TOKEN_IS && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		typ = p.parseRefinementTypeSuffix(typ)
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "where" {
		typ = p.parseWhereRefinementTypeSuffix(typ)
	}
	return typ
}

// parseWhereRefinementTypeSuffix parses `where <bool-expr>` after a type. It deliberately lives after
// named `is Law` refinements, so existing law syntax keeps its precedence and behavior.
func (p *Parser) parseWhereRefinementTypeSuffix(base ast.TypeExpr) ast.TypeExpr {
	pos := p.cur().Pos
	p.expectIdentText("where")
	return &ast.WhereRefinementTypeExpr{Position: pos, Base: base, Predicate: p.parseExpr()}
}

// parseRefinementTypeSuffix parses `is Pred[…]` (and `, Pred` / `and Pred`) after a base type.
func (p *Parser) parseRefinementTypeSuffix(base ast.TypeExpr) ast.TypeExpr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IS)
	preds := []ast.RefinementPredExpr{p.parseRefinementPred()}
	for {
		if p.matchIdentText("and") {
			preds = append(preds, p.parseRefinementPred())
			continue
		}
		// A `,` continues the predicate list ONLY when followed by a parametric
		// predicate (`, Law[`). In a parameter list `(a: T is Law[..], b: U ..)`
		// the comma is the parameter separator (`, name:`), not a predicate
		// separator — consuming it there mis-parses the next parameter. The `[`
		// lookahead disambiguates; additional bare predicates use `and`.
		if p.peek() == lexer.TOKEN_COMMA && p.peekAt(1) == lexer.TOKEN_IDENT && p.peekAt(2) == lexer.TOKEN_LBRACKET {
			p.advance()
			preds = append(preds, p.parseRefinementPred())
			continue
		}
		break
	}
	return &ast.RefinementTypeExpr{Position: pos, Base: base, Preds: preds}
}

// parseRefinementPred parses one predicate: a law name with optional static `[..]` args.
func (p *Parser) parseRefinementPred() ast.RefinementPredExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var args []ast.Expr
	if p.match(lexer.TOKEN_LBRACKET) {
		args = p.parseRefinementPredArgs()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	return ast.RefinementPredExpr{Position: pos, Name: name, Args: args}
}

// ambiguousBareRangeMsg is the shared diagnostic for a bare `..` used as a RANGE operator: its endpoint
// is ambiguous (inclusive in Elisa, exclusive in Rust/Python/Swift), so callers must write `..<` or `..=`.
// `..` remains valid only as the for-loop step separator.
const ambiguousBareRangeMsg = "ambiguous bare `..` range: use `..<` for exclusive (e.g. `lo ..< hi`) or `..=` for inclusive (e.g. `lo ..= hi`)"

// consumeValueRangeOp consumes a just-peeked range operator (`..<` / `..=` / `..>` / bare `..`) for a
// value / membership / comprehension range, returning the kind NORMALIZED so the downstream lowering
// (which keys on TOKEN_RANGE = inclusive, TOKEN_RANGE_LT = exclusive) is unchanged: `..=` maps to the
// inclusive kind, and bare `..` is rejected as ambiguous but recovered as inclusive (single diagnostic).
func (p *Parser) consumeValueRangeOp() (lexer.TokenKind, lexer.Pos) {
	op := p.advance()
	switch op.Kind {
	case lexer.TOKEN_RANGE:
		p.errorAt(op.Pos, ambiguousBareRangeMsg)
		return lexer.TOKEN_RANGE, op.Pos
	case lexer.TOKEN_RANGE_LE:
		return lexer.TOKEN_RANGE, op.Pos
	default:
		return op.Kind, op.Pos
	}
}

func (p *Parser) consumeMembershipRangeOp() (lexer.TokenKind, lexer.Pos) {
	op := p.advance()
	switch op.Kind {
	case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LE:
		return lexer.TOKEN_RANGE, op.Pos
	default:
		return op.Kind, op.Pos
	}
}

// parseRefinementPredArgs parses the comma-separated bracket arguments of a refinement predicate,
// shared by `T is Law[...]` types and `ensures x is Law[...]` postconditions. An inclusive `..=` range is
// sugar for its two endpoints (`Bounded[0 ..= 500]` → `0, 500`); a `..<` range is exclusive
// (`Bounded[0 ..< n]` → `0, n - 1`), which makes `Bounded[0 ..< xs.count]` a dependent length bound. Bare
// `..` is rejected as ambiguous (recovered as inclusive).
func (p *Parser) parseRefinementPredArgs() []ast.Expr {
	var args []ast.Expr
	for p.peek() != lexer.TOKEN_RBRACKET {
		first := p.parseExpr()
		if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LE {
			if p.peek() == lexer.TOKEN_RANGE {
				p.errorAt(p.cur().Pos, ambiguousBareRangeMsg)
			}
			p.advance()
			args = append(args, first, p.parseExpr())
		} else if rangePos := p.cur().Pos; p.match(lexer.TOKEN_RANGE_LT) {
			end := p.parseExpr()
			args = append(args, first, &ast.BinaryExpr{Position: rangePos, Op: lexer.TOKEN_MINUS, Left: end, Right: &ast.IntLit{Position: rangePos, Value: "1"}})
		} else {
			args = append(args, first)
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return args
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
	if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "fn" || p.cur().Text == "func") {
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
	// Parentheses also group a type expression. This is required to compose
	// reference capabilities: `(mutable T&) &` means a readonly reference to a
	// mutable reference, while `mutable (T&) &` marks the outer reference
	// mutable. Named-field tuple types remain unambiguous because their first
	// token pair is `IDENT :`.
	if p.peek() != lexer.TOKEN_RPAREN && !(p.peek() == lexer.TOKEN_IDENT && p.peekAt(1) == lexer.TOKEN_COLON) {
		elem := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return elem
	}
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
	secondName := p.parseComprehensionSecondBinder()
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
		filter = p.parseExpr()
	}
	// Optional `by par` marker on a list-map comprehension (docs/79 Part IV): lowers to a parallel
	// map over the source's disjoint bands (the analyzer does the desugar, once the element type is
	// known). `by simd` was removed — comprehensions vectorize by default (docs/79 Part IV).
	parallel := false
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		mode := p.expect(lexer.TOKEN_IDENT).Text
		switch mode {
		case "simd":
			p.errorAt(byPos, "`by simd` was removed; comprehensions vectorize by default (a non-vectorizable shape warns under -Wperf)")
		case "par":
			parallel = true
		default:
			p.errorAt(byPos, "expected `par` after `by`, got %q", mode)
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
	return &ast.ListComprehensionExpr{Position: pos, Value: value, Name: name, SecondName: secondName, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter, Owner: owner, Parallel: parallel}
}

// parseComprehensionSecondBinder consumes an optional `, IDENT` after a comprehension
// head's first binder — the two-binder tuple-destructuring head `for k, v in src`
// (dict entries, darray-of-tuples), mirroring the statement-level `for k, v in d:`.
func (p *Parser) parseComprehensionSecondBinder() string {
	if !p.match(lexer.TOKEN_COMMA) {
		return ""
	}
	return p.expect(lexer.TOKEN_IDENT).Text
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
	secondName := p.parseComprehensionSecondBinder()
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
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.ListComprehensionExpr{Position: pos, Key: key, Value: value, Name: name, SecondName: secondName, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter}
}

// parseSetComprehensionFromFirst parses a set comprehension
//
//	{ value  for name in source  [if filter] }
//
// producing a ListComprehensionExpr with Set=true (result type set[V]).
func (p *Parser) parseSetComprehensionFromFirst(pos lexer.Pos, value ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	secondName := p.parseComprehensionSecondBinder()
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
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.ListComprehensionExpr{Position: pos, Set: true, Value: value, Name: name, SecondName: secondName, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter}
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
