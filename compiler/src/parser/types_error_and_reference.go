package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseFuncTypeExpr() ast.TypeExpr {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "func" {
		// `func` as the function-type keyword has been renamed to `fn` (which doubles
		// as the lambda head — one keyword for "function" in type and value position,
		// like Rust). Directed error; recover by parsing the type as usual.
		p.errorAt(pos, "the `func` type keyword has been renamed to `fn`; write `fn(...) -> T`")
	}
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
		tags = append(tags, p.parseErrorSetItemGroup()...)
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

// parseErrorSetQualifiedSetName parses an error-set name that may be MODULE-QUALIFIED
// (`Mod::Sub::Set`) and returns it in the analyzer's internal dotted spelling
// (`Mod.Sub.Set`) — the same form joinQualifiedName produces when the declaration is
// registered, so the existing lookup finds it with no further translation. A bare `Set`
// is returned unchanged, so unqualified references keep their current behaviour.
//
// Before this, `error[...]` was the one type position in the language whose grammar took a
// bare identifier only: `error[M::MyErr]` failed at the parser with "expected ], got ::",
// which made an error set declared inside a module unnameable from outside it.
func (p *Parser) parseErrorSetQualifiedSetName() string {
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.match(lexer.TOKEN_SCOPE) {
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}

func (p *Parser) parseErrorSetItem() ast.ErrorTagExpr {
	pos := p.cur().Pos
	setName := p.parseErrorSetQualifiedSetName()
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

// parseErrorSetItemGroup parses one error-set item, expanding the member-brace sugar
// `Set{Tag1, Tag2, ...}` into one ErrorTagExpr per tag (equivalent to repeating
// `Set.Tag1, Set.Tag2`). `Set` and `Set.Tag` yield a single tag.
func (p *Parser) parseErrorSetItemGroup() []ast.ErrorTagExpr {
	pos := p.cur().Pos
	// `*Set` is an explicit whole-family spread. A bare `Name` (no `*`, no `.Tag`)
	// resolves as a whole family if Name is a declared set, otherwise as a single
	// variant searched across all error sets.
	if p.match(lexer.TOKEN_STAR) {
		setName := p.parseErrorSetQualifiedSetName()
		return []ast.ErrorTagExpr{{Position: pos, SetName: setName, Tag: "", Family: true}}
	}
	setName := p.parseErrorSetQualifiedSetName()
	if p.match(lexer.TOKEN_LBRACE) {
		var tags []ast.ErrorTagExpr
		for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
			tpos := p.cur().Pos
			tag := p.expect(lexer.TOKEN_IDENT).Text
			tags = append(tags, ast.ErrorTagExpr{Position: tpos, SetName: setName, Tag: tag})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACE)
		return tags
	}
	tag := ""
	if p.match(lexer.TOKEN_DOT) {
		if p.peek() == lexer.TOKEN_STAR {
			tag = p.advance().Text
		} else {
			tag = p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	return []ast.ErrorTagExpr{{Position: pos, SetName: setName, Tag: tag}}
}

// isPostReturnClauseKeyword reports whether an identifier begins a post-return-type signature clause
// (`-> RetType <clause> ...`). These must NOT be mistaken for a legacy `<region> T&` prefix on the
// return type, so an `ident ident` where the second is one of these is the return type followed by a
// clause, not a region-prefixed reference.
func isPostReturnClauseKeyword(text string) bool {
	switch text {
	case "can", "effects", "ensures", "ensure", "requires", "require", "uses", "changes", "preserves", "fulfills":
		return true
	default:
		return false
	}
}

func (p *Parser) parseRefStorageQualifier() (ast.RefStorage, bool, string, string) {
	switch p.peek() {
	case lexer.TOKEN_HEAP:
		tok := p.advance()
		return ast.RefStorageHeap, true, tok.Text, ""
	case lexer.TOKEN_STACK:
		tok := p.advance()
		return ast.RefStorageStack, true, tok.Text, ""
	case lexer.TOKEN_STATIC:
		tok := p.advance()
		return ast.RefStorageStatic, true, tok.Text, ""
	default:
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "any" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text != "where" && !isPostReturnClauseKeyword(p.tokenAt(p.pos+1).Text) {
			// The legacy `<ident> T&` region prefix is removed (docs/68 §7): use the
			// canonical `T& @r` suffix. Now that refstorage params are gone this prefix is
			// unambiguously a region, so we can reject it. Recover by treating the ident as
			// the region so the remainder of the type still parses and only this surfaces.
			name := p.advance().Text
			// `T NAME` (a type juxtaposed with a bare identifier) is most often a refinement type
			// written without `is` — e.g. `u32 RegIndex` for `u32 is RegIndex`. Lead the diagnostic
			// there, since the legacy region prefix this would otherwise be is removed (docs/68 §7).
			if p.peek() == lexer.TOKEN_IDENT {
				next := p.cur().Text
				p.errorf("`%s %s` is not a valid type: for a refinement type write `%s is %s`; the legacy region prefix is removed — for a region use the `@%s` suffix (e.g. `T& @%s`)", name, next, name, next, name, name)
			} else {
				p.errorf("region prefix `%s` on a reference is no longer supported; use the `@%s` suffix instead (e.g. `T& @%s`)", name, name, name)
			}
			return ast.RefStorageAny, true, name, name
		}
		return ast.RefStorageAny, false, "", ""
	}
}
func (p *Parser) parseRefTypeSuffixes(base ast.TypeExpr, pos lexer.Pos, storage ast.RefStorage, explicit bool, region string) (ast.TypeExpr, int) {
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
			typ = &ast.RefType{Position: pos, Elem: typ, State: state, Storage: storage, Region: region, Explicit: explicit}
			count++
		case lexer.TOKEN_BANG:
			p.advance()
			typ = &ast.RefType{Position: pos, Elem: typ, State: ast.RefStateNull, Storage: storage, Region: region, Explicit: explicit}
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
		if !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
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
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_PIPE {
		return false
	}
	return p.tokenAt(p.pos+2).Kind == lexer.TOKEN_IDENT
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
	for p.hasToken(i) {
		tok := p.tokenAt(i)
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
func (p *Parser) parseBaseType(storage ast.RefStorage, explicit bool, label string, region string) ast.TypeExpr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	// Accumulate the SOURCE spelling alongside the dot-joined key: `::` and `.` mean
	// different things and the key cannot represent the difference.
	spelling := name
	parts := 1
	for {
		separator := ""
		if p.peek() == lexer.TOKEN_SCOPE {
			separator = "::"
		} else if p.peek() == lexer.TOKEN_DOT {
			separator = "."
		} else {
			break
		}
		p.advance()
		segment := p.expect(lexer.TOKEN_IDENT).Text
		name += "." + segment
		spelling += separator + segment
		parts++
	}
	// Always recorded for a multi-segment name: an empty Spelling means "not parsed from
	// source separators" (a synthesized node), and the analyzer must not guess for those.
	named := &ast.NamedType{Position: pos, Name: name}
	if parts > 1 {
		named.Spelling = spelling
	}
	var typ ast.TypeExpr = named

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
			if p.hasToken(p.pos + 1) {
				afterBracket = p.tokenAt(p.pos + 1).Kind
			}
			isArray := afterBracket == lexer.TOKEN_INT_LIT || afterBracket == lexer.TOKEN_HEX_LIT
			if !hasTopLevelComma && afterBracket == lexer.TOKEN_IDENT && p.hasToken(p.pos+2) {
				afterIdent := p.tokenAt(p.pos + 2).Kind
				isArray = afterIdent != lexer.TOKEN_AMPERSAND && afterIdent != lexer.TOKEN_QUESTION &&
					afterIdent != lexer.TOKEN_BANG && afterIdent != lexer.TOKEN_COMMA &&
					afterIdent != lexer.TOKEN_LBRACKET && afterIdent != lexer.TOKEN_PIPE && afterIdent != lexer.TOKEN_DOT
				if afterIdent == lexer.TOKEN_RBRACKET {
					argName := p.tokenAt(p.pos + 1).Text
					isArray = isSingleUppercaseIdentifier(name) || isConstantStyleArraySizeIdentifier(argName)
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
				generic := &ast.GenericType{Position: pos, Name: name, Args: args}
				if parts > 1 {
					generic.Spelling = spelling
				}
				typ = generic
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
	typ, refCount = p.parseRefTypeSuffixes(typ, pos, storage, explicit, region)
	if explicit && refCount == 0 {
		if region != "" {
			p.errorf("region qualifier %q requires a pointer type", label)
		} else {
			p.errorf("storage qualifier %q requires a pointer type", label)
		}
	}

	// Each further `[N]` nests another array level (`i64[2][3][4]`), not just one.
	for p.peek() == lexer.TOKEN_LBRACKET {
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
	for i := p.pos; p.hasToken(i); i++ {
		switch p.tokenAt(i).Kind {
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
