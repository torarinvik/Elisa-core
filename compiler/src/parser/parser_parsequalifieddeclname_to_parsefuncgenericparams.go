package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseQualifiedDeclName() string {
	name := p.expect(lexer.TOKEN_IDENT).Text
	for p.matchQualifiedNameSeparator() {
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}

func (p *Parser) matchQualifiedNameSeparator() bool {
	return p.match(lexer.TOKEN_DOT) || p.match(lexer.TOKEN_SCOPE)
}

// parseQualifiedIdentNameAfterFirst consumes `::`-separated qualified-name
// segments in EXPRESSION position. It deliberately stops at `.`: `::`
// qualifies namespaces while `.` accesses value members, so
// `Shapes::Form.Circle` is the qualified name `Shapes.Form` followed by
// member access `.Circle` (handled by parsePostfix), not one
// three-segment name.
func (p *Parser) parseQualifiedIdentNameAfterFirst(first string) string {
	name := first
	for p.peek() == lexer.TOKEN_SCOPE {
		p.advance()
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}
func (p *Parser) parseNamespaceDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("module")
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseDeclBlock()
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true}
}
func (p *Parser) parseConstModuleDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	p.expectIdentText("module")
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	decls := make([]ast.Decl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		decls = append(decls, p.parseConstModuleMemberDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true, Const: true}
}
func (p *Parser) parseConstModuleMemberDecl() *ast.ConstDecl {
	pos := p.cur().Pos
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
func (p *Parser) parseUsingDecl() *ast.UsingDecl {
	pos := p.cur().Pos
	p.expectIdentText("using")
	name := p.parseQualifiedDeclName()
	p.expectNewline()
	return &ast.UsingDecl{Position: pos, Name: name}
}

// parseImportDecl parses `from Module import a, b` — a selective import that
// brings only the named members of an in-program module/namespace into scope.
func (p *Parser) parseImportDecl() *ast.ImportDecl {
	pos := p.cur().Pos
	p.expectIdentText("from")
	module := p.parseQualifiedDeclName()
	p.expectIdentText("import")
	names := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_COMMA) {
		names = append(names, p.expect(lexer.TOKEN_IDENT).Text)
	}
	p.expectNewline()
	return &ast.ImportDecl{Position: pos, Module: module, Names: names}
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
	// Optional sealed-refinement suffix (docs/77): `enum Child is Parent:`. `is` reads as the
	// Liskov "is-a" relation — Child's cases are a subset of Parent's, so Child <: Parent.
	parent := ""
	if p.peek() == lexer.TOKEN_IS {
		p.advance()
		parent = p.expect(lexer.TOKEN_IDENT).Text
		for p.matchQualifiedNameSeparator() {
			parent += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
	}
	layout, layoutSet, sparse, indexWidth := p.parseEnumLayoutSuffix()
	p.expect(lexer.TOKEN_COLON)
	// An abstract root that only gathers sub-categories may use the inline empty body `enum Node: pass`
	// (docs/77) instead of an indented block.
	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Layout: layout, LayoutSet: layoutSet, LayoutSparse: sparse, IndexWidth: indexWidth, Parent: parent}
	}
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
		// An abstract root that only gathers sub-categories has no variants: `enum Node: pass`.
		if p.peek() == lexer.TOKEN_PASS {
			p.advance()
			continue
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.pos+1 < len(p.tokens) {
			switch p.tokens[p.pos+1].Kind {
			case lexer.TOKEN_COLON:
				// Legacy indented block form: `common:` then fields below.
				commonFields = append(commonFields, p.parseEnumCommonFields()...)
				continue
			case lexer.TOKEN_LPAREN:
				// Canonical inline form (docs/76): `common(span: int, metadata: cstr)`.
				commonFields = append(commonFields, p.parseEnumCommonFieldsInline()...)
				continue
			}
		}
		variants = append(variants, p.parseEnumVariantDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Common: commonFields, Variants: variants, Layout: layout, LayoutSet: layoutSet, LayoutSparse: sparse, IndexWidth: indexWidth, Parent: parent}
}

// parseEnumLayoutSuffix parses an optional `layout soa|aos|c|packed` suffix on an enum declaration
// (docs/76), with optional `(sparse)` and/or `(handle: uN)` sub-options, e.g.
//
//	enum Expr layout soa:
//	enum Expr layout soa(sparse):
//	enum Small layout aos(handle: u16):
//	enum Small layout(handle: u16):       # mode omitted — keeps the default layout (docs/82)
//
// `handle:` is the canonical key for the opaque-handle width (docs/82); `index:` is the
// deprecated docs/76 spelling, kept as an alias. `layout` reuses the struct layout grammar
// (docs/01) — one vocabulary across structs and enums. It is layout-only: it carries no region
// and no usage meaning (orthogonality, docs/10).
func (p *Parser) parseEnumLayoutSuffix() (ast.StructLayoutMode, bool, bool, string) {
	if !p.matchIdentText("layout") {
		return ast.StructLayoutDefault, false, false, ""
	}
	layout := ast.StructLayoutDefault
	if p.peek() != lexer.TOKEN_LPAREN { // mode-less `layout(...)` keeps the default mode
		mode := p.cur()
		if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
			p.errorf("expected enum layout mode `aos`, `soa`, `c`, or `packed`, got %s", mode)
		} else {
			p.advance()
		}
		switch mode.Text {
		case "aos":
			layout = ast.StructLayoutAOS
		case "soa":
			layout = ast.StructLayoutSOA
		case "c":
			layout = ast.StructLayoutC
		case "packed":
			layout = ast.StructLayoutPacked
		default:
			p.errorf("unsupported enum layout %q; expected `aos`, `soa`, `c`, or `packed`", mode.Text)
		}
	}
	sparse := false
	indexWidth := ""
	if p.match(lexer.TOKEN_LPAREN) {
		for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
			opt := p.cur()
			if opt.Kind == lexer.TOKEN_IDENT && opt.Text == "sparse" {
				p.advance()
				sparse = true
			} else if opt.Kind == lexer.TOKEN_IDENT && (opt.Text == "handle" || opt.Text == "index") {
				p.advance()
				p.expect(lexer.TOKEN_COLON)
				width := p.expect(lexer.TOKEN_IDENT).Text
				switch width {
				case "u8", "u16", "u32", "u64", "ptr":
					indexWidth = width
				default:
					p.errorf("enum handle width must be u8, u16, u32, u64, or ptr, got %q", width)
				}
			} else {
				p.errorf("unexpected enum layout option %s; expected `sparse` or `handle: uN`", opt)
				p.advance()
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return layout, true, sparse, indexWidth
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

// parseEnumCommonFieldsInline parses the canonical inline shared-field form (docs/76):
//
//	common(span: int, metadata: cstr)
//
// and its multi-line variant (newlines and a trailing comma are allowed inside the parens, like a
// long function signature). It produces the same []ast.FieldDecl as the legacy `common:` block.
func (p *Parser) parseEnumCommonFieldsInline() []ast.FieldDecl {
	p.expect(lexer.TOKEN_IDENT) // "common"
	p.expect(lexer.TOKEN_LPAREN)
	fields := make([]ast.FieldDecl, 0, 4)
	for {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_RPAREN || p.peek() == lexer.TOKEN_EOF {
			break
		}
		annotations := p.parseAnnotations()
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		mutable := false
		if p.match(lexer.TOKEN_MUTABLE) {
			mutable = true
		}
		typ := p.parseTypeExpr()
		fields = append(fields, ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Mutable: mutable, Type: typ})
		p.skipNewlines()
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.skipNewlines()
	p.expect(lexer.TOKEN_RPAREN)
	p.expectNewline()
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
func (p *Parser) parseCharsetDecl() *ast.CharsetDecl {
	pos := p.cur().Pos
	p.expectIdentText("charset")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	terms := []ast.LexerCharClassTerm{p.parseLexerCharClassTerm()}
	for p.match(lexer.TOKEN_PIPE) {
		terms = append(terms, p.parseLexerCharClassTerm())
	}
	p.expectNewline()
	return &ast.CharsetDecl{Position: pos, Name: name, Terms: terms}
}
func (p *Parser) parseKeywordMapDecl() *ast.KeywordMapDecl {
	pos := p.cur().Pos
	p.expectIdentText("keywordmap")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	inputType := p.parseTypeExpr()
	p.expect(lexer.TOKEN_ARROW)
	returnType := p.parseTypeExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	entries := make([]ast.KeywordMapEntry, 0, p.estimateIndentedItemCount())
	seen := map[string]lexer.Pos{}
	var fallback ast.Expr
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		entry, isFallback := p.parseKeywordMapEntry()
		if isFallback {
			if fallback != nil {
				p.errorAt(entry.Position, "keywordmap can only have one `_` fallback arm")
			}
			fallback = entry.Value
			continue
		}
		if prev, exists := seen[entry.Text]; exists {
			p.errorAt(entry.Position, "duplicate keywordmap entry %q (first seen at %s:%d:%d)", entry.Text, prev.File, prev.Line, prev.Col)
		} else {
			seen[entry.Text] = entry.Position
		}
		entries = append(entries, entry)
	}
	p.expect(lexer.TOKEN_DEDENT)
	if fallback == nil {
		p.errorAt(pos, "keywordmap requires a default fallback")
		fallback = &ast.ZeroedLit{Position: pos}
	}
	return &ast.KeywordMapDecl{Position: pos, Name: name, InputType: inputType, ReturnType: returnType, Fallback: fallback, Entries: entries}
}

func (p *Parser) parseKeywordMapEntry() (ast.KeywordMapEntry, bool) {
	pos := p.cur().Pos
	isFallback := p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_"
	text := ""
	if isFallback {
		p.advance()
	} else {
		text = p.expect(lexer.TOKEN_STRING_LIT).Text
	}
	p.expect(lexer.TOKEN_FATARROW)
	value := p.parseExpr()
	p.expectNewline()
	return ast.KeywordMapEntry{Position: pos, Text: text, Value: value}, isFallback
}
func (p *Parser) parseConstEnumDecl() *ast.ConstEnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	p.expect(lexer.TOKEN_ENUM)
	name := p.expect(lexer.TOKEN_IDENT).Text
	var storage ast.TypeExpr
	if p.matchIdentText("of") {
		storage = p.parseTypeExpr()
	}
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
	return p.parseStructDeclWithLeadingLayout(annotations, ast.StructLayoutDefault, false, p.cur().Pos)
}
func (p *Parser) parseStructDeclWithLeadingLayout(annotations []ast.Annotation, leadingLayout ast.StructLayoutMode, leadingReprC bool, pos lexer.Pos) *ast.StructDecl {
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
		mode := p.cur()
		if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
			p.errorf("expected struct layout mode `aos`, `soa`, `c`, or `packed`, got %s", mode)
		} else {
			p.advance()
		}
		switch mode.Text {
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
		default:
			p.errorf("unsupported struct layout %q; expected `aos`, `soa`, `c`, or `packed`", mode.Text)
		}
	}

	// The `struct X in owner:` declaration sugar is removed (docs/68 §7): declare the
	// region as a type parameter instead, `struct X[region owner]:`. RegionOwner is
	// kept vestigially ("" always) to avoid churn in the downstream readers.
	regionOwner := ""
	if p.peek() == lexer.TOKEN_IN {
		p.advance() // consume `in`
		ownerName := ""
		if p.peek() == lexer.TOKEN_IDENT {
			ownerName = p.advance().Text // consume the region name to recover
		}
		p.errorf("`struct %s in %s:` is no longer supported; declare the region as a parameter: `struct %s[region %s]:`", name, ownerName, name, ownerName)
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

	return &ast.StructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RegionParams: regionParams, RegionOwner: regionOwner, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, NamedStateCases: append([]string(nil), namedStateCases...), DerivedStates: derivedStates, Affine: affine, Droppable: droppable, ReprC: reprC, Layout: layout, Fields: fields}
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

	if p.peekIdentText("bitset") || p.peekIdentText("bitfield") {
		if mutable {
			p.errorAt(pos, "packed struct groups cannot be marked mutable; mutate their members instead")
		}
		if isTail {
			p.errorAt(pos, "packed struct groups cannot be tail fields")
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

	return ast.FieldDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Mutable: mutable, IsTail: isTail, Type: typ, DefaultValue: defaultValue}
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
