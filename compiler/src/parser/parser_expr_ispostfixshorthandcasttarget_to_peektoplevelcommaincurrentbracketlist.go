package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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
func (p *Parser) parseListComprehensionFromFirst(pos lexer.Pos, value ast.Expr) ast.Expr {
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	var rangeEnd ast.Expr
	var rangeStep ast.Expr
	rangeOp := lexer.TOKEN_EOF
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		op := p.advance()
		rangeOp = op.Kind
		rangeEnd = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		if p.match(lexer.TOKEN_RANGE) {
			rangeStep = p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RBRACKET)
	var owner ast.Expr
	if p.match(lexer.TOKEN_IN) {
		owner = p.withInMembershipDisabled(p.parseExpr)
	}
	return &ast.ListComprehensionExpr{Position: pos, Value: value, Name: name, Source: source, RangeEnd: rangeEnd, RangeStep: rangeStep, RangeOp: rangeOp, Filter: filter, Owner: owner}
}
func (p *Parser) parseQueryExpr() ast.Expr {
	pos := p.cur().Pos
	kindText := p.expect(lexer.TOKEN_IDENT).Text
	kind := ast.QueryExprAny
	switch kindText {
	case "any":
		kind = ast.QueryExprAny
	case "all":
		kind = ast.QueryExprAll
	case "first":
		kind = ast.QueryExprFirst
	case "count":
		kind = ast.QueryExprCount
	default:
		p.errorAt(pos, "unknown query expression %q", kindText)
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	p.expectIdentText("where")
	filter := p.parseExpr()
	return &ast.QueryExpr{Position: pos, Kind: kind, Name: name, Source: source, Filter: filter}
}
func (p *Parser) looksLikeQueryExpr() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+3 >= len(p.tokens) {
		return false
	}
	switch p.cur().Text {
	case "any", "all", "first", "count":
	default:
		return false
	}
	return p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_IN
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
	var effects []ast.SignatureEffectItem
	if p.matchIdentText("effects") {
		effectAliasPos = p.tokens[p.pos-1].Pos
		if p.peek() == lexer.TOKEN_LBRACKET {
			effects = p.parseSignatureEffectsClause()
		} else {
			effectAlias = p.parseQualifiedDeclName()
		}
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

	return &ast.FuncTypeExpr{Position: pos, Params: params, ImplicitParams: implicitParams, ImplicitBundles: implicitBundles, ImplicitItemOrder: implicitItemOrder, Return: retType, EffectAliasPos: effectAliasPos, EffectAlias: effectAlias, Effects: effects, Permissions: permissions, Variadic: variadic}
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
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "any" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text != "can" && p.tokens[p.pos+1].Text != "effects" && p.tokens[p.pos+1].Text != "ensures" {
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
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT:
		pos := p.cur().Pos
		return &ast.GenericValueArgTypeExpr{Position: pos, Value: p.parseExpr()}
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
	case lexer.TOKEN_HEAP, lexer.TOKEN_STACK, lexer.TOKEN_STATIC:
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
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET && afterIdent != lexer.TOKEN_PIPE && afterIdent != lexer.TOKEN_DOT
				if afterIdent == lexer.TOKEN_RBRACKET {
					isArray = len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z'
				}
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
