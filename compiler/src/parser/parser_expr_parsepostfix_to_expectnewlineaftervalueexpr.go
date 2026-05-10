package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
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
					Position: pos,
					Operand:  &ast.AddrOfExpr{Position: pos, Operand: expr},
					Target:   target,
				}
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
				expr = &ast.SpecializeExpr{Position: pos, Operand: expr, TypeArgs: typeArgs}
				continue
			}

			if p.peek() == lexer.TOKEN_AMPERSAND || p.peek() == lexer.TOKEN_BANG {
				castPos := pos
				savedCastPos := p.pos
				var target ast.TypeExpr = &ast.NamedType{Position: castPos, Name: field}
				target, _ = p.parseRefTypeSuffixes(target, castPos, ast.RefStorageAny, false, "", "")
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
			p.errorAt(pos, "legacy expression arrow cast `expr -> T` is deprecated; use `expr as T`, `expr.T()`, or `expr.cast[T]` instead")
			expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target}

		case lexer.TOKEN_LBRACE:
			pos := p.cur().Pos
			p.advance()
			args, argNames := p.parseRecordUpdateFields()
			p.expect(lexer.TOKEN_RBRACE)
			expr = &ast.RecordUpdateExpr{Position: pos, Base: expr, Args: args, ArgNames: argNames}

		case lexer.TOKEN_LBRACKET:
			pos := p.cur().Pos
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
			if p.peek() == lexer.TOKEN_ELSE {
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

		case lexer.TOKEN_IF:
			if !p.allowTernary {
				return expr
			}
			pos := p.cur().Pos
			p.advance()
			cond := p.parseOr()
			p.expect(lexer.TOKEN_ELSE)
			alt := p.parseExpr()
			expr = &ast.TernaryExpr{Position: pos, Value: expr, Cond: cond, Alt: alt}

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
		if p.peek() != lexer.TOKEN_RBRACKET {
			first := p.parseExpr()
			if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
				return p.parseListComprehensionFromFirst(pos, first)
			}
			elems = append(elems, first)
			for p.match(lexer.TOKEN_COMMA) {
				if p.peek() == lexer.TOKEN_RBRACKET {
					break
				}
				elems = append(elems, p.parseExpr())
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.ListLitExpr{Position: pos, Elems: elems}
	case lexer.TOKEN_SIZEOF:
		pos := p.cur().Pos
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.SizeofExpr{Position: pos, Type: typ}
	case lexer.TOKEN_ALIGNOF:
		pos := p.cur().Pos
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.AlignofExpr{Position: pos, Type: typ}
	case lexer.TOKEN_OFFSETOF:
		pos := p.cur().Pos
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_COMMA)
		field := p.expect(lexer.TOKEN_IDENT)
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.OffsetofExpr{Position: pos, Type: typ, Field: field.Text}
	case lexer.TOKEN_TRY:
		pos := p.cur().Pos
		p.advance()
		if p.match(lexer.TOKEN_QUESTION) {
			value := p.parseOr()
			p.expectIdentText("default")
			fallback := p.parseExpr()
			return &ast.TryExpr{
				Position:                 pos,
				Value:                    value,
				Fallback:                 fallback,
				Recovery:                 &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: fallback},
				UsesDefaultShorthandForm: true,
			}
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
		tok := p.advance()
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
			args, argNames := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true}
		}
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames := p.parseStructLiteralParenArgs()
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames}
		}
		if p.peek() == lexer.TOKEN_LBRACE && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args, argNames := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args, ArgNames: argNames, Brace: true}
		}
		return &ast.Ident{Position: tok.Pos, Name: tok.Text}
	case lexer.TOKEN_LPAREN:
		pos := p.cur().Pos
		p.advance()
		inner := p.withInMembershipEnabled(p.parseExpr)
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
func (p *Parser) parseStructLiteralBraceFields() ([]ast.Expr, []string) {
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	argNames := make([]string, 0, cap(args))
	if p.peek() == lexer.TOKEN_RBRACE {
		return args, argNames
	}
	for {
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
	return args, argNames
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
	if p.peek() == lexer.TOKEN_NEWLINE {
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
