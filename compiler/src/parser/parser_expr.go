package parser

import (
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func isPostfixShorthandCastTarget(name string) bool {
	switch name {
	case "void", "bool", "char", "int",
		"i8", "i16", "i32", "i64", "isize",
		"u8", "u16", "u32", "u64", "usize", "uintptr",
		"f32", "f64":
		return true
	}
	if name == "" {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

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
	var typ ast.TypeExpr
	if p.peek() == lexer.TOKEN_LPAREN {
		typ = p.parseTupleTypeExpr()
	} else {
		storage, explicit, label, region, storageParam := p.parseRefStorageQualifier()
		typ = p.parseBaseType(storage, explicit, label, region, storageParam)
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

	effectAliasPos := lexer.Pos{}
	effectAlias := ""
	if p.matchIdentText("effects") {
		effectAliasPos = p.tokens[p.pos-1].Pos
		effectAlias = p.parseQualifiedDeclName()
	}

	var permissions []ast.PermissionRef
	if p.matchIdentText("can") {
		permissions = p.parsePermissionRefs(true)
	}

	if effectAlias != "" {
		if signatureHasExplicitErrorEffects(retType) {
			p.errorf("effects alias cannot be combined with an explicit error[...] clause")
		}
		if len(permissions) != 0 {
			p.errorf("effects alias cannot be combined with an explicit can[...] clause")
		}
	}

	return &ast.FuncTypeExpr{Position: pos, Params: params, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, Return: retType, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Permissions: permissions, Variadic: variadic}
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
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text != "can" && p.tokens[p.pos+1].Text != "effects" && p.tokens[p.pos+1].Text != "ensures" {
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
			hasTopLevelComma := p.peekTopLevelCommaInCurrentBracketList()
			afterBracket := lexer.TOKEN_EOF
			if p.pos+1 < len(p.tokens) {
				afterBracket = p.tokens[p.pos+1].Kind
			}
			isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
			if !hasTopLevelComma && afterBracket == lexer.TOKEN_IDENT && p.pos+2 < len(p.tokens) {
				afterIdent := p.tokens[p.pos+2].Kind
				isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_RBRACKET && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET && afterIdent != lexer.TOKEN_PIPE && afterIdent != lexer.TOKEN_DOT
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
	typ, refCount = p.parseRefTypeSuffixes(typ, pos, storage, explicit, region, storageParam)
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
	case "view", "dview", "packedview", "treeview":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		if name == "dview" || name == "packedview" || name == "treeview" {
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

func (p *Parser) parseChildrenCallArgs() ([]ast.Expr, []string) {
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil
	}
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	for {
		arg := p.parseExpr()
		if p.match(lexer.TOKEN_TO) {
			target := p.parseTypeExpr()
			arg = &ast.CastExpr{Position: arg.Pos(), Operand: arg, Target: target, Origin: ast.CastExprOriginToSyntax}
		}
		args = append(args, arg)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return args, nil
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
		p.peek() == lexer.TOKEN_IS || p.membershipLiteralAhead() {
		pos := p.cur().Pos
		op := p.advance()
		var right ast.Expr
		if op.Kind == lexer.TOKEN_IS {
			right = p.parseIsTestExpr()
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

func (p *Parser) parseAs() ast.Expr {
	left := p.parseBitwiseOr()
	for p.allowAsCast && p.peek() == lexer.TOKEN_AS {
		if p.pos+1 >= len(p.tokens) || !tokenCanStartTypeExpr(p.tokens[p.pos+1]) {
			return left
		}
		pos := p.cur().Pos
		p.advance()
		target := p.parseTypeExpr()
		left = &ast.CastExpr{Position: pos, Operand: left, Target: target, Origin: ast.CastExprOriginAsSyntax}
	}
	return left
}

func (p *Parser) parseIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	targets := make([]ast.Expr, 0, 1)
	targets = append(targets, p.parseSingleIsTestTargetExpr())
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		targets = append(targets, p.parseSingleIsTestTargetExpr())
	}
	if len(targets) == 1 {
		return targets[0]
	}
	return &ast.IsPatternExpr{Position: pos, Targets: targets}
}

func (p *Parser) parseSingleIsTestTargetExpr() ast.Expr {
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
	value := p.withInMembershipDisabled(p.parseExpr)
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchExpr{Position: pos, Value: value, Store: store, Arms: arms}
}

func (p *Parser) parseCatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CATCH)
	value := p.withInMembershipDisabled(p.parseExpr)
	success, arms := p.parseCatchArms()
	return &ast.CatchExpr{Position: pos, Value: value, Success: success, Arms: arms}
}

func (p *Parser) parseCatchArms() (ast.CatchArm, []ast.CatchArm) {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	p.skipNewlines()
	if p.peek() == lexer.TOKEN_DEDENT || p.peek() == lexer.TOKEN_EOF {
		p.errorf("catch expression requires a `value:` success arm")
		p.expect(lexer.TOKEN_DEDENT)
		return ast.CatchArm{}, nil
	}
	success := p.parseCatchArm()
	if success.Name != "value" {
		p.errorf("catch expression must start with a `value:` success arm")
	}
	var arms []ast.CatchArm
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseCatchArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return success, arms
}

func (p *Parser) parseCatchArm() ast.CatchArm {
	name, pos := p.parseQualifiedTargetName()
	p.expect(lexer.TOKEN_COLON)
	body := p.parseCatchArmBody(pos)
	return ast.CatchArm{Position: pos, Name: name, Body: body}
}

func (p *Parser) parseCatchArmBody(pos lexer.Pos) []ast.Stmt {
	if p.match(lexer.TOKEN_NEWLINE) {
		return p.parseBlock()
	}
	value := p.parseExpr()
	return []ast.Stmt{&ast.ExprStmt{Position: pos, Expr: value}}
}

func (p *Parser) parseVisitExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("visit")
	value := p.withAsCastDisabled(p.parseExpr)
	var root ast.TypeExpr
	if p.match(lexer.TOKEN_AS) {
		root = p.parseTypeExpr()
	}
	arms := p.parseVisitArms()
	return &ast.VisitExpr{Position: pos, Value: value, Root: root, Arms: arms}
}

func (p *Parser) parseFoldExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("fold")
	value := p.withAsCastDisabled(p.parseExpr)
	p.expect(lexer.TOKEN_AS)
	root := p.parseTypeExpr()
	p.expectIdentText("into")
	resultType := p.parseTypeExpr()
	arms := p.parseVisitArms()
	return &ast.FoldExpr{Position: pos, Value: value, Root: root, ResultType: resultType, Arms: arms}
}

func (p *Parser) parseRewriteExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("rewrite")
	value := p.withAsCastDisabled(p.parseExpr)
	p.expect(lexer.TOKEN_AS)
	root := p.parseTypeExpr()
	arms := p.parseVisitArms()
	return &ast.FoldExpr{Position: pos, Keyword: "rewrite", Value: value, Root: root, ResultType: root, Arms: arms}
}

func (p *Parser) parseLambdaExpr() ast.Expr {
	pos := p.cur().Pos
	keyword := p.expect(lexer.TOKEN_IDENT).Text
	params, shorthand := p.parseLambdaParams()
	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	if p.match(lexer.TOKEN_NEWLINE) {
		body := p.parseBlock()
		return &ast.LambdaExpr{
			Position:            pos,
			Keyword:             keyword,
			UsesShorthandParams: shorthand,
			Params:              params,
			ReturnType:          retType,
			Body:                body,
		}
	}
	bodyExpr := p.parseExpr()
	return &ast.LambdaExpr{
		Position:            pos,
		Keyword:             keyword,
		UsesShorthandParams: shorthand,
		Params:              params,
		ReturnType:          retType,
		BodyExpr:            bodyExpr,
	}
}

func (p *Parser) parseLambdaParams() ([]ast.ParamDecl, bool) {
	if p.match(lexer.TOKEN_LPAREN) {
		params := make([]ast.ParamDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				params = append(params, p.parseParam(false))
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
		return params, false
	}
	params := []ast.ParamDecl{{Position: p.cur().Pos, Name: p.expect(lexer.TOKEN_IDENT).Text}}
	for p.match(lexer.TOKEN_COMMA) {
		params = append(params, ast.ParamDecl{Position: p.cur().Pos, Name: p.expect(lexer.TOKEN_IDENT).Text})
	}
	return params, true
}

func (p *Parser) parseQualifiedTargetName() (string, lexer.Pos) {
	pos := p.cur().Pos
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	return strings.Join(parts, "."), pos
}

func (p *Parser) parseVisitArms() []ast.VisitArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.VisitArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseVisitArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}

func (p *Parser) parseVisitArm() []ast.VisitArm {
	arms := []ast.VisitArm{p.parseVisitArmHead()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		arms = append(arms, p.parseVisitArmHead())
	}
	var guard ast.Expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "when" {
		p.advance()
		guard = p.parseExpr()
	}
	for i := range arms {
		arms[i].Guard = guard
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	for i := range arms {
		arms[i].Body = body
	}
	return arms
}

func (p *Parser) parseVisitArmHead() ast.VisitArm {
	pos := p.cur().Pos
	arm := ast.VisitArm{Position: pos}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		arm.Wildcard = true
	} else {
		arm.TargetName, _ = p.parseQualifiedTargetName()
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				arm.BindName = p.expect(lexer.TOKEN_IDENT).Text
				if p.match(lexer.TOKEN_COMMA) {
					arm = p.parseVisitArmChildBindings(arm)
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
	}
	return arm
}

func (p *Parser) parseVisitArmChildBindings(arm ast.VisitArm) ast.VisitArm {
	if p.peek() != lexer.TOKEN_IDENT {
		p.errorf("expected fold child result binding name, got %s", p.cur())
		return arm
	}
	firstTok := p.expect(lexer.TOKEN_IDENT)
	if p.peek() != lexer.TOKEN_COLON && p.peek() == lexer.TOKEN_RPAREN {
		arm.ChildResultsName = firstTok.Text
		return arm
	}
	arm.ChildBindings = append(arm.ChildBindings, p.finishVisitArmChildBinding(firstTok))
	for p.match(lexer.TOKEN_COMMA) {
		nameTok := p.expect(lexer.TOKEN_IDENT)
		arm.ChildBindings = append(arm.ChildBindings, p.finishVisitArmChildBinding(nameTok))
	}
	return arm
}

func (p *Parser) finishVisitArmChildBinding(nameTok lexer.Token) ast.VisitArmChildBinding {
	binding := ast.VisitArmChildBinding{Position: nameTok.Pos, FieldName: nameTok.Text, BindName: nameTok.Text}
	if p.match(lexer.TOKEN_COLON) {
		binding.BindName = p.expect(lexer.TOKEN_IDENT).Text
	}
	return binding
}

func (p *Parser) parseNamedArgList(endToken lexer.TokenKind, allowSpread bool) ([]ast.WithArg, bool) {
	args := make([]ast.WithArg, 0, p.estimateCommaSeparatedCount(endToken))
	spread := false
	if p.peek() == endToken {
		return nil, false
	}
	for {
		if allowSpread && p.match(lexer.TOKEN_RANGE) {
			if spread {
				p.errorf("with bundle spread `..` can only appear once")
			}
			spread = true
		} else {
			pos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			arg := ast.WithArg{Position: pos, Name: name}
			switch {
			case p.match(lexer.TOKEN_ASSIGN):
				arg.Value = p.parseExpr()
			case p.match(lexer.TOKEN_COLON):
				if p.peek() == endToken || p.peek() == lexer.TOKEN_COMMA {
					arg.Value = &ast.Ident{Position: pos, Name: name}
					arg.Shorthand = true
				} else {
					arg.Value = p.parseExpr()
				}
			default:
				p.errorf("expected `=` or `:` after named argument %q", name)
				arg.Value = &ast.Ident{Position: pos, Name: name}
				arg.Shorthand = true
			}
			args = append(args, arg)
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == endToken {
			break
		}
	}
	return args, spread
}

func (p *Parser) parseValueParamPackUse() ast.ParamPackUse {
	pos := p.tokens[p.pos-1].Pos
	pack := ast.ParamPackUse{Position: pos, Name: p.parseQualifiedDeclName()}
	if p.peek() != lexer.TOKEN_LPAREN {
		pack.Bare = true
		return pack
	}
	p.expect(lexer.TOKEN_LPAREN)
	pack.Args, _ = p.parseNamedArgList(lexer.TOKEN_RPAREN, false)
	p.expect(lexer.TOKEN_RPAREN)
	return pack
}

func (p *Parser) parseCallArgs() ([]ast.Expr, []string, []bool, []ast.ParamPackUse, []ast.CallArgItem, bool, lexer.Pos) {
	argCapacity := p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN)
	args := make([]ast.Expr, 0, argCapacity)
	argNames := make([]string, 0, argCapacity)
	argShorthand := make([]bool, 0, argCapacity)
	packs := make([]ast.ParamPackUse, 0, 1)
	items := make([]ast.CallArgItem, 0, argCapacity)
	hasArgForward := false
	argForwardPos := lexer.Pos{}
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil, nil, nil, nil, false, lexer.Pos{}
	}
	sawPack := false
	for {
		if p.peek() == lexer.TOKEN_RANGE {
			pos := p.cur().Pos
			p.advance()
			if hasArgForward {
				p.errorf("call argument forwarding `..` can only appear once")
			}
			if len(args) != 0 {
				p.errorf("call argument forwarding `..` must appear before other call arguments")
			}
			hasArgForward, argForwardPos = true, pos
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RPAREN {
				break
			}
			continue
		}
		if p.matchIdentText("use") {
			if sawPack {
				p.errorf("call parameter-pack application may appear at most once")
			}
			if len(argNames) != 0 {
				for _, name := range argNames {
					if name != "" {
						p.errorf("call parameter-pack application must appear before ordinary named arguments")
						break
					}
				}
			}
			pack := p.parseValueParamPackUse()
			packs = append(packs, pack)
			items = append(items, ast.CallArgItem{Position: pack.Position, Pack: pack, IsPack: true})
			sawPack = true
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RPAREN {
				break
			}
			continue
		}
		name := ""
		namePos := lexer.Pos{}
		shorthand := false
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON && !p.peekImmediateGroupedBlockExprStart() {
			tok := p.advance()
			name = tok.Text
			namePos = tok.Pos
			p.expect(lexer.TOKEN_COLON)
			if p.peek() == lexer.TOKEN_COMMA || p.peek() == lexer.TOKEN_RPAREN {
				shorthand = true
			}
		}
		var arg ast.Expr
		if shorthand {
			arg = &ast.Ident{Position: namePos, Name: name}
		} else {
			arg = p.parseExpr()
		}
		if name == "" && sawPack {
			p.errorf("call parameter-pack application must come after all positional arguments")
		}
		args = append(args, arg)
		argNames = append(argNames, name)
		argShorthand = append(argShorthand, shorthand)
		items = append(items, ast.CallArgItem{Position: arg.Pos(), ArgIndex: len(args) - 1})
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
	if hasArgForward {
		for _, name := range argNames {
			if name == "" {
				p.errorf("call argument forwarding `..` only supports named arguments")
				break
			}
		}
	}
	if !hasNamed && len(packs) == 0 {
		return args, nil, nil, nil, nil, hasArgForward, argForwardPos
	}
	return args, argNames, argShorthand, packs, items, hasArgForward, argForwardPos
}

func (p *Parser) peekImmediateGroupedBlockExprStart() bool {
	return p.peek() == lexer.TOKEN_IDENT &&
		p.isImmediateGroupedBlockExprName(p.cur().Text) &&
		p.pos+2 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON &&
		p.tokens[p.pos+2].Kind == lexer.TOKEN_NEWLINE
}

func (p *Parser) isImmediateGroupedBlockExprName(name string) bool {
	switch name {
	case "do":
		return true
	default:
		return false
	}
}

func (p *Parser) parseWithBundleNamedArgs() ([]ast.WithArg, bool) {
	return p.parseNamedArgList(lexer.TOKEN_RPAREN, true)
}

func (p *Parser) parseWithValueClause() ([]ast.WithArg, []ast.WithBundleUse, []ast.WithItem) {
	p.expect(lexer.TOKEN_WITH)
	args := make([]ast.WithArg, 0, 2)
	bundles := make([]ast.WithBundleUse, 0, 1)
	items := make([]ast.WithItem, 0, 2)
	for {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		qualifiedName := name
		hasDot := false
		for p.match(lexer.TOKEN_DOT) {
			hasDot = true
			qualifiedName += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
		switch {
		case p.peek() == lexer.TOKEN_LPAREN:
			p.advance()
			bundleArgs, spread := p.parseWithBundleNamedArgs()
			p.expect(lexer.TOKEN_RPAREN)
			bundle := ast.WithBundleUse{Position: pos, Name: qualifiedName, Args: bundleArgs, Spread: spread}
			bundles = append(bundles, bundle)
			items = append(items, ast.WithItem{Position: pos, Bundle: bundle, IsBundle: true})
		case !hasDot && p.match(lexer.TOKEN_ASSIGN):
			arg := ast.WithArg{Position: pos, Name: name, Value: p.parseExpr()}
			args = append(args, arg)
			items = append(items, ast.WithItem{Position: pos, Arg: arg})
		case !hasDot:
			arg := ast.WithArg{Position: pos, Name: name, Value: &ast.Ident{Position: pos, Name: name}, Shorthand: true}
			args = append(args, arg)
			items = append(items, ast.WithItem{Position: pos, Arg: arg})
		default:
			p.errorf("context bundle use %q requires (...) in a with clause", qualifiedName)
			bundle := ast.WithBundleUse{Position: pos, Name: qualifiedName}
			bundles = append(bundles, bundle)
			items = append(items, ast.WithItem{Position: pos, Bundle: bundle, IsBundle: true})
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return args, bundles, items
}

func (p *Parser) attachOptionalCallWithClause(expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || p.peek() != lexer.TOKEN_WITH {
		return expr
	}
	call.WithArgs, call.WithBundles, call.WithItemOrder = p.parseWithValueClause()
	return call
}

func (p *Parser) peekPostfixGenericApplication() bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	if probe.peek() == lexer.TOKEN_RBRACKET {
		return false
	}
	for {
		_ = probe.parseGenericTypeArgExpr()
		if !probe.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	if probe.peek() != lexer.TOKEN_RBRACKET {
		return false
	}
	probe.advance()
	return probe.peek() == lexer.TOKEN_LPAREN
}

func (p *Parser) looksLikeCascadeExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "cascade" {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	_ = probe.parseExpr()
	return probe.peek() == lexer.TOKEN_FATARROW
}

func (p *Parser) looksLikeLambdaExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || (p.cur().Text != "lambda" && p.cur().Text != "λ") {
		return false
	}
	probe := *p
	probe.errors = nil
	probe.advance()
	if !probe.parseLambdaSignatureProbe() {
		return false
	}
	return probe.peek() == lexer.TOKEN_COLON
}

func (p *Parser) parseLambdaSignatureProbe() bool {
	if p.peek() == lexer.TOKEN_LPAREN {
		p.advance()
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				p.match(lexer.TOKEN_MUTABLE)
				if p.peek() != lexer.TOKEN_IDENT {
					return false
				}
				p.advance()
				if !p.match(lexer.TOKEN_COLON) {
					return false
				}
				_ = p.parseTypeExpr()
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		if !p.match(lexer.TOKEN_RPAREN) {
			return false
		}
	} else if p.peek() == lexer.TOKEN_IDENT {
		p.advance()
		for p.match(lexer.TOKEN_COMMA) {
			if p.peek() != lexer.TOKEN_IDENT {
				return false
			}
			p.advance()
		}
	} else {
		return false
	}
	if p.match(lexer.TOKEN_ARROW) {
		_ = p.parseTypeExpr()
	}
	return true
}

func (p *Parser) parsePostfix() ast.Expr {
	expr := p.parsePrimary()
	for {
		switch p.peek() {
		case lexer.TOKEN_QUESTION:
			if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_DOT {
				return expr
			}
			pos := p.cur().Pos
			p.advance()
			p.expect(lexer.TOKEN_DOT)
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
				legacySyntax := false
				if p.peek() == lexer.TOKEN_LPAREN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
					p.advance()
					p.advance()
					legacySyntax = true
				}
				expr = &ast.CastExpr{Position: pos, Operand: expr, Target: target, LegacySyntax: legacySyntax}
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
					p.errorf("legacy reference cast syntax is no longer supported; use .cast[any T&] with an explicit target type instead")
					p.advance()
					p.expect(lexer.TOKEN_RPAREN)
					expr = &ast.CastExpr{Position: castPos, Operand: expr, Target: target}
					continue
				}
				p.pos = savedCastPos
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
		if p.match(lexer.TOKEN_QUESTION) {
			value := p.parseOr()
			p.expectIdentText("default")
			fallback := p.parseExpr()
			return &ast.TryExpr{
				Position:                 pos,
				Value:                    value,
				Fallback:                 fallback,
				UsesDefaultShorthandForm: true,
			}
		}
		value := p.parseOr()
		return &ast.TryExpr{Position: pos, Value: value}
	case lexer.TOKEN_CATCH:
		return p.parseCatchExpr()
	case lexer.TOKEN_RAISE:
		pos := p.cur().Pos
		p.advance()
		errExpr := p.parseOr()
		return &ast.RaiseExpr{Position: pos, Error: errExpr}
	case lexer.TOKEN_MATCH:
		return p.parseMatchExpr()
	case lexer.TOKEN_IDENT:
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
		if len(tok.Text) > 0 && tok.Text[0] >= 'A' && tok.Text[0] <= 'Z' && p.peekStructLiteralTypeArgsFollowedBy(lexer.TOKEN_LBRACE) {
			typeArgs := p.parseStructLiteralTypeArgs()
			p.expect(lexer.TOKEN_LBRACE)
			args, argNames := p.parseStructLiteralBraceFields()
			p.expect(lexer.TOKEN_RBRACE)
			return &ast.StructLitExpr{Position: tok.Pos, Name: tok.Text, TypeArgs: typeArgs, Args: args, ArgNames: argNames, Brace: true}
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
		inner := p.parseExpr()
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
	exprStmt, ok := body[len(body)-1].(*ast.ExprStmt)
	if !ok || exprStmt == nil || exprStmt.Expr == nil {
		p.errorf("expression block requires a final expression statement in the block")
		return &ast.NullLit{Position: pos}
	}
	if flattenSingle && len(body) == 1 {
		return exprStmt.Expr
	}
	stmts := append([]ast.Stmt(nil), body[:len(body)-1]...)
	return &ast.ExprBlock{Position: pos, Stmts: stmts, Value: exprStmt.Expr}
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
	p.expectNewline()
}
