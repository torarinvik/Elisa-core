package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parsePrimary() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString(), IsHex: false}
	case lexer.TOKEN_FLOAT_LIT:
		tok := p.advance()
		return &ast.FloatLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString()}
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString(), IsHex: true}
	case lexer.TOKEN_STRING_LIT:
		tok := p.advance()
		return &ast.StringLit{Position: tok.Pos, Value: tok.Text}
	case lexer.TOKEN_FSTRING_LIT:
		tok := p.advance()
		return p.desugarFString(tok)
	case lexer.TOKEN_CHAR_LIT:
		tok := p.advance()
		return &ast.CharLit{Position: tok.Pos, Value: tok.Text}
	case lexer.TOKEN_NULL:
		tok := p.advance()
		return &ast.NullLit{Position: tok.Pos}
	case lexer.TOKEN_TRUE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: true}
	case lexer.TOKEN_FALSE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: false}
	case lexer.TOKEN_ZEROED:
		tok := p.advance()
		return &ast.ZeroedLit{Position: tok.Pos}
	case lexer.TOKEN_DOT:
		pos := p.cur().Pos
		p.advance()
		parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
		for p.match(lexer.TOKEN_DOT) {
			parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		}
		return &ast.ShorthandMemberExpr{Position: pos, Parts: parts}
	case lexer.TOKEN_LBRACKET:
		pos := p.cur().Pos
		p.advance()
		elems := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
		spreads := make([]bool, 0, cap(elems))
		if p.peek() != lexer.TOKEN_RBRACKET {
			firstSpread := p.match(lexer.TOKEN_ELLIPSIS)
			first := p.parseExpr()
			if ident, ok := first.(*ast.Ident); ok && !firstSpread && (p.peek() == lexer.TOKEN_ASSIGN || p.peek() == lexer.TOKEN_COLON) {
				// Binding-prefixed list comprehension: `[ name [:T] = e, ... , body for ... ]`.
				bindings := p.parseComprehensionHeadBindings(pos, ident.Name, true)
				body := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
				comp := p.parseListComprehensionFromFirst(pos, body)
				if lc, ok := comp.(*ast.ListComprehensionExpr); ok {
					lc.Bindings = bindings
				}
				return comp
			}
			if !firstSpread && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
				return p.parseListComprehensionFromFirst(pos, first)
			}
			elems = append(elems, first)
			spreads = append(spreads, firstSpread)
			for p.match(lexer.TOKEN_COMMA) {
				if p.peek() == lexer.TOKEN_RBRACKET {
					break
				}
				spread := p.match(lexer.TOKEN_ELLIPSIS)
				elems = append(elems, p.parseExpr())
				spreads = append(spreads, spread)
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
		var owner ast.Expr
		if p.match(lexer.TOKEN_IN) {
			// The expression-owner `[...] in <owner>` is removed (docs/68 §7): annotate
			// the binding type with `@<owner>` instead. Recover by keeping the owner so the
			// rest still analyzes and only this diagnostic surfaces.
			owner = p.withInMembershipDisabled(p.parseExpr)
			p.errorf("list allocation owner `[...] in <owner>` is no longer supported; annotate the binding type instead (e.g. `xs: darray[T] @<owner> = [...]`)")
		}
		return &ast.ListLitExpr{Position: pos, Elems: elems, Spreads: spreads, Owner: owner}
	case lexer.TOKEN_LBRACE:
		pos := p.cur().Pos
		p.advance()
		elems := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
		var keys []ast.Expr // non-nil => dict literal `{k: v, ...}`
		if p.peek() != lexer.TOKEN_RBRACE {
			first := p.parseExpr()
			if ident, ok := first.(*ast.Ident); ok && p.peek() == lexer.TOKEN_ASSIGN {
				// Binding-prefixed dict/set comprehension. Only `IDENT =` starts a binding here
				// (an `IDENT :` is a dict key:value), so head bindings inside {...} are untyped.
				bindings := p.parseComprehensionHeadBindings(pos, ident.Name, false)
				bodyOrKey := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
				if p.peek() == lexer.TOKEN_COLON {
					p.advance()
					value := p.parseExpr()
					comp := p.parseDictComprehensionFromFirst(pos, bodyOrKey, value)
					comp.(*ast.ListComprehensionExpr).Bindings = bindings
					return comp
				}
				comp := p.parseSetComprehensionFromFirst(pos, bodyOrKey)
				comp.(*ast.ListComprehensionExpr).Bindings = bindings
				return comp
			}
			if p.peek() == lexer.TOKEN_COLON {
				// Dict literal: `first` is the first key. Parse `key: value` pairs.
				p.advance()
				firstValue := p.parseExpr()
				if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
					// Dict comprehension `{ key: value for name in source [if filter] }`.
					return p.parseDictComprehensionFromFirst(pos, first, firstValue)
				}
				keys = append(keys, first)
				elems = append(elems, firstValue)
				for p.match(lexer.TOKEN_COMMA) {
					if p.peek() == lexer.TOKEN_RBRACE {
						break
					}
					keys = append(keys, p.parseExpr())
					p.expect(lexer.TOKEN_COLON)
					elems = append(elems, p.parseExpr())
				}
			} else if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
				// Set comprehension `{ value for name in source [if filter] }`.
				return p.parseSetComprehensionFromFirst(pos, first)
			} else {
				// Set-membership literal: `first` may be a range; remaining are candidates.
				if p.membershipRangeOpAhead() {
					opKind, opPos := p.consumeValueRangeOp()
					first = &ast.MembershipRangeExpr{Position: opPos, Start: first, End: p.parseExpr(), Op: opKind}
				}
				elems = append(elems, first)
				for p.match(lexer.TOKEN_COMMA) {
					if p.peek() == lexer.TOKEN_RBRACE {
						break
					}
					elems = append(elems, p.parseBraceMembershipCandidateExpr())
				}
			}
		}
		p.expect(lexer.TOKEN_RBRACE)
		return &ast.ListLitExpr{Position: pos, Elems: elems, Keys: keys, Brace: true}
	case lexer.TOKEN_SIZEOF:
		pos := p.cur().Pos
		syntax := p.cur().Text
		p.advance()
		typ := p.parseLayoutIntrospectionTypeArg()
		if p.match(lexer.TOKEN_LPAREN) {
			p.expect(lexer.TOKEN_RPAREN)
		}
		if syntax == "sizeof" {
			p.errorAt(pos, "`sizeof` has been removed; use `size_of`")
		}
		return &ast.SizeofExpr{Position: pos, Type: typ}
	case lexer.TOKEN_ALIGNOF:
		pos := p.cur().Pos
		syntax := p.cur().Text
		p.advance()
		typ := p.parseLayoutIntrospectionTypeArg()
		if p.match(lexer.TOKEN_LPAREN) {
			p.expect(lexer.TOKEN_RPAREN)
		}
		if syntax == "alignof" {
			p.errorAt(pos, "`alignof` has been removed; use `align_of`")
		}
		return &ast.AlignofExpr{Position: pos, Type: typ}
	case lexer.TOKEN_OFFSETOF:
		pos := p.cur().Pos
		syntax := p.cur().Text
		p.advance()
		var typ ast.TypeExpr
		var field lexer.Token
		if p.match(lexer.TOKEN_LBRACKET) {
			typ = p.parseTypeExpr()
			p.expect(lexer.TOKEN_RBRACKET)
			p.expect(lexer.TOKEN_LPAREN)
			field = p.parseLayoutIntrospectionFieldArg()
			p.expect(lexer.TOKEN_RPAREN)
		} else {
			p.expect(lexer.TOKEN_LPAREN)
			typ = p.parseTypeExpr()
			p.expect(lexer.TOKEN_COMMA)
			field = p.parseLayoutIntrospectionFieldArg()
			p.expect(lexer.TOKEN_RPAREN)
		}
		if syntax == "offsetof" {
			p.errorAt(pos, "`offsetof` has been removed; use `offset_of`")
		}
		return &ast.OffsetofExpr{Position: pos, Type: typ, Field: field.Text}
	case lexer.TOKEN_TRY:
		pos := p.cur().Pos
		p.advance()
		if p.match(lexer.TOKEN_QUESTION) {
			p.errorAt(pos, "`try? ... default` is no longer supported; use `try ... else ...`")
			p.skipRemovedQuestionExprTail()
			return &ast.Ident{Position: pos, Name: "__invalid_removed_try_question"}
		}
		value := p.parseOr()
		return &ast.TryExpr{Position: pos, Value: value}
	case lexer.TOKEN_CATCH:
		return p.parseCatchExpr()
	case lexer.TOKEN_WHILE:
		// Same-line value-form loop (`x = while cond |i = 0| -> i: …`). The block-RHS
		// form (`x: T =` NEWLINE INDENT `while …`) is handled earlier by the `=` site;
		// reaching here means `while` sits directly in value position.
		return p.parseLoopValueExpr("while")
	case lexer.TOKEN_MATCH:
		return p.parseMatchExpr()
	case lexer.TOKEN_RAISE:
		pos := p.cur().Pos
		p.advance()
		errExpr := p.parseOr()
		return &ast.RaiseExpr{Position: pos, Error: errExpr}
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "when" && p.looksLikeWhenConstruct() {
			return p.parseWhenExpr()
		}
		if p.cur().Text == "machine" && p.looksLikeMachineFromExpr() {
			return p.parseMachineFromExpr()
		}
		// Same-line value-form loop (`x = for i in xs |acc = 0| -> acc: …`). `for` is a
		// contextual ident, so it reaches here in value position; the block-RHS form is
		// handled earlier by the `=` site. looksLikeForStmt keeps a variable named `for`
		// from being mistaken for a loop.
		if p.cur().Text == "for" && p.looksLikeForStmt() {
			return p.parseLoopValueExpr("for")
		}
		// docs/125 §5 local-state sugar: `start State …:` runs a `machine from` over the
		// function's inline `state` declarations. Value-expression atom like `machine from`.
		if p.cur().Text == "start" && p.looksLikeStartExpr() {
			return p.parseStartExpr()
		}
		if p.looksLikeQueryExpr() {
			return p.parseQueryExpr()
		}
		if p.cur().Text == "do" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
			pos := p.cur().Pos
			// docs/119 §2: the bare block form (`x =` NEWLINE INDENT ... tail) supersedes
			// `do:`. In positions the bare form cannot express (call arguments, list
			// elements), bind the block to a local first and pass the local.
			p.noticeAt(pos, "`do:` expression blocks are deprecated; use the bare block form (`x =` followed by an indented block) or bind to a local first (docs/119)")
			p.advance()
			return p.parseExprBlockValue(pos, false)
		}
		if isLambdaKeyword(p.cur().Text) && p.looksLikeLambdaExpr() {
			return p.parseLambdaExpr()
		}
		if p.cur().Text == "cascade" && p.looksLikeCascadeExpr() {
			pos := p.cur().Pos
			p.advance()
			target := p.parseExpr()
			p.errorAt(pos, "the `cascade` expression has been removed")
			if p.match(lexer.TOKEN_FATARROW) {
				_ = p.parseExpr() // discard the cascade value for recovery
			}
			return target
		}
		if p.cur().Text == "visit" && !(p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LPAREN) {
			p.errorf("the `visit` expression has been removed with the `tree` construct (docs/81); use `match` over the `enum … is` hierarchy")
			return p.parseExprAfterRemovedPrefixKeyword()
		}
		if p.cur().Text == "fold" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
			p.errorf("the tree `fold … as … into …` expression has been removed (docs/81); use a recursive function with `match` over the `enum … is` hierarchy")
			return p.parseExprAfterRemovedPrefixKeyword()
		}
		// Contextual `get` prefix: the optional analog of `try`. Recognized only
		// when immediately followed by an identifier (the start of the operand),
		// so call sites like `get(x)`, method calls `x.get(i)`, indexing `get[i]`,
		// and bare `get` identifiers are left untouched. A trailing `else` is
		// folded into the GetExpr at parseExpr, mirroring TryExpr.
		if p.cur().Text == "get" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
			pos := p.cur().Pos
			p.advance()
			value := p.parseOr()
			return &ast.GetExpr{Position: pos, Value: value}
		}
		tok := p.advance()
		// `atomic[T]` is a lowercase builtin generic struct (registered by the
		// semantic analyzer). Permit its brace literal even though user-declared
		// struct constructors follow the capitalized-name convention. Keeping this
		// exception keyed to the builtin avoids reinterpreting lowercase indexed
		// values such as `items[index]{field: value}` as type constructors.
		if tok.Text == "atomic" && p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LBRACE) {
			typeArgs := p.parseStructLiteralTypeArgs()
			p.expect(lexer.TOKEN_LBRACE)
			args, argNames, spreads, copyBase := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads, CopyBase: copyBase}
		}
		if p.peek() == lexer.TOKEN_SCOPE {
			qualified := p.parseQualifiedIdentNameAfterFirst(tok.Text)
			// A `::`-qualified name whose final segment is capitalized supports the
			// same construction forms as a bare type name: `Geo::Point(3, 4)`,
			// `Geo::Point{x: 3}`, and the type-arg variants.
			last := qualified
			if idx := strings.LastIndex(qualified, "."); idx >= 0 {
				last = qualified[idx+1:]
			}
			if len(last) > 0 && last[0] >= 'A' && last[0] <= 'Z' {
				if p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LPAREN) {
					typeArgs := p.parseStructLiteralTypeArgs()
					p.expect(lexer.TOKEN_LPAREN)
					args, argNames := p.parseStructLiteralParenArgs()
					p.expect(lexer.TOKEN_RPAREN)
					return &ast.StructLitExpr{Position: tok.Pos, Name: qualified, TypeArgs: typeArgs, Args: args, ArgNames: argNames}
				}
				if p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LBRACE) {
					typeArgs := p.parseStructLiteralTypeArgs()
					p.expect(lexer.TOKEN_LBRACE)
					args, argNames, spreads, copyBase := p.parseStructLiteralBraceFields()
					p.expect(lexer.TOKEN_RBRACE)
					return &ast.StructLitExpr{Position: tok.Pos, Name: qualified, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads, CopyBase: copyBase}
				}
				if p.peek() == lexer.TOKEN_LPAREN {
					p.advance()
					args, argNames := p.parseStructLiteralParenArgs()
					p.expect(lexer.TOKEN_RPAREN)
					return &ast.StructLitExpr{Position: tok.Pos, Name: qualified, Args: args, ArgNames: argNames}
				}
				if p.peek() == lexer.TOKEN_LBRACE {
					p.advance()
					args, argNames, spreads, copyBase := p.parseStructLiteralBraceFields()
					p.expect(lexer.TOKEN_RBRACE)
					return &ast.StructLitExpr{Position: tok.Pos, Name: qualified, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads, CopyBase: copyBase}
				}
			}
			return &ast.Ident{Position: tok.Pos, Name: qualified}
		}
		if len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' && p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LPAREN) {
			typeArgs := p.parseStructLiteralTypeArgs()
			p.expect(lexer.TOKEN_LPAREN)
			args, argNames := p.parseStructLiteralParenArgs()
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames}
		}
		if len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' && p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LBRACE) {
			typeArgs := p.parseStructLiteralTypeArgs()
			p.expect(lexer.TOKEN_LBRACE)
			args, argNames, spreads, copyBase := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads, CopyBase: copyBase}
		}
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames := p.parseStructLiteralParenArgs()
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames}
		}
		if p.peek() == lexer.TOKEN_LBRACE && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames, spreads, copyBase := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads, CopyBase: copyBase}
		}
		return &ast.Ident{Position: tok.Pos, Name: tok.Text}
	case lexer.TOKEN_LPAREN:
		pos := p.cur().Pos
		p.advance()
		inner := p.withInMembershipEnabled(p.parseExpr)
		if _, ok := inner.(*ast.Ident); ok && p.peek() == lexer.TOKEN_COLON && p.namedTupleValueAhead() {
			return p.parseNamedTupleExprFromFirst(pos)
		}
		if ident, ok := inner.(*ast.Ident); ok && (p.peek() == lexer.TOKEN_ASSIGN || p.peek() == lexer.TOKEN_COLON) {
			// Binding-prefixed fold head: `( name [:T] = e, ... , body for ... with acc = seed )`.
			// `name =`/`name :` can only begin a binding (assignment is not an expression).
			return p.parseFoldComprehensionWithBindings(pos, ident.Name)
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
			return p.parseFoldComprehensionFromFirst(pos, inner)
		}
		if p.peek() == lexer.TOKEN_COMMA {
			tuple := p.parseTupleExprFromFirst(pos, inner)
			p.expect(lexer.TOKEN_RPAREN)
			return tuple
		}
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: pos, Inner: inner}
	default:
		if isReservedWordToken(p.cur()) {
			tok := p.advance()
			p.reportReservedWordAsName(tok)
			return &ast.Ident{Position: tok.Pos, Name: tok.Text}
		}
		p.errorf("unexpected token %s in expression", p.cur())
		tok := p.advance()
		return &ast.Ident{Position: tok.Pos, Name: "<error>"}
	}
}
