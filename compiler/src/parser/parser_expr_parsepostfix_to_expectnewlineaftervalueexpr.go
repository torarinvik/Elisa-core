package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	chain := 0
	for {
		// chain counts the postfix operators already applied; the iteration that finds none exits.
		p.checkChainDepth(chain)
		chain++
		// Column scan: `<EnumName> of .field` (docs/76 §5). Tightly guarded —
		// only when the left side is a bare type name, the soft keyword `of`
		// follows, and a `.field` selector comes next. This sequence is not
		// otherwise valid, so it cannot shadow ordinary expressions.
		if ident, ok := expr.(*ast.Ident); ok &&
			p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "of" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_DOT {
			pos := p.cur().Pos
			p.advance() // consume `of`
			p.expect(lexer.TOKEN_DOT)
			field := p.expect(lexer.TOKEN_IDENT).Text
			expr = &ast.EnumColumnExpr{Position: pos, Enum: ident.Name, Field: field}
			continue
		}
		switch p.peek() {
		case lexer.TOKEN_QUESTION:
			if !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_DOT {
				return expr
			}
			pos := p.cur().Pos
			p.errorAt(pos, "optional chaining `x?.…` has been removed; bind the optional explicitly with `if x is v: …`")
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
				if p.peek() == lexer.TOKEN_LPAREN && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_RPAREN {
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
				args, argNames, argShorthand, argItems, hasArgForward, argForwardPos := p.parseCallArgs()
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CallExpr{Position: pos, Func: castExpr, HasArgForward: hasArgForward, ArgForwardPos: argForwardPos, Args: args, ArgNames: argNames, ArgShorthand: argShorthand, ArgItemOrder: argItems}
				continue
			}

			if field == "specialize" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				if p.peek() != lexer.TOKEN_RBRACKET {
					for {
						_ = p.parseTypeExpr()
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				p.expect(lexer.TOKEN_LPAREN)
				p.expect(lexer.TOKEN_RPAREN)
				p.errorAt(pos, "legacy generic specialization `.specialize[T]()` has been removed; use bracket specialization `fn[T]` instead")
				expr = &ast.Ident{Position: pos, Name: "__invalid_removed_specialize"}
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
				p.seek(savedCastPos)
			}

			if isPostfixShorthandCastTarget(field) && p.peek() == lexer.TOKEN_QUESTION && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LPAREN && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance()
				p.advance()
				p.advance()
				target := &ast.OptionalTypeExpr{Position: castPos, Value: &ast.NamedType{Position: castPos, Name: field}}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target, Origin: ast.CastExprOriginPostfixShorthand}
				continue
			}

			if isPostfixShorthandCastTarget(field) && p.peek() == lexer.TOKEN_LPAREN && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance()
				p.advance()
				target := &ast.NamedType{Position: castPos, Name: field}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target, Origin: ast.CastExprOriginPostfixShorthand}
				continue
			}

			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}

		case lexer.TOKEN_ARROW:
			// A multi-line block expression (catch/match arms, an indented value block)
			// ends on a DEDENT. An `->` directly after that boundary opens the NEXT
			// statement — a machine arm's `-> State` transition — and is not a cast of
			// the block. Claiming it here SWALLOWED the transition: the arm then reported
			// the removed `expr -> T` cast plus a spurious "arm makes no decision", for
			// source the arm law itself accepts (and which parses once any plain statement
			// sits in between). Same boundary rule the postfix ternary `if` already uses.
			// Safe by construction: `expr -> T` is a REMOVED form that only ever produces
			// an error here, so declining it cannot change any accepted program.
			if p.prevTokenIsDedent() {
				return expr
			}
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
			// A multi-element bracket cannot be an index, so this
			// is unambiguous. The single-arg value form `fn[T]` stays an IndexExpr here and is
			// reinterpreted as a specialization in the analyzer when the operand is a function.
			//
			// A field access `x.field[a, b]` is never a generic-function value specialization (you
			// specialize a named generic function, not a field projection), so it is excluded here
			// and routes to the two-operand index form below — that is the docs/108 fixed-stride
			// table accessor `table.field[mem, i]`.
			_, operandIsField := expr.(*ast.FieldExpr)
			if !operandIsField && p.peekPostfixGenericValueApplication() {
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
				args, argNames, argShorthand, argItems, hasArgForward, argForwardPos := p.parseCallArgs()
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CallExpr{Position: pos, Func: &ast.SpecializeExpr{Position: pos, Operand: expr, TypeArgs: typeArgs}, HasArgForward: hasArgForward, ArgForwardPos: argForwardPos, Args: args, ArgNames: argNames, ArgShorthand: argShorthand, ArgItemOrder: argItems}
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
			// Two-operand index `obj[a, b]` (docs/108 fixed-stride table overlay accessor
			// `table.field[mem, i]`). Parsed structurally here; the analyzer accepts it only as an
			// overlay table read and rejects any other two-operand index.
			var index2 ast.Expr
			if p.match(lexer.TOKEN_COMMA) {
				index2 = p.parseExpr()
			}
			p.expect(lexer.TOKEN_RBRACKET)
			expr = &ast.IndexExpr{Position: pos, Object: expr, Index: start, Index2: index2}
			// A trailing `else <value>` is a postfix index fallback. Recovery forms
			// (`else return`, `else raise`, `else void`, `else binding:`) are left
			// for the expression-level recovery handling, so that e.g.
			// `get arr[i] else return null` folds the recovery into the `get`.
			if p.peek() == lexer.TOKEN_ELSE && !p.elseIntroducesRecoveryClause() {
				p.advance()
				expr.(*ast.IndexExpr).Fallback = p.parseOr()
				expr.(*ast.IndexExpr).LegacyElseFallback = true
			}

		case lexer.TOKEN_LPAREN:
			pos := p.cur().Pos
			p.advance()
			var args []ast.Expr
			var argNames []string
			var argShorthand []bool
			var argItems []ast.CallArgItem
			var hasArgForward bool
			var argForwardPos lexer.Pos
			args, argNames, argShorthand, argItems, hasArgForward, argForwardPos = p.parseCallArgs()
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
				ArgItemOrder:  argItems,
				Safe:          safe,
			}

		default:
			return expr
		}
	}
}
