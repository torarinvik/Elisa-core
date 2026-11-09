package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseStructDecl() ast.Decl {
	return p.parseStructDeclWithAnnotations(nil)
}
func (p *Parser) parseStructDeclWithAnnotations(annotations []ast.Annotation) ast.Decl {
	return p.parseStructDeclWithLeadingLayout(annotations, ast.StructLayoutDefault, false, p.cur().Pos)
}
func (p *Parser) parseStructDeclWithLeadingLayout(annotations []ast.Annotation, leadingLayout ast.StructLayoutMode, leadingReprC bool, pos lexer.Pos) ast.Decl {
	// Two distinct single-use disciplines (both move-only / use-at-most-once):
	//   `linear` = must be consumed exactly once (cannot be dropped); the
	//             must-consume-before-scope-exit obligation applies.
	//   `affine` = may be used at most once but MAY be dropped silently (no
	//             must-consume obligation).
	// Droppable defaults false so any later propagation gap is over-strict
	// (treated as must-consume / linear), never unsound.
	affine := false
	droppable := false
	if p.matchIdentText("linear") {
		affine = true
	} else if p.matchIdentText("affine") {
		affine = true
		droppable = true
	}
	reprC := leadingReprC
	if p.peek() == lexer.TOKEN_REPR {
		p.errorf("legacy `repr(c) struct` syntax is no longer supported; use `struct` instead")
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		p.expect(lexer.TOKEN_IDENT) // "c"
		p.expect(lexer.TOKEN_RPAREN)
		reprC = true
	}
	p.expect(lexer.TOKEN_STRUCT)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typeParams []string
	var regionParams []string
	var genericParams []ast.GenericParam
	hasStateParam := false
	stateParamCount := 0
	var namedStateCases []string
	if states, ok := p.peekAggregateStateBracketList(); ok {
		states = p.parseAggregateStateBracketList()
		for _, state := range states {
			if state != ast.RefStateNullable {
				p.errorf("struct state parameter declaration must use only [?] placeholders, got [%s]", joinAggregateStateMarkers(states))
				break
			}
		}
		hasStateParam = len(states) > 0
		stateParamCount = len(states)
	} else if p.peekNamedStructStateBracket() {
		namedStateCases = p.parseNamedStructStateBracket(name)
		genericParams = append(genericParams, ast.GenericParam{Position: pos, Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), namedStateCases...), StateOwner: name})
	} else if p.match(lexer.TOKEN_LBRACKET) {
		typeParams, regionParams, _, genericParams = p.parseGenericParamListAfterLBracket(true, false)
		p.expect(lexer.TOKEN_RBRACKET)
		if states, ok := p.peekAggregateStateBracketList(); ok {
			states = p.parseAggregateStateBracketList()
			for _, state := range states {
				if state != ast.RefStateNullable {
					p.errorf("struct state parameter declaration must use only [?] placeholders, got [%s]", joinAggregateStateMarkers(states))
					break
				}
			}
			hasStateParam = len(states) > 0
			stateParamCount = len(states)
		} else if p.peekNamedStructStateBracket() {
			namedStateCases = p.parseNamedStructStateBracket(name)
			genericParams = append(genericParams, ast.GenericParam{Position: pos, Kind: ast.GenericParamState, Name: "state", StateCases: append([]string(nil), namedStateCases...), StateOwner: name})
		}
	}

	layout := leadingLayout
	if p.matchIdentText("layout") {
		var opts layoutClauseOptions
		if p.peek() == lexer.TOKEN_LPAREN {
			// Canonical parenthesized layout clause (one grammar across struct/enum/
			// overlay): `layout(soa)`, `layout(c)`, `layout(packed)`,
			// `layout(guest, size: 72)`.
			opts = p.parseLayoutClauseOptions()
		} else {
			// Removed bare-word spelling `struct Name layout soa:` — directed error,
			// recover as the canonical parenthesized clause (same mode, no cascade).
			mode := p.cur()
			if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
				p.errorf("expected `layout(...)` clause, got %s", mode)
			} else {
				p.errorf("bare `layout %s` has been removed; use the parenthesized clause `layout(%s)`", mode.Text, mode.Text)
				p.advance()
				opts.Mode = mode.Text
			}
			opts.Size = -1
		}
		if opts.Sparse {
			p.errorf("`sparse` is an enum layout option; it has no meaning on a struct")
		}
		if opts.HandleWidth != "" {
			p.errorf("`handle: uN` is an enum layout option; it has no meaning on a struct")
		}
		if opts.Guest {
			// `struct Name layout(guest, size: N):` — a typed guest-memory overlay
			// (docs/107). The body is `field: type at OFFSET [requires size >= N]`
			// lines producing the LayoutDecl the overlay checker consumes.
			return p.parseGuestOverlayBody(name, opts.Size, pos)
		}
		if opts.Size >= 0 {
			p.errorf("`size: N` is only meaningful with the `guest` overlay layout")
		}
		switch opts.Mode {
		case "aos":
			layout = ast.StructLayoutAOS
			reprC = false
		case "soa":
			layout = ast.StructLayoutSOA
			reprC = false
		case "c":
			layout = ast.StructLayoutC
			reprC = true
		case "packed":
			layout = ast.StructLayoutPacked
			reprC = false
		case "":
			p.errorf("struct `layout(...)` needs a mode: `aos`, `soa`, `c`, `packed`, or `guest`")
		default:
			p.errorf("unsupported struct layout %q; expected `aos`, `soa`, `c`, `packed`, or `guest`", opts.Mode)
		}
	}

	// The `struct X in owner:` declaration sugar is removed (docs/68 §7): declare the
	// region as a type parameter instead, `struct X[@owner]:`. RegionOwner is
	// kept vestigially ("" always) to avoid churn in the downstream readers.
	regionOwner := ""
	if p.peek() == lexer.TOKEN_IN {
		p.advance() // consume `in`
		ownerName := ""
		if p.peek() == lexer.TOKEN_IDENT {
			ownerName = p.advance().Text // consume the region name to recover
		}
		p.errorf("`struct %s in %s:` is no longer supported; declare the region as a parameter: `struct %s[@%s]:`", name, ownerName, name, ownerName)
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	derivedStates := make([]ast.DerivedStateDecl, 0)
	hasDerivedStateBlock := false
	var stateTransitions []ast.StateTransitionDecl
	var invariants []ast.Expr
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "derive" {
			hasDerivedStateBlock = true
			derivedStates = append(derivedStates, p.parseDerivedStateBlock()...)
			continue
		}
		if p.peekIdentText("transitions") && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_NEWLINE {
			stateTransitions = append(stateTransitions, p.parseStateTransitionGraph()...)
			continue
		}
		// `invariant <bool-expr>` field-contract over the struct's fields (referencing self.field).
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "invariant" && p.looksLikeContractStmt() {
			p.advance()
			invariants = append(invariants, p.parseExpr())
			p.expectNewline()
			continue
		}
		if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "private" || p.cur().Text == "public") && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_NEWLINE {
			private := p.advance().Text == "private"
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			p.expect(lexer.TOKEN_INDENT)
			for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
				p.skipNewlines()
				if p.peek() == lexer.TOKEN_DEDENT {
					break
				}
				field := p.parseFieldDecl()
				field.Private = private
				fields = append(fields, field)
			}
			p.expect(lexer.TOKEN_DEDENT)
			continue
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RegionParams: regionParams, RegionOwner: regionOwner, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, NamedStateCases: append([]string(nil), namedStateCases...), DerivedStates: derivedStates, HasDerivedStateBlock: hasDerivedStateBlock, StateTransitions: stateTransitions, Affine: affine, Droppable: droppable, ReprC: reprC, Layout: layout, Fields: fields, Invariants: invariants}
}
func (p *Parser) peekNamedStructStateBracket() bool {
	return p.peek() == lexer.TOKEN_LBRACKET && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "state"
}
func (p *Parser) parseNamedStructStateBracket(structName string) []string {
	p.expect(lexer.TOKEN_LBRACKET)
	p.expectIdentText("state")
	cases := make([]string, 0, 2)
	seen := map[string]bool{}
	for {
		name := p.expect(lexer.TOKEN_IDENT).Text
		if seen[name] {
			p.errorf("duplicate struct state %q in %q", name, structName)
		} else {
			seen[name] = true
			cases = append(cases, name)
		}
		if !p.match(lexer.TOKEN_PIPE) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return cases
}
func (p *Parser) parseDerivedStateBlock() []ast.DerivedStateDecl {
	p.expectIdentText("derive")
	p.expectIdentText("state")
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	derived := make([]ast.DerivedStateDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		pos := p.cur().Pos
		stateName := p.expect(lexer.TOKEN_IDENT).Text
		p.expectIdentText("when")
		cond := p.parseExpr()
		p.expectNewline()
		derived = append(derived, ast.DerivedStateDecl{Position: pos, StateName: stateName, Condition: cond})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return derived
}
func joinAggregateStateMarkers(states []ast.RefState) string {
	if len(states) == 0 {
		return ""
	}
	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, ast.RefStateMarker(state))
	}
	return strings.Join(parts, ", ")
}
func (p *Parser) parseFieldDecl() ast.FieldDecl {
	annotations := p.parseAnnotations()
	pos := p.cur().Pos
	// `ghost name: T` — a verification-only model field. The `ghost` keyword is a leading IDENT
	// modifier; it is only consumed when followed by another identifier (the field name), so a
	// field literally named `ghost` (`ghost: T`) still parses as an ordinary field.
	ghost := false
	if p.peekIdentText("ghost") && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		p.advance()
		ghost = true
		pos = p.cur().Pos
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)

	mutable := false
	isTail := false

	if p.match(lexer.TOKEN_MUTABLE) {
		mutable = true
	}
	if p.match(lexer.TOKEN_TAIL) {
		isTail = true
	}

	if p.peekIdentText("bitset") || p.peekIdentText("bitfield") {
		if mutable {
			p.errorAt(pos, "packed struct groups cannot be marked mutable; mutate their members instead")
		}
		if isTail {
			p.errorAt(pos, "packed struct groups cannot be tail fields")
		}
		if ghost {
			p.errorAt(pos, "a `ghost` field cannot be a packed bit group")
		}
		group := p.parseBitGroupDecl()
		return ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, BitGroup: group}
	}

	typ := p.parseTypeExpr()
	var defaultValue ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		defaultValue = p.parseExpr()
	}
	p.expectNewline()

	return ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Mutable: mutable, IsTail: isTail, Ghost: ghost, Type: typ, DefaultValue: defaultValue}
}

func (p *Parser) parseBitGroupDecl() *ast.BitGroupDecl {
	pos := p.cur().Pos
	kind := ast.BitGroupBitset
	if p.matchIdentText("bitfield") {
		kind = ast.BitGroupBitfield
	} else {
		p.expectIdentText("bitset")
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	members := make([]ast.BitGroupMemberDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		memberPos := p.cur().Pos
		memberName := p.expect(lexer.TOKEN_IDENT).Text
		var memberType ast.TypeExpr
		if kind == ast.BitGroupBitfield {
			p.expect(lexer.TOKEN_COLON)
			memberType = p.parseTypeExpr()
		}
		p.expectNewline()
		members = append(members, ast.BitGroupMemberDecl{Position: memberPos, Name: memberName, Type: memberType})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.BitGroupDecl{Position: pos, Kind: kind, Members: members}
}
func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	return p.parseFuncDeclWithAnnotations(nil)
}
func (p *Parser) parseFuncGenericParams() ([]string, []string, []string, []ast.GenericParam) {
	if !p.match(lexer.TOKEN_LBRACKET) {
		return nil, nil, nil, nil
	}
	typeParams, regionParams, permissionParams, genericParams := p.parseGenericParamListAfterLBracket(true, true)
	p.expect(lexer.TOKEN_RBRACKET)
	return typeParams, regionParams, permissionParams, genericParams
}
