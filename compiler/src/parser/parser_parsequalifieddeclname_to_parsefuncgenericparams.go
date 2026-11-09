package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

// parseModulePathName parses the name of a `module` / `extend` / `const module`
// declaration. A module path is spelled with `::` and nothing else.
//
// The dotted form used to parse here silently. It is not the language's namespace
// separator (`.` accesses a value member), and accepting it split the ecosystem: a
// `module A.B:` declaration registered a namespace that no `A::B::f()` use site names,
// which on the self-hosted compiler produced a module that was declared and then
// unreachable. Rejecting it is the fix -- normalising would keep two spellings alive for
// one concept.
//
// The whole chain is still CONSUMED after the error so the declaration body parses
// normally and the user sees this one diagnostic rather than a cascade.
func (p *Parser) parseModulePathName() string {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	dotted := false
	for {
		if p.peek() == lexer.TOKEN_DOT {
			dotted = true
		} else if p.peek() != lexer.TOKEN_SCOPE {
			break
		}
		p.advance()
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	if dotted {
		p.errorAt(pos, "module paths are separated by `::`, not `.`; write `%s`", ast.ModulePathSpelling(name))
	}
	return name
}

// parseQualifiedDeclName parses a qualified TYPE-LIKE name in a declaration position:
// a generic bound, an `impl` interface, a protocol base, a `from` import source, a
// `let Type{...}` destructure target. None of these can name an enum variant, so every
// separator in the chain is a module-path separator and must be `::`; a `.` is rejected
// the way `module A.B:` is, and the chain is still consumed so the declaration parses.
func (p *Parser) parseQualifiedDeclName() string {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	dotted := false
	for {
		if p.peek() == lexer.TOKEN_DOT {
			dotted = true
		} else if p.peek() != lexer.TOKEN_SCOPE {
			break
		}
		p.advance()
		name += "." + p.expect(lexer.TOKEN_IDENT).Text
	}
	if dotted {
		p.errorAt(pos, "module paths are separated by `::`, not `.`; write `%s`", ast.ModulePathSpelling(name))
	}
	return name
}

// parseVariantPathName parses a qualified ENUM.Variant path in a pattern: `Kind.A`,
// `Pack::Kind.A`. The last separator selects the variant and may be `.`; every
// separator before it walks a module and must be `::`. The whole chain is consumed
// either way. Returns the flattened dotted key and the raw separator list.
func (p *Parser) parseVariantPathName(pos lexer.Pos, first string) []string {
	parts, _ := p.parseVariantPathNameWithSeparators(pos, first)
	return parts
}

// parseVariantPathNameWithSeparators is parseVariantPathName that also hands back
// the separator list, so a caller can tell an all-`::` TYPE path (`Pack::Item`, a
// struct pattern when followed by `{`/`(`) from an enum VARIANT path (`Pack::Kind.A`).
func (p *Parser) parseVariantPathNameWithSeparators(pos lexer.Pos, first string) ([]string, []string) {
	parts := []string{first}
	separators := []string{}
	for {
		if p.peek() == lexer.TOKEN_DOT {
			separators = append(separators, ".")
		} else if p.peek() == lexer.TOKEN_SCOPE {
			separators = append(separators, "::")
		} else {
			break
		}
		p.advance()
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	for i := 0; i+1 < len(separators); i++ {
		if separators[i] == "." {
			corrected := parts[0]
			for j, sep := range separators {
				if j+1 < len(separators) {
					sep = "::"
				}
				corrected += sep + parts[j+1]
			}
			p.errorAt(pos, "module paths are separated by `::`, not `.`; write `%s`", corrected)
			break
		}
	}
	return parts, separators
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

// parseModuleBody reads a module's own body block. A `public:` / `private:` section
// belongs to the module that WROTE it and stops at the module boundary: the section
// marks a nested `module` / `const module` declaration itself, and that nested body
// starts again at its own default. Visibility is relative -- `public` inside a
// private module means "as visible as this module", never more.
func (p *Parser) parseModuleBody(parseBlock func() []ast.Decl) []ast.Decl {
	savedVisibility := p.currentVisibility
	p.currentVisibility = ""
	defer func() { p.currentVisibility = savedVisibility }()
	return parseBlock()
}

func (p *Parser) parseNamespaceDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("module")
	name := p.parseModulePathName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseModuleBody(p.parseDeclBlock)
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true}
}

// parseExtendDecl parses `extend Foo:` — a block that adds members to an
// already-declared `module Foo:`. It shares the module body grammar (including
// `public:`/`private:` sections); the semantic analyzer verifies the target
// module exists and merges the members into its namespace.
func (p *Parser) parseExtendDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("extend")
	name := p.parseModulePathName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseModuleBody(p.parseDeclBlock)
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true, Extend: true}
}
func (p *Parser) parseConstModuleDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CONST)
	p.expectIdentText("module")
	name := p.parseModulePathName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseModuleBody(p.parseConstModuleBlock)
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true, Const: true}
}

// parseConstModuleBlock reads a const module's indented body. Its members are consts,
// and `public:` / `private:` sections mark them exactly as they do in an ordinary
// module block (parseDeclBlock): the mark is recorded per member, and an explicit mark
// beats the enclosing module's default. That is what lets a const module sitting in a
// `private:` section publish some of its constants -- without it, every member was
// private to the const module itself and even the PARENT module could not read them
// (`canAccessPrivateName` grants the owner namespace and its descendants, not ancestors).
// Both section layouts parse, the indented form and the flat `public:`-then-siblings form.
func (p *Parser) parseConstModuleBlock() []ast.Decl {
	p.expect(lexer.TOKEN_INDENT)
	decls := make([]ast.Decl, 0, p.estimateIndentedItemCount())
	savedVisibility := p.currentVisibility
	defer func() { p.currentVisibility = savedVisibility }()
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if (p.peekIdentText("public") || p.peekIdentText("private")) && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
			visibility := p.cur().Text
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_INDENT {
				sectionVisibility := p.currentVisibility
				p.currentVisibility = visibility
				decls = append(decls, p.parseConstModuleBlock()...)
				p.currentVisibility = sectionVisibility
				continue
			}
			p.currentVisibility = visibility
			continue
		}
		member := p.parseConstModuleMemberDecl()
		p.markDeclVisibility(member, "")
		decls = append(decls, member)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decls
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

// parseUsingDecl parses the three `using` forms. A multi-segment qualified name
// (`using Foo::bar`) is selective — the final segment is the member; an `as` suffix
// (`using Foo as F`) makes a module-qualifier alias. A bare single name is wildcard.
func (p *Parser) parseUsingDecl() *ast.UsingDecl {
	pos := p.cur().Pos
	p.expectIdentText("using")
	// A `using` path walks modules (its last segment may be a member): `::` only.
	namePos := p.cur().Pos
	segments := []string{p.expect(lexer.TOKEN_IDENT).Text}
	dotted := false
	for {
		if p.peek() == lexer.TOKEN_DOT {
			dotted = true
		} else if p.peek() != lexer.TOKEN_SCOPE {
			break
		}
		p.advance()
		segments = append(segments, p.expect(lexer.TOKEN_IDENT).Text)
	}
	if dotted {
		p.errorAt(namePos, "module paths are separated by `::`, not `.`; write `%s`", strings.Join(segments, "::"))
	}
	if p.match(lexer.TOKEN_AS) {
		alias := p.expect(lexer.TOKEN_IDENT).Text
		p.expectNewline()
		return &ast.UsingDecl{Position: pos, Name: strings.Join(segments, "."), Alias: alias}
	}
	p.expectNewline()
	if len(segments) > 1 {
		member := segments[len(segments)-1]
		module := strings.Join(segments[:len(segments)-1], ".")
		return &ast.UsingDecl{Position: pos, Name: module, Member: member}
	}
	return &ast.UsingDecl{Position: pos, Name: segments[0]}
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
		parent = p.parseQualifiedDeclName()
	}
	layout, layoutSet, sparse, indexWidth := p.parseEnumLayoutSuffix()
	storeRegion := ""
	if p.peek() == lexer.TOKEN_AT {
		p.advance()
		storeRegion = p.expect(lexer.TOKEN_IDENT).Text
	}
	p.expect(lexer.TOKEN_COLON)
	// An abstract root that only gathers sub-categories may use the inline empty body `enum Node: pass`
	// (docs/77) instead of an indented block.
	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Layout: layout, LayoutSet: layoutSet, LayoutSparse: sparse, IndexWidth: indexWidth, Parent: parent, StoreRegion: storeRegion}
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
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "common" && p.hasToken(p.pos+1) {
			switch p.tokenAt(p.pos + 1).Kind {
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

	return &ast.EnumDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, Packed: packed, Common: commonFields, Variants: variants, Layout: layout, LayoutSet: layoutSet, LayoutSparse: sparse, IndexWidth: indexWidth, Parent: parent, StoreRegion: storeRegion}
}

// parseEnumLayoutSuffix parses an optional `layout soa|aos|c|packed` suffix on an enum declaration
// (docs/76), with optional `(sparse)` and/or `(handle: uN)` sub-options, e.g.
//
//	enum Expr layout(soa):
//	enum Expr layout(soa, sparse):
//	enum Small layout(aos, handle: u16):
//	enum Small layout(handle: u16):       # mode omitted — keeps the default layout (docs/82)
//
// `handle:` is the canonical key for the opaque-handle width (docs/82); the legacy docs/76 `index:`
// spelling is removed (hard parser error). `layout` reuses the struct layout grammar (docs/01) — one
// vocabulary across structs and enums. It is layout-only: it carries no region and no usage meaning
// (orthogonality, docs/10).
func (p *Parser) parseEnumLayoutSuffix() (ast.StructLayoutMode, bool, bool, string) {
	if !p.matchIdentText("layout") {
		return ast.StructLayoutDefault, false, false, ""
	}
	var opts layoutClauseOptions
	if p.peek() == lexer.TOKEN_LPAREN {
		// Canonical parenthesized clause: `layout(soa)`, `layout(soa, sparse)`,
		// `layout(aos, handle: u16)`, `layout(handle: u32)` (mode-less keeps the
		// default layout, docs/82).
		opts = p.parseLayoutClauseOptions()
	} else {
		// Removed bare-word spellings `layout soa` / `layout soa(sparse)` — directed
		// error, recover as the canonical clause (same options, no cascade).
		opts.Size = -1
		mode := p.cur()
		if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
			p.errorf("expected `layout(...)` clause, got %s", mode)
		} else {
			p.errorf("bare `layout %s` has been removed; use the parenthesized clause `layout(%s, ...)`", mode.Text, mode.Text)
			p.advance()
			opts.Mode = mode.Text
		}
		if p.peek() == lexer.TOKEN_LPAREN {
			legacy := p.parseLayoutClauseOptions() // the trailing `(sparse)` / `(handle: uN)` options
			opts.Sparse = legacy.Sparse
			opts.HandleWidth = legacy.HandleWidth
		}
	}
	if opts.Guest {
		p.errorf("`guest` is a struct overlay layout; it has no meaning on an enum")
	}
	if opts.Size >= 0 {
		p.errorf("`size: N` is only meaningful with the struct `guest` overlay layout")
	}
	layout := ast.StructLayoutDefault
	switch opts.Mode {
	case "aos":
		layout = ast.StructLayoutAOS
	case "soa":
		layout = ast.StructLayoutSOA
	case "c":
		layout = ast.StructLayoutC
	case "packed":
		layout = ast.StructLayoutPacked
	case "": // mode-less keeps the default layout
	default:
		p.errorf("unsupported enum layout %q; expected `aos`, `soa`, `c`, or `packed`", opts.Mode)
	}
	return layout, true, opts.Sparse, opts.HandleWidth
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
	var payload []ast.EnumPayloadDecl
	if p.match(lexer.TOKEN_LPAREN) {
		payload = make([]ast.EnumPayloadDecl, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
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
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		relationText := p.tokenAt(p.pos).Text
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
		if p.hasToken(colonIndex) && p.tokenAt(colonIndex).Kind == lexer.TOKEN_QUESTION {
			optionalNamed = true
			colonIndex++
		}
		if relation != ast.EnumPayloadRelationNone && p.hasToken(colonIndex) && p.tokenAt(colonIndex).Kind == lexer.TOKEN_COLON {
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
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) {
		optionalNamed := p.tokenAt(p.pos+1).Kind == lexer.TOKEN_QUESTION && p.hasToken(p.pos+2) && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_COLON
		if p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON || optionalNamed {
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
func (p *Parser) peekTreeMemberHeader(keyword string) bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == keyword && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_COLON
}
