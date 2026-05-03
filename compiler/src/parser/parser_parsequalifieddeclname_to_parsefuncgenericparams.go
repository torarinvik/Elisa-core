package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

func (p *Parser) parseQualifiedDeclName() string {
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.match(lexer.TOKEN_DOT) {
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}
func (p *Parser) parseNamespaceDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	isModule := p.peekIdentText("module")
	if isModule {
		p.expectIdentText("module")
	} else {
		p.expectIdentText("namespace")
	}
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseDeclBlock()
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: isModule}
}
func (p *Parser) parseUsingDecl() *ast.UsingDecl {
	pos := p.cur().Pos
	p.expectIdentText("using")
	name := p.parseQualifiedDeclName()
	p.expectNewline()
	return &ast.UsingDecl{Position: pos, Name: name}
}
func (p *Parser) parseAttributeDecl() *ast.AttributeDecl {
	pos := p.cur().Pos
	p.expectIdentText("attribute")
	target := p.parseQualifiedDeclName()
	dot := strings.LastIndex(target, ".")
	var receiver ast.TypeExpr
	name := target
	if dot <= 0 || dot == len(target)-1 {
		p.errorf("attribute declaration expects receiver.name, got %q", target)
	} else {
		receiver = &ast.NamedType{Position: pos, Name: target[:dot]}
		name = target[dot+1:]
	}
	var retType ast.TypeExpr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	arms := p.parseVisitArms()
	return &ast.AttributeDecl{Position: pos, Receiver: receiver, Name: name, ReturnType: retType, Arms: arms}
}
func (p *Parser) parseEnumDecl() *ast.EnumDecl {
	return p.parseEnumDeclWithAnnotations(nil)
}
func (p *Parser) parseEnumDeclWithAnnotations(annotations []ast.Annotation) *ast.EnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ENUM)
	return p.parseEnumDeclRest(pos, false, annotations)
}
func (p *Parser) parseEnumDeclRest(pos lexer.Pos, packed bool, annotations []ast.Annotation) *ast.EnumDecl {
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	itemCapacity := p.estimateIndentedItemCount()
	commonFields := make([]ast.FieldDecl, 0, itemCapacity/2)
	variants := make([]ast.EnumVariantDecl, 0, itemCapacity)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			commonFields = append(commonFields, p.parseEnumCommonFields()...)
			continue
		}
		variants = append(variants, p.parseEnumVariantDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Common: commonFields, Variants: variants}
}
func (p *Parser) parseEnumCommonFields() []ast.FieldDecl {
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return fields
}
func (p *Parser) parseEnumVariantDecl() ast.EnumVariantDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	payload := make([]ast.EnumPayloadDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				payload = append(payload, p.parseEnumPayloadDecl())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expectNewline()
	return ast.EnumVariantDecl{Position: pos, Name: name, Payload: payload}
}
func (p *Parser) parseEnumPayloadDecl() ast.EnumPayloadDecl {
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		relationText := p.tokens[p.pos].Text
		var relation ast.EnumPayloadRelation
		switch relationText {
		case string(ast.EnumPayloadRelationChild):
			relation = ast.EnumPayloadRelationChild
		case string(ast.EnumPayloadRelationChildren):
			relation = ast.EnumPayloadRelationChildren
		case string(ast.EnumPayloadRelationLink):
			relation = ast.EnumPayloadRelationLink
		}
		colonIndex := p.pos + 2
		optionalNamed := false
		if colonIndex < len(p.tokens) && p.tokens[colonIndex].Kind == lexer.TOKEN_QUESTION {
			optionalNamed = true
			colonIndex++
		}
		if relation != ast.EnumPayloadRelationNone && colonIndex < len(p.tokens) && p.tokens[colonIndex].Kind == lexer.TOKEN_COLON {
			pos := p.cur().Pos
			p.expect(lexer.TOKEN_IDENT)
			nameTok := p.expect(lexer.TOKEN_IDENT)
			if optionalNamed {
				p.expect(lexer.TOKEN_QUESTION)
			}
			p.expect(lexer.TOKEN_COLON)
			typ := p.parseTypeExpr()
			if optionalNamed {
				typ = &ast.OptionalTypeExpr{Position: nameTok.Pos, Value: typ}
			}
			return ast.EnumPayloadDecl{Position: pos, Relation: relation, Name: nameTok.Text, Type: typ}
		}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) {
		optionalNamed := p.tokens[p.pos+1].Kind == lexer.TOKEN_QUESTION && p.pos+2 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
		if p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON || optionalNamed {
			pos := p.cur().Pos
			nameTok := p.expect(lexer.TOKEN_IDENT)
			if optionalNamed {
				p.expect(lexer.TOKEN_QUESTION)
			}
			p.expect(lexer.TOKEN_COLON)
			typ := p.parseTypeExpr()
			if optionalNamed {
				typ = &ast.OptionalTypeExpr{Position: nameTok.Pos, Value: typ}
			}
			return ast.EnumPayloadDecl{Position: pos, Name: nameTok.Text, Type: typ}
		}
	}
	typ := p.parseTypeExpr()
	return ast.EnumPayloadDecl{Position: typ.Pos(), Type: typ}
}
func (p *Parser) parseTreeDecl() *ast.TreeDecl {
	return p.parseTreeDeclWithAnnotations(nil)
}
func (p *Parser) parseTreeDeclWithAnnotations(annotations []ast.Annotation) *ast.TreeDecl {
	pos := p.cur().Pos
	p.expectIdentText("tree")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	itemCapacity := p.estimateIndentedItemCount()
	commonFields := make([]ast.FieldDecl, 0, itemCapacity/2)
	members := make([]ast.TreeMemberDecl, 0, itemCapacity)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		memberAnnotations := p.parseAnnotations()
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			if len(memberAnnotations) != 0 {
				for _, annotation := range memberAnnotations {
					p.errorf("tree common: block does not support declaration annotation @%s", annotation.Name)
				}
			}
			commonFields = append(commonFields, p.parseTreeCommonFields()...)
			continue
		}
		if p.peekTreeMemberHeader("block") {
			members = append(members, p.parseTreeBlockDecl(memberAnnotations))
			continue
		}
		if p.peek() == lexer.TOKEN_STRUCT {
			members = append(members, p.parseTreeStructMemberDecl(memberAnnotations))
			continue
		}
		if p.peekTreeMemberHeader("node") {
			members = append(members, p.parseTreeCategoryDecl(memberAnnotations, ""))
			continue
		}
		p.errorf("expected tree member declaration, got %s", p.cur())
		p.advance()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Common: commonFields, Members: members}
}
func (p *Parser) parseTreeCommonFields() []ast.FieldDecl {
	p.expect(lexer.TOKEN_IDENT)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return fields
}
func (p *Parser) peekTreeMemberHeader(keyword string) bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == keyword && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
}
func (p *Parser) parseTreeCategoryDecl(annotations []ast.Annotation, prefix string) *ast.TreeCategoryDecl {
	pos := p.cur().Pos
	p.expectIdentText("node")
	localName := p.expect(lexer.TOKEN_IDENT).Text
	name := prefix + localName
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	variants := make([]ast.EnumVariantDecl, 0, p.estimateIndentedItemCount())
	nested := make([]ast.TreeCategoryDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		memberAnnotations := p.parseAnnotations()
		if p.peekTreeMemberHeader("node") {
			nested = append(nested, *p.parseTreeCategoryDecl(memberAnnotations, name+"."))
			continue
		}
		if len(memberAnnotations) != 0 {
			for _, annotation := range memberAnnotations {
				p.errorf("tree variant does not support declaration annotation @%s", annotation.Name)
			}
		}
		variants = append(variants, p.parseEnumVariantDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeCategoryDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Variants: variants, Nested: nested}
}
func (p *Parser) parseTreeBlockDecl(annotations []ast.Annotation) *ast.TreeBlockDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeBlockDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Fields: fields}
}
func (p *Parser) parseTreeStructMemberDecl(annotations []ast.Annotation) *ast.TreeStructDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STRUCT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.TreeStructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Fields: fields}
}
func (p *Parser) parseConstDecl() *ast.ConstDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	name := p.expect(lexer.TOKEN_IDENT).Text

	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_COLON) {
		typ = p.parseTypeExpr()
	}

	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseExpr()
	p.expectNewlineAfterValueExpr(value)

	return &ast.ConstDecl{Position: pos, Name: name, Type: typ, Value: value}
}
func (p *Parser) parseTokenSetDecl() *ast.TokenSetDecl {
	pos := p.cur().Pos
	p.expectIdentText("tokenset")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var elemType ast.TypeExpr
	if p.match(lexer.TOKEN_COLON) {
		elemType = p.parseTypeExpr()
	}
	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseExpr()
	p.expectNewlineAfterValueExpr(value)
	list, ok := value.(*ast.ListLitExpr)
	if !ok {
		p.errorAt(value.Pos(), "tokenset initializer must be a list literal")
		list = &ast.ListLitExpr{Position: value.Pos()}
	}
	return &ast.TokenSetDecl{Position: pos, Name: name, ElemType: elemType, Value: list}
}
func (p *Parser) parseConstEnumDecl() *ast.ConstEnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	p.expect(lexer.TOKEN_ENUM)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("of")
	storage := p.parseTypeExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]ast.ConstEnumMemberDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		members = append(members, p.parseConstEnumMemberDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.ConstEnumDecl{Position: pos, Name: name, Storage: storage, Members: members}
}
func (p *Parser) parseConstEnumMemberDecl() ast.ConstEnumMemberDecl {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var value ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		value = p.parseExpr()
	}
	p.expectNewlineAfterValueExpr(value)
	return ast.ConstEnumMemberDecl{Position: pos, Name: name, Value: value}
}
func (p *Parser) parseGlobalDecl() *ast.GlobalDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_GLOBAL)
	mutable := p.match(lexer.TOKEN_MUTABLE)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	typ := p.parseTypeExpr()

	var value ast.Expr
	if p.match(lexer.TOKEN_ASSIGN) {
		value = p.parseExpr()
	}
	p.expectNewlineAfterValueExpr(value)

	return &ast.GlobalDecl{Position: pos, Mutable: mutable, Name: name, Type: typ, Value: value}
}
func (p *Parser) parseStructDecl() *ast.StructDecl {
	return p.parseStructDeclWithAnnotations(nil)
}
func (p *Parser) parseStructDeclWithAnnotations(annotations []ast.Annotation) *ast.StructDecl {
	pos := p.cur().Pos
	affine := p.matchIdentText("affine")
	reprC := true
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
	var refStorageParams []string
	var refStateParams []string
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
		typeParams, refStorageParams, refStateParams, _, _, genericParams = p.parseGenericParamListAfterLBracket(false, false)
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

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	fields := make([]ast.FieldDecl, 0, p.estimateIndentedItemCount())
	derivedStates := make([]ast.DerivedStateDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "derive" {
			derivedStates = append(derivedStates, p.parseDerivedStateBlock()...)
			continue
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RefStorageParams: refStorageParams, RefStateParams: refStateParams, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, NamedStateCases: append([]string(nil), namedStateCases...), DerivedStates: derivedStates, Affine: affine, ReprC: reprC, Fields: fields}
}
func (p *Parser) peekNamedStructStateBracket() bool {
	return p.peek() == lexer.TOKEN_LBRACKET && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "state"
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

	typ := p.parseTypeExpr()
	p.expectNewline()

	return ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Mutable: mutable, IsTail: isTail, Type: typ}
}
func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	return p.parseFuncDeclWithAnnotations(nil)
}
func (p *Parser) parseFuncGenericParams() ([]string, []string, []string, []string, []string, []ast.GenericParam) {
	if !p.match(lexer.TOKEN_LBRACKET) {
		return nil, nil, nil, nil, nil, nil
	}
	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseGenericParamListAfterLBracket(true, true)
	p.expect(lexer.TOKEN_RBRACKET)
	return typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams
}
