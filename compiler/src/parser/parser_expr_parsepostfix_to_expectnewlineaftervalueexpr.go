package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		// Column scan: `<EnumName> of .field` (docs/76 §5). Tightly guarded —
		// only when the left side is a bare type name, the soft keyword `of`
		// follows, and a `.field` selector comes next. This sequence is not
		// otherwise valid, so it cannot shadow ordinary expressions.
		if ident, ok := expr.(*ast.Ident); ok &&
			p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "of" &&
			p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_DOT {
			pos := p.cur().Pos
			p.advance() // consume `of`
			p.expect(lexer.TOKEN_DOT)
			field := p.expect(lexer.TOKEN_IDENT).Text
			expr = &ast.EnumColumnExpr{Position: pos, Enum: ident.Name, Field: field}
			continue
		}
		switch p.peek() {
		case lexer.TOKEN_QUESTION:
			if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_DOT {
				return expr
			}
			pos := p.cur().Pos
			receiver := expr
			p.advance()
			p.expect(lexer.TOKEN_DOT)
			if p.peek() == lexer.TOKEN_LPAREN {
				p.advance()
				fn := p.parseExpr()
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CallExpr{Position: pos, Func: fn, SafeReceiver: receiver, Safe: true}
				continue
			}
			field := p.expect(lexer.TOKEN_IDENT).Text
			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field, Safe: true}

		case lexer.TOKEN_DOT:
			pos := p.cur().Pos
			p.advance()
			field := p.expect(lexer.TOKEN_IDENT).Text

			if field == "cast" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				target := p.parseTypeExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				if p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
					p.errorf("legacy cast syntax `.cast[T]()` is no longer supported; use `.cast[T]` instead")
					p.advance()
					p.advance()
				}
				expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target, Origin: ast.CastExprOriginExplicitCast}
				continue
			}

			if field == "ref" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				target := p.parseTypeExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				expr = &ast.CastExpr{
					Position:     pos,
					Operand:      &ast.AddrOfExpr{Position: pos, Operand: expr},
					Target:       target,
					RefShorthand: true,
				}
				continue
			}

			if field == "call_as" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				sig := p.parseTypeExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				if _, ok := sig.(*ast.FuncTypeExpr); !ok {
					p.errorAt(pos, "`.call_as[T]` requires a function-signature type, e.g. `.call_as[func(int) -> int]`")
				}
				castExpr := &ast.CastExpr{Position: pos, Operand: expr, Target: sig, Origin: ast.CastExprOriginIndirectCall}
				p.expect(lexer.TOKEN_LPAREN)
				args, argNames, argShorthand, paramPacks, argItems, hasArgForward, argForwardPos := p.parseCallArgs()
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CallExpr{Position: pos, Func: castExpr, HasArgForward: hasArgForward, ArgForwardPos: argForwardPos, Args: args, ArgNames: argNames, ArgShorthand: argShorthand, ParamPacks: paramPacks, ArgItemOrder: argItems}
				continue
			}

			if field == "specialize" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				typeArgs := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
				if p.peek() != lexer.TOKEN_RBRACKET {
					for {
						typeArgs = append(typeArgs, p.parseTypeExpr())
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				p.expect(lexer.TOKEN_LPAREN)
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.SpecializeExpr{Position: pos, Operand: expr, TypeArgs: typeArgs, Legacy: true}
				continue
			}

			// Legacy reference-cast detection for `expr.Field!(operand)`. NOTE: do
			// NOT include TOKEN_AMPERSAND here — `expr.field & (rhs)` is a valid
			// bitwise-AND whose RHS is parenthesized, and `&` after a member access
			// must fall through to the binary-operator parser. Treating `& (` as a
			// legacy cast caused a spurious parse error (the legacy cast syntax is
			// removed anyway; `.cast[T&]` is the replacement).
			if p.peek() == lexer.TOKEN_BANG {
				castPos := pos
				savedCastPos := p.pos
				var target ast.TypeExpr = &ast.NamedType{Position: castPos, Name: field}
				target, _ = p.parseRefTypeSuffixes(target, castPos, ast.RefStorageAny, false, "")
				if p.peek() == lexer.TOKEN_LPAREN {
					p.errorf("legacy reference cast syntax is no longer supported; use .cast[T&] with an explicit target type instead")
					p.advance()
					p.expect(lexer.TOKEN_RPAREN)
					expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
					continue
				}
				p.pos = savedCastPos
			}

			if isPostfixShorthandCastTarget(field) && p.peek() == lexer.TOKEN_QUESTION && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN && p.tokens[p.pos+2].Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance()
				p.advance()
				p.advance()
				target := &ast.OptionalTypeExpr{Position: castPos, Value: &ast.NamedType{Position: castPos, Name: field}}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target, Origin: ast.CastExprOriginPostfixShorthand}
				continue
			}

			if isPostfixShorthandCastTarget(field) && p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance()
				p.advance()
				target := &ast.NamedType{Position: castPos, Name: field}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target, Origin: ast.CastExprOriginPostfixShorthand}
				continue
			}

			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}

		case lexer.TOKEN_ARROW:
			pos := p.cur().Pos
			p.advance()
			target := p.parseTypeExpr()
			p.errorAt(pos, "the `expr -> T` cast has been removed; use `expr.cast[T]` (reinterpret), a constructor `T(expr)` / `expr.T()` (value conversion), or `&expr` (reference)")
			expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target}

		case lexer.TOKEN_LBRACE:
			pos := p.cur().Pos
			p.advance()
			args, argNames := p.parseRecordUpdateFields()
			p.expect(lexer.TOKEN_RBRACE)
			expr = &ast.RecordUpdateExpr{Position: pos, Base: expr, Args: args, ArgNames: argNames}

		case lexer.TOKEN_LBRACKET:
			pos := p.cur().Pos
			// Generic-function VALUE specialization with 2+ type args and no trailing call:
			// `fn[A, B]` materializes the generic function as a value (the modern replacement
			// for `fn.specialize[A, B]()`). A multi-element bracket cannot be an index, so this
			// is unambiguous. The single-arg value form `fn[T]` stays an IndexExpr here and is
			// reinterpreted as a specialization in the analyzer when the operand is a function.
			if p.peekPostfixGenericValueApplication() {
				p.advance()
				typeArgs := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
				for {
					typeArgs = append(typeArgs, p.parseGenericTypeArgExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				expr = &ast.SpecializeExpr{Position: pos, Operand: expr, TypeArgs: typeArgs}
				continue
			}
			if p.peekPostfixGenericApplication() {
				p.advance()
				typeArgs := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
				for {
					typeArgs = append(typeArgs, p.parseGenericTypeArgExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				p.expect(lexer.TOKEN_LPAREN)
				args, argNames, argShorthand, paramPacks, argItems, hasArgForward, argForwardPos := p.parseCallArgs()
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CallExpr{Position: pos, Func: &ast.SpecializeExpr{Position: pos, Operand: expr, TypeArgs: typeArgs}, HasArgForward: hasArgForward, ArgForwardPos: argForwardPos, Args: args, ArgNames: argNames, ArgShorthand: argShorthand, ParamPacks: paramPacks, ArgItemOrder: argItems}
				expr = p.attachOptionalCallWithClause(expr)
				continue
			}
			p.advance()
			start := p.parseExpr()
			if p.match(lexer.TOKEN_COLON) {
				end := p.parseExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				expr = &ast.SliceExpr{Position: pos, Object: expr, Start: start, End: end}
				continue
			}
			p.expect(lexer.TOKEN_RBRACKET)
			expr = &ast.IndexExpr{Position: pos, Object: expr, Index: start}
			// A trailing `else <value>` is a postfix index fallback. Recovery forms
			// (`else return`, `else raise`, `else void`, `else binding:`) are left
			// for the expression-level recovery handling, so that e.g.
			// `get arr[i] else return null` folds the recovery into the `get`.
			if p.peek() == lexer.TOKEN_ELSE && !p.elseIntroducesRecoveryClause() {
				p.advance()
				expr.(*ast.IndexExpr).Fallback = p.parseOr()
			}

		case lexer.TOKEN_LPAREN:
			pos := p.cur().Pos
			p.advance()
			var args []ast.Expr
			var argNames []string
			var argShorthand []bool
			var paramPacks []ast.ParamPackUse
			var argItems []ast.CallArgItem
			var hasArgForward bool
			var argForwardPos lexer.Pos
			if ident, ok := expr.(*ast.Ident); ok && ident.Name == "children" {
				args, argNames = p.parseChildrenCallArgs()
			} else {
				args, argNames, argShorthand, paramPacks, argItems, hasArgForward, argForwardPos = p.parseCallArgs()
			}
			p.expect(lexer.TOKEN_RPAREN)
			safe := false
			callFunc := expr
			if fieldExpr, ok := expr.(*ast.FieldExpr); ok && fieldExpr != nil && fieldExpr.Safe {
				callFunc = &ast.FieldExpr{Position: fieldExpr.Position, Object: fieldExpr.Object, Field: fieldExpr.Field}
				safe = true
			}
			expr = &ast.CallExpr{
				Position:      pos,
				Func:          callFunc,
				HasArgForward: hasArgForward,
				ArgForwardPos: argForwardPos,
				Args:          args,
				ArgNames:      argNames,
				ArgShorthand:  argShorthand,
				ParamPacks:    paramPacks,
				ArgItemOrder:  argItems,
				Safe:          safe,
			}
			expr = p.attachOptionalCallWithClause(expr)

		default:
			return expr
		}
	}
}
func (p *Parser) parsePrimary() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: false}
	case lexer.TOKEN_FLOAT_LIT:
		tok := p.advance()
		return &ast.FloatLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix}
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: true}
	case lexer.TOKEN_STRING_LIT:
		tok := p.advance()
		return &ast.StringLit{Position: tok.Pos, Value: tok.Text}
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
				if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT {
					op := p.advance()
					first = &ast.MembershipRangeExpr{Position: op.Pos, Start: first, End: p.parseExpr(), Op: op.Kind}
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
			value := p.parseOr()
			if p.matchIdentText("default") {
				_ = p.parseExpr() // consume and discard the legacy fallback for recovery
			}
			return &ast.TryExpr{Position: pos, Value: value}
		}
		value := p.parseOr()
		return &ast.TryExpr{Position: pos, Value: value}
	case lexer.TOKEN_CATCH:
		return p.parseCatchExpr()
	case lexer.TOKEN_MATCH:
		return p.parseMatchExpr()
	case lexer.TOKEN_RAISE:
		pos := p.cur().Pos
		p.advance()
		errExpr := p.parseOr()
		return &ast.RaiseExpr{Position: pos, Error: errExpr}
	case lexer.TOKEN_IDENT:
		if p.looksLikeQueryExpr() {
			return p.parseQueryExpr()
		}
		if p.cur().Text == "do" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			pos := p.cur().Pos
			p.advance()
			return p.parseExprBlockValue(pos, false)
		}
		if (p.cur().Text == "lambda" || p.cur().Text == "λ") && p.looksLikeLambdaExpr() {
			return p.parseLambdaExpr()
		}
		if p.cur().Text == "cascade" && p.looksLikeCascadeExpr() {
			pos := p.cur().Pos
			p.advance()
			target := p.parseExpr()
			p.expect(lexer.TOKEN_FATARROW)
			value := p.parseExpr()
			return &ast.CascadeExpr{Position: pos, Target: target, Value: value}
		}
		if p.cur().Text == "visit" && !(p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN) {
			return p.parseVisitExpr()
		}
		if p.cur().Text == "fold" && !(p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN) {
			return p.parseFoldExpr()
		}
		if p.cur().Text == "rewrite" && !(p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN) {
			return p.parseRewriteExpr()
		}
		// Contextual `get` prefix: the optional analog of `try`. Recognized only
		// when immediately followed by an identifier (the start of the operand),
		// so call sites like `get(x)`, method calls `x.get(i)`, indexing `get[i]`,
		// and bare `get` identifiers are left untouched. A trailing `else` is
		// folded into the GetExpr at parseExpr, mirroring TryExpr.
		if p.cur().Text == "get" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
			pos := p.cur().Pos
			p.advance()
			value := p.parseOr()
			return &ast.GetExpr{Position: pos, Value: value}
		}
		tok := p.advance()
		if p.peek() == lexer.TOKEN_SCOPE {
			return &ast.Ident{Position: tok.Pos, Name: p.parseQualifiedIdentNameAfterFirst(tok.Text)}
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
			args, argNames, spreads := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads}
		}
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames := p.parseStructLiteralParenArgs()
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames}
		}
		if p.peek() == lexer.TOKEN_LBRACE && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames, spreads := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames, Brace: true, Spreads: spreads}
		}
		return &ast.Ident{Position: tok.Pos, Name: tok.Text}
	case lexer.TOKEN_LPAREN:
		pos := p.cur().Pos
		p.advance()
		inner := p.withInMembershipEnabled(p.parseExpr)
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
		p.errorf("unexpected token %s in expression", p.cur())
		tok := p.advance()
		return &ast.Ident{Position: tok.Pos, Name: "<error>"}
	}
}

func (p *Parser) parseLayoutIntrospectionTypeArg() ast.TypeExpr {
	if p.match(lexer.TOKEN_LBRACKET) {
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return typ
	}
	p.expect(lexer.TOKEN_LPAREN)
	typ := p.parseTypeExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return typ
}

func (p *Parser) parseLayoutIntrospectionFieldArg() lexer.Token {
	if p.peek() == lexer.TOKEN_RPAREN {
		p.errorf("expected field name")
		return lexer.Token{}
	}
	if p.match(lexer.TOKEN_DOT) {
		// Dot-prefixed field names make offset_of[T](.field) read like
		// a field selector without requiring a value expression.
	}
	return p.expect(lexer.TOKEN_IDENT)
}

func (p *Parser) parseBraceMembershipCandidateExpr() ast.Expr {
	start := p.parseExpr()
	if p.peek() != lexer.TOKEN_RANGE && p.peek() != lexer.TOKEN_RANGE_LT {
		return start
	}
	op := p.advance()
	end := p.parseExpr()
	return &ast.MembershipRangeExpr{Position: op.Pos, Start: start, End: end, Op: op.Kind}
}
func (p *Parser) parseExprBlockValue(pos lexer.Pos, flattenSingle bool) ast.Expr {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	if len(body) == 0 {
		p.errorf("expression block requires a final expression statement in the block")
		return &ast.NullLit{Position: pos}
	}
	var value ast.Expr
	switch tail := body[len(body)-1].(type) {
	case *ast.ExprStmt:
		if tail == nil || tail.Expr == nil {
			p.errorf("expression block requires a final expression statement in the block")
			return &ast.NullLit{Position: pos}
		}
		value = tail.Expr
	default:
		p.errorf("expression block requires a final expression statement in the block")
		return &ast.NullLit{Position: pos}
	}
	if flattenSingle && len(body) == 1 {
		return value
	}
	stmts := append([]ast.Stmt(nil), body[:len(body)-1]...)
	return &ast.ExprBlock{Position: pos, Stmts: stmts, Value: value}
}
func (p *Parser) peekStructLiteralTypeArgsFollowedBy(close lexer.TokenKind) bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
			if depth == 0 {
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == close
			}
		}
	}
	return false
}
func (p *Parser) parseStructLiteralTypeArgs() []ast.TypeExpr {
	p.expect(lexer.TOKEN_LBRACKET)
	args := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	if p.peek() != lexer.TOKEN_RBRACKET {
		for {
			args = append(args, p.parseGenericTypeArgExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return args
}
func (p *Parser) parseStructLiteralBraceFields() ([]ast.Expr, []string, []ast.Expr) {
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	argNames := make([]string, 0, cap(args))
	spreads := make([]ast.Expr, 0, 1)
	if p.peek() == lexer.TOKEN_RBRACE {
		return args, argNames, nil
	}
	for {
		if p.match(lexer.TOKEN_ELLIPSIS) {
			spreads = append(spreads, p.parseExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACE {
				break
			}
			continue
		}
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		value := ast.Expr(&ast.Ident{Position: pos, Name: name})
		if p.match(lexer.TOKEN_COLON) {
			value = p.parseExpr()
		}
		args = append(args, value)
		argNames = append(argNames, name)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RBRACE {
			break
		}
	}
	return args, argNames, spreads
}
func (p *Parser) parseStructLiteralParenArgs() ([]ast.Expr, []string) {
	args, argNames, _, packs, _, hasArgForward, _ := p.parseCallArgs()
	if hasArgForward {
		p.errorf("struct constructor arguments do not support call forwarding `..`")
	}
	for range packs {
		p.errorf("struct constructor arguments do not support parameter-pack application")
	}
	return args, argNames
}
func (p *Parser) parseRecordUpdateFields() ([]ast.Expr, []string) {
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	argNames := make([]string, 0, cap(args))
	if p.peek() == lexer.TOKEN_RBRACE {
		return args, argNames
	}
	for {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		value := ast.Expr(&ast.Ident{Position: pos, Name: name})
		if p.match(lexer.TOKEN_ASSIGN) {
			value = p.parseExpr()
		}
		args = append(args, value)
		argNames = append(argNames, name)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RBRACE {
			break
		}
	}
	return args, argNames
}
func (p *Parser) expectNewline() {
	if p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_SEMICOLON {
		p.advance()
	} else if p.peek() == lexer.TOKEN_EOF || p.peek() == lexer.TOKEN_DEDENT {
		// OK at end of file or block
	} else {
		p.errorf("expected newline, got %s", p.cur())
	}
}
func (p *Parser) expectNewlineAfterValueExpr(expr ast.Expr) {
	if _, ok := expr.(*ast.ExprBlock); ok {
		return
	}
	// A catch expression always ends with its arm block (COLON NEWLINE INDENT …
	// DEDENT), so in statement position there is no trailing newline to consume —
	// the same as ExprBlock. Without this, a statement-position `catch …:` followed
	// by another statement fails to parse.
	if _, ok := expr.(*ast.CatchExpr); ok {
		return
	}
	if exprHasBlockRecovery(expr) {
		return
	}
	p.expectNewline()
}

func exprHasBlockRecovery(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.TryExpr:
		return n.Recovery != nil && n.Recovery.Kind == ast.RecoveryBlock
	case *ast.UnwrapElseExpr:
		return n.Recovery != nil && n.Recovery.Kind == ast.RecoveryBlock
	default:
		return false
	}
}
