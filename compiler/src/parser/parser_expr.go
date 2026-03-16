package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

// ---------- Type expressions ----------

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	if p.match(lexer.TOKEN_MUTABLE) {
		elem := p.parseTypeExpr()
		return &ast.MutableType{Position: elem.Pos(), Elem: elem}
	}
	if p.match(lexer.TOKEN_TAIL) {
		elem := p.parseTypeExpr()
		return &ast.TailType{Position: elem.Pos(), Elem: elem}
	}
	storage, explicit, label := p.parseRefStorageQualifier()
	typ := p.parseBaseType(storage, explicit, label)
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
		tags = append(tags, p.parseErrorSetItem())
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

func (p *Parser) parseRefStorageQualifier() (ast.RefStorage, bool, string) {
	switch p.peek() {
	case lexer.TOKEN_ANY:
		tok := p.advance()
		return ast.RefStorageAny, true, tok.Text
	case lexer.TOKEN_HEAP:
		tok := p.advance()
		return ast.RefStorageHeap, true, tok.Text
	case lexer.TOKEN_STACK:
		tok := p.advance()
		return ast.RefStorageStack, true, tok.Text
	case lexer.TOKEN_STATIC:
		tok := p.advance()
		return ast.RefStorageStatic, true, tok.Text
	default:
		return ast.RefStorageAny, false, ""
	}
}

func (p *Parser) parseRefTypeSuffixes(base ast.TypeExpr, pos lexer.Pos, storage ast.RefStorage, explicit bool) (ast.TypeExpr, int) {
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
			typ = &ast.RefType{Position: pos, Elem: typ, State: state, Storage: storage, Explicit: explicit}
			count++
		case lexer.TOKEN_BANG:
			p.advance()
			typ = &ast.RefType{Position: pos, Elem: typ, State: ast.RefStateNull, Storage: storage, Explicit: explicit}
			count++
		default:
			return typ, count
		}
	}
}

func (p *Parser) parseBaseType(storage ast.RefStorage, explicit bool, label string) ast.TypeExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var typ ast.TypeExpr = &ast.NamedType{Position: pos, Name: name}

	if p.peek() == lexer.TOKEN_LBRACKET {
		if builtin := p.parseBuiltinTypeExpr(pos, name); builtin != nil {
			typ = builtin
		} else {
			afterBracket := lexer.TOKEN_EOF
			if p.pos+1 < len(p.tokens) {
				afterBracket = p.tokens[p.pos+1].Kind
			}
			isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
			if afterBracket == lexer.TOKEN_IDENT && p.pos+2 < len(p.tokens) {
				afterIdent := p.tokens[p.pos+2].Kind
				isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_RBRACKET && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET
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
					args = append(args, p.parseTypeExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
				p.expect(lexer.TOKEN_RBRACKET)
				typ = &ast.GenericType{Position: pos, Name: name, Args: args}
			}
		}
	}

	refCount := 0
	typ, refCount = p.parseRefTypeSuffixes(typ, pos, storage, explicit)
	if !explicit && refCount > 0 {
		p.errorf("reference types require an explicit storage qualifier like \"any\", \"heap\", \"stack\", or \"static\"")
	}
	if explicit && refCount == 0 {
		p.errorf("storage qualifier %q requires a pointer type", label)
	}

	if p.peek() == lexer.TOKEN_LBRACKET {
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		typ = &ast.ArrayType{Position: pos, Elem: typ, Size: size}
	}

	return typ
}

func (p *Parser) parseBuiltinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	switch name {
	case "array", "darray":
		p.advance()
		elem := p.parseTypeExpr()
		p.expect(lexer.TOKEN_COMMA)
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}, ValueArgs: []ast.Expr{size}}
	case "str", "string":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "str", ValueArgs: []ast.Expr{size}}
	case "dstr", "dstring":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "dstr", ValueArgs: []ast.Expr{size}}
	case "view":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
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

// ---------- Expression parsing (precedence climbing) ----------

func (p *Parser) parseExpr() ast.Expr {
	expr := p.parseOr()

	if p.peek() == lexer.TOKEN_IF {
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
		alt := p.parseExpr()
		if tryExpr, ok := expr.(*ast.TryExpr); ok && tryExpr.Fallback == nil {
			tryExpr.Fallback = alt
			return tryExpr
		}
		return &ast.UnwrapElseExpr{Position: pos, Value: expr, Fallback: alt}
	}

	return expr
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
	return p.parseComparison()
}

func (p *Parser) parseComparison() ast.Expr {
	left := p.parseBitwiseOr()
	for p.peek() == lexer.TOKEN_EQEQ || p.peek() == lexer.TOKEN_BANGEQ ||
		p.peek() == lexer.TOKEN_LT || p.peek() == lexer.TOKEN_GT ||
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseBitwiseOr()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
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
	if p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.AddrOfExpr{Position: pos, Operand: operand}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		switch p.peek() {
		case lexer.TOKEN_DOT:
			pos := p.cur().Pos
			p.advance()
			field := p.expect(lexer.TOKEN_IDENT).Text

			if field == "cast" && p.peek() == lexer.TOKEN_LBRACKET {
				p.advance()
				target := p.parseTypeExpr()
				p.expect(lexer.TOKEN_RBRACKET)
				p.expect(lexer.TOKEN_LPAREN)
				p.expect(lexer.TOKEN_RPAREN)
				expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target}
				continue
			}

			if p.peek() == lexer.TOKEN_AMPERSAND || p.peek() == lexer.TOKEN_BANG {
				castPos := pos
				savedCastPos := p.pos
				var target ast.TypeExpr = &ast.NamedType{Position: castPos, Name: field}
				target, _ = p.parseRefTypeSuffixes(target, castPos, ast.RefStorageAny, false)
				if p.peek() == lexer.TOKEN_LPAREN {
					p.errorf("legacy reference cast syntax is no longer supported; use .cast[any T&]() with an explicit target type instead")
					p.advance()
					p.expect(lexer.TOKEN_RPAREN)
					expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
					continue
				}
				p.pos = savedCastPos
			}

			if p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
				castPos := pos
				p.advance()
				p.advance()
				target := &ast.NamedType{Position: castPos, Name: field}
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
				continue
			}

			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}

		case lexer.TOKEN_LBRACKET:
			pos := p.cur().Pos
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

		case lexer.TOKEN_LPAREN:
			pos := p.cur().Pos
			p.advance()
			var args []ast.Expr
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
			expr = &ast.CallExpr{Position: pos, Func: expr, Args: args}

		case lexer.TOKEN_IF:
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
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: true}
	case lexer.TOKEN_STRING_LIT:
		tok := p.advance()
		return &ast.StringLit{Position: tok.Pos, Value: tok.Text}
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
	case lexer.TOKEN_LBRACKET:
		pos := p.cur().Pos
		p.advance()
		var elems []ast.Expr
		if p.peek() != lexer.TOKEN_RBRACKET {
			for {
				elems = append(elems, p.parseExpr())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
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
	case lexer.TOKEN_TRY:
		pos := p.cur().Pos
		p.advance()
		value := p.parseOr()
		return &ast.TryExpr{Position: pos, Value: value}
	case lexer.TOKEN_RAISE:
		pos := p.cur().Pos
		p.advance()
		errExpr := p.parseOr()
		return &ast.RaiseExpr{Position: pos, Error: errExpr}
	case lexer.TOKEN_IDENT:
		tok := p.advance()
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			var args []ast.Expr
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, Args: args}
		}
		return &ast.Ident{Position: tok.Pos, Name: tok.Text}
	case lexer.TOKEN_LPAREN:
		p.advance()
		inner := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: inner.Pos(), Inner: inner}
	default:
		p.errorf("unexpected token %s in expression", p.cur())
		tok := p.advance()
		return &ast.Ident{Position: tok.Pos, Name: "<error>"}
	}
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
