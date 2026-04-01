package parser

import (
	"strings"

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
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "func" {
		return p.parseFuncTypeExpr()
	}
	storage, explicit, label, region, storageParam := p.parseRefStorageQualifier()
	typ := p.parseBaseType(storage, explicit, label, region, storageParam)
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

	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	return &ast.FuncTypeExpr{Position: pos, Params: params, Return: retType, Permissions: permissions, Variadic: variadic}
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

func (p *Parser) parseRefStorageQualifier() (ast.RefStorage, bool, string, string, string) {
	switch p.peek() {
	case lexer.TOKEN_ANY:
		tok := p.advance()
		return ast.RefStorageAny, true, tok.Text, "", ""
	case lexer.TOKEN_HEAP:
		tok := p.advance()
		return ast.RefStorageHeap, true, tok.Text, "", ""
	case lexer.TOKEN_STACK:
		tok := p.advance()
		return ast.RefStorageStack, true, tok.Text, "", ""
	case lexer.TOKEN_STATIC:
		tok := p.advance()
		return ast.RefStorageStatic, true, tok.Text, "", ""
	default:
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text != "can" && p.tokens[p.pos+1].Text != "ensures" {
			name := p.advance().Text
			return ast.RefStorageAny, true, name, "", name
		}
		return ast.RefStorageAny, false, "", "", ""
	}
}

func (p *Parser) peekRefStateParamBracket() (string, bool) {
	if p.peek() != lexer.TOKEN_LBRACKET || p.pos+2 >= len(p.tokens) {
		return "", false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT || p.tokens[p.pos+2].Kind != lexer.TOKEN_RBRACKET {
		return "", false
	}
	return p.tokens[p.pos+1].Text, true
}

func (p *Parser) parseRefStateParamBracket() string {
	p.expect(lexer.TOKEN_LBRACKET)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RBRACKET)
	return name
}

func (p *Parser) parseRefTypeSuffixes(base ast.TypeExpr, pos lexer.Pos, storage ast.RefStorage, explicit bool, region string, storageParam string) (ast.TypeExpr, int) {
	typ := base
	count := 0
	for {
		switch p.peek() {
		case lexer.TOKEN_AMPERSAND:
			p.advance()
			state := ast.RefStateNonNull
			allowStateParam := true
			if p.match(lexer.TOKEN_QUESTION) {
				state = ast.RefStateNullable
				allowStateParam = false
			}
			stateParam := ""
			if allowStateParam {
				if _, ok := p.peekRefStateParamBracket(); ok {
					stateParam = p.parseRefStateParamBracket()
				}
			}
			typ = &ast.RefType{Position: pos, Elem: typ, State: state, Storage: storage, StateParam: stateParam, StorageParam: storageParam, Region: region, Explicit: explicit}
			count++
		case lexer.TOKEN_BANG:
			p.advance()
			typ = &ast.RefType{Position: pos, Elem: typ, State: ast.RefStateNull, Storage: storage, StorageParam: storageParam, Region: region, Explicit: explicit}
			count++
		default:
			return typ, count
		}
	}
}

func (p *Parser) parseGenericTypeArgExpr() ast.TypeExpr {
	switch p.peek() {
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
	case lexer.TOKEN_ANY, lexer.TOKEN_HEAP, lexer.TOKEN_STACK, lexer.TOKEN_STATIC:
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

func (p *Parser) parseBaseType(storage ast.RefStorage, explicit bool, label string, region string, storageParam string) ast.TypeExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.match(lexer.TOKEN_DOT) {
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
			afterBracket := lexer.TOKEN_EOF
			if p.pos+1 < len(p.tokens) {
				afterBracket = p.tokens[p.pos+1].Kind
			}
			isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
			if afterBracket == lexer.TOKEN_IDENT && p.pos+2 < len(p.tokens) {
				afterIdent := p.tokens[p.pos+2].Kind
				isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_RBRACKET && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET && afterIdent != lexer.TOKEN_PIPE
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
	typ, refCount = p.parseRefTypeSuffixes(typ, pos, storage, explicit, region, storageParam)
	if !explicit && refCount > 0 {
		p.errorf("reference types require an explicit storage qualifier like \"any\", \"heap\", \"stack\", or \"static\"")
	}
	if explicit && refCount == 0 {
		if region != "" {
			p.errorf("region qualifier %q requires a pointer type", label)
		} else if storageParam != "" {
			p.errorf("refstorage qualifier %q requires a pointer type", label)
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

func (p *Parser) parseBuiltinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	switch name {
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
	case "str":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "str", ValueArgs: []ast.Expr{size}}
	case "string":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "string", ValueArgs: []ast.Expr{size}}
	case "dict":
		p.advance()
		key := p.parseTypeExpr()
		p.expect(lexer.TOKEN_COMMA)
		value := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{key, value}}
	case "dstr":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "dstr", ValueArgs: []ast.Expr{size}}
	case "dstring":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "dstring", ValueArgs: []ast.Expr{size}}
	case "view", "dview", "packedview":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		if name == "dview" || name == "packedview" {
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
	if p.matchIdentText("can") {
		permissions := p.parsePermissionRefs(false)
		return &ast.CanExpr{Position: expr.Pos(), Expr: expr, Permissions: permissions}
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
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ ||
		p.peek() == lexer.TOKEN_IS {
		pos := p.cur().Pos
		op := p.advance()
		var right ast.Expr
		if op.Kind == lexer.TOKEN_IS {
			right = p.parseIsTestExpr()
		} else {
			right = p.parseBitwiseOr()
		}
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}

func (p *Parser) parseIsTestExpr() ast.Expr {
	if p.peekQualifiedVariantTargetWithPayload() {
		return p.parseVariantIsTestExpr()
	}
	target := p.parseTypeExpr()
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
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_submit1"},
			Args: []ast.Expr{
				poolArg,
				call.Func,
				call.Args[0],
			},
		}
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
	if p.match(lexer.TOKEN_LBRACKET) {
		owner = p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	value := p.parseExpr()
	return &ast.AllocExpr{Position: pos, Owner: owner, Value: value}
}

func (p *Parser) parseMatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	value := p.parseExpr()
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchExpr{Position: pos, Value: value, Store: store, Arms: arms}
}

func (p *Parser) parseCallArgs() ([]ast.Expr, []string) {
	argCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN)
	args := make([]ast.Expr, 0, argCapacity)
	argNames := make([]string, 0, argCapacity)
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil
	}
	for {
		name := ""
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			name = p.advance().Text
			p.expect(lexer.TOKEN_COLON)
		}
		args = append(args, p.parseExpr())
		argNames = append(argNames, name)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	hasNamed := false
	for _, name := range argNames {
		if name != "" {
			hasNamed = true
			break
		}
	}
	if !hasNamed {
		return args, nil
	}
	return args, argNames
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
				legacySyntax := false
				if p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
					p.advance()
					p.advance()
					legacySyntax = true
				}
				expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target, LegacySyntax: legacySyntax}
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
					p.errorf("legacy reference cast syntax is no longer supported; use .cast[any T&] with an explicit target type instead")
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
				expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target, Origin: ast.CastExprOriginPostfixShorthand}
				continue
			}

			expr = &ast.FieldExpr{Position: pos, Object: expr, Field: field}

		case lexer.TOKEN_ARROW:
			pos := p.cur().Pos
			p.advance()
			target := p.parseTypeExpr()
			expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target}

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
			args, argNames := p.parseCallArgs()
			p.expect(lexer.TOKEN_RPAREN)
			expr = &ast.CallExpr{Position: pos, Func: expr, Args: args, ArgNames: argNames}

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
	case lexer.TOKEN_LBRACKET:
		pos := p.cur().Pos
		p.advance()
		elems := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
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
	case lexer.TOKEN_MATCH:
		return p.parseMatchExpr()
	case lexer.TOKEN_IDENT:
		tok := p.advance()
		if len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' && p.peekStructLiteralTypeArgCall() {
			typeArgs := p.parseStructLiteralTypeArgs()
			p.expect(lexer.TOKEN_LPAREN)
			args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseExpr())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args}
		}
		if p.peek() == lexer.TOKEN_LPAREN && len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' {
			p.advance()
			args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
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

func (p *Parser) peekStructLiteralTypeArgCall() bool {
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
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_LPAREN
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

func (p *Parser) expectNewline() {
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	} else if p.peek() == lexer.TOKEN_EOF || p.peek() == lexer.TOKEN_DEDENT {
		// OK at end of file or block
	} else {
		p.errorf("expected newline, got %s", p.cur())
	}
}
