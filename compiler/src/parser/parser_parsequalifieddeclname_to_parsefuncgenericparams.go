package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
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
// parseExtendDecl parses `extend Foo:` — a block that adds members to an
// already-declared `module Foo:`. It shares the module body grammar (including
// `public:`/`private:` sections); the semantic analyzer verifies the target
// module exists and merges the members into its namespace.
func (p *Parser) parseExtendDecl() *ast.NamespaceDecl {
	pos := p.cur().Pos
	p.expectIdentText("extend")
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	decls := p.parseDeclBlock()
	return &ast.NamespaceDecl{Position: pos, Name: name, Decls: decls, Module: true, Extend: true}
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
// parseUsingDecl parses the three `using` forms. A multi-segment qualified name
// (`using Foo::bar`) is selective — the final segment is the member; an `as` suffix
// (`using Foo as F`) makes a module-qualifier alias. A bare single name is wildcard.
func (p *Parser) parseUsingDecl() *ast.UsingDecl {
	pos := p.cur().Pos
	p.expectIdentText("using")
	segments := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.matchQualifiedNameSeparator() {
		segments = append(segments, p.expect(lexer.TOKEN_IDENT).Text)
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
func (p *Parser) peekTreeMemberHeader(keyword string) bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == keyword && p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
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
// looksLikeLayoutDecl distinguishes a guest-memory overlay `layout Name [size N]:` declaration
// (docs/107) from the struct/enum layout-mode prefix that shares the `layout` keyword
// (`layout soa struct …`, `layout(...) enum …`, docs/01). The overlay form reaches a `:` directly
// (with at most a `size N` between), whereas the layout-mode prefix reaches `struct`/`enum` (or opens
// a `(...)` option list) first. We scan the rest of the logical line: a `:` before any
// `struct`/`enum`/`(` means the overlay form; `struct`/`enum`/`(` first (or a newline) means it is the
// layout-mode prefix and we defer to the struct/enum decl path.
func (p *Parser) looksLikeLayoutDecl() bool {
	if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	for j := p.pos + 1; j < len(p.tokens); j++ {
		switch p.tokens[j].Kind {
		case lexer.TOKEN_COLON:
			return true
		case lexer.TOKEN_STRUCT, lexer.TOKEN_ENUM, lexer.TOKEN_LPAREN, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}

// parseLayoutDecl parses an in-source typed guest-memory layout (docs/107 §(a)):
//
//	layout OrbisProcParam size 80:
//	    0  size:      u64
//	    64 mem_param: u64 requires size >= 72
//
// `size N` is optional (bounds otherwise derive from the fields). Each field line is
// `<offset> <name>: <scalar-type>` with an optional trailing `requires size >= <N>` minimum-size tag.
func (p *Parser) parseLayoutDecl() *ast.LayoutDecl {
	pos := p.cur().Pos
	p.expectIdentText("layout")
	name := p.expect(lexer.TOKEN_IDENT).Text
	decl := &ast.LayoutDecl{Position: pos, Name: name}
	if p.matchIdentText("size") {
		decl.Size = p.parseLayoutInt()
	}
	// The standalone overlay declaration has been replaced by the unified postfix
	// layout clause: an overlay is a struct whose layout is externally fixed.
	// Directed error; recovery keeps parsing the legacy offset-led body below so
	// the fields still land in the LayoutDecl (no cascade).
	if decl.Size > 0 {
		p.errorAt(pos, "standalone `layout %s size %d:` has been removed; use `struct %s layout(guest, size: %d):` with `field: type at OFFSET` members", name, decl.Size, name, decl.Size)
	} else {
		p.errorAt(pos, "standalone `layout %s:` has been removed; use `struct %s layout(guest):` with `field: type at OFFSET` members", name, name)
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		decl.Fields = append(decl.Fields, p.parseLayoutField())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decl
}

// parseLayoutField parses one `<offset> <name>: <scalar-type> [requires size >= <N>]` line.
func (p *Parser) parseLayoutField() ast.LayoutFieldDecl {
	fpos := p.cur().Pos
	offset := p.parseLayoutInt()
	fname := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	ftype := p.expect(lexer.TOKEN_IDENT).Text
	field := ast.LayoutFieldDecl{Position: fpos, Offset: offset, Name: fname, Type: ftype}
	if p.matchIdentText("requires") {
		p.expectIdentText("size")
		p.expect(lexer.TOKEN_GTEQ)
		field.RequiresSizeAtLeast = p.parseLayoutInt()
	}
	p.expectNewline()
	return field
}

// parseLayoutInt parses a non-negative integer literal (decimal or 0x-hex) for layout offsets/sizes.
func (p *Parser) parseLayoutInt() int64 {
	tok := p.advance()
	text := tok.Text
	base := 0
	if tok.Kind == lexer.TOKEN_HEX_LIT {
		base = 16
		text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	} else if tok.Kind != lexer.TOKEN_INT_LIT {
		p.errorAt(tok.Pos, "expected an integer offset/size in layout declaration, got %q", tok.Text)
		return 0
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		p.errorAt(tok.Pos, "invalid integer %q in layout declaration", tok.Text)
		return 0
	}
	return v
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
// layoutClauseOptions is the parsed content of the canonical parenthesized
// `layout(...)` clause — one grammar shared by struct, enum, and guest-overlay
// declarations. Callers validate which options make sense in their context.
type layoutClauseOptions struct {
	Mode        string // "aos" | "soa" | "c" | "packed" | "" (mode-less)
	Sparse      bool   // enum: sparse handle store
	HandleWidth string // enum: `handle: uN` opaque-handle width
	Guest       bool   // struct: typed guest-memory overlay (docs/107)
	Size        int64  // guest overlay total byte size; -1 when absent
}

// parseLayoutClauseOptions parses `( opt {, opt} )` where opt is a layout mode
// word (`aos`/`soa`/`c`/`packed`), `sparse`, `handle: uN`, `guest`, or `size: N`.
func (p *Parser) parseLayoutClauseOptions() layoutClauseOptions {
	opts := layoutClauseOptions{Size: -1}
	p.expect(lexer.TOKEN_LPAREN)
	for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
		opt := p.cur()
		switch {
		case opt.Kind == lexer.TOKEN_PACKED || (opt.Kind == lexer.TOKEN_IDENT && (opt.Text == "aos" || opt.Text == "soa" || opt.Text == "c")):
			if opts.Mode != "" {
				p.errorf("duplicate layout mode %q in `layout(...)` (already %q)", opt.Text, opts.Mode)
			}
			opts.Mode = opt.Text
			p.advance()
		case opt.Kind == lexer.TOKEN_IDENT && opt.Text == "guest":
			opts.Guest = true
			p.advance()
		case opt.Kind == lexer.TOKEN_IDENT && opt.Text == "sparse":
			opts.Sparse = true
			p.advance()
		case opt.Kind == lexer.TOKEN_IDENT && opt.Text == "handle":
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			width := p.expect(lexer.TOKEN_IDENT).Text
			switch width {
			case "u8", "u16", "u32", "u64", "ptr":
				opts.HandleWidth = width
			default:
				p.errorf("layout handle width must be u8, u16, u32, u64, or ptr, got %q", width)
			}
		case opt.Kind == lexer.TOKEN_IDENT && opt.Text == "size":
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			opts.Size = p.parseLayoutInt()
		case opt.Kind == lexer.TOKEN_IDENT && opt.Text == "index":
			p.errorf("layout `(index: uN)` has been removed; use the canonical `(handle: uN)` spelling (docs/82)")
			p.advance()
			p.match(lexer.TOKEN_COLON)
			p.advance()
		default:
			p.errorf("unexpected layout option %s; expected a mode (`aos`/`soa`/`c`/`packed`), `sparse`, `handle: uN`, `guest`, or `size: N`", opt)
			p.advance()
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return opts
}

// parseGuestOverlayBody parses the member block of a typed guest-memory overlay
// `struct Name layout(guest, size: N):` — `field: type at OFFSET [requires size >= N]`
// lines (name-first, like every other struct member) — into the LayoutDecl the
// docs/107 overlay checker consumes. Size -1 means "derive from the fields".
func (p *Parser) parseGuestOverlayBody(name string, size int64, pos lexer.Pos) *ast.LayoutDecl {
	decl := &ast.LayoutDecl{Position: pos, Name: name}
	if size >= 0 {
		decl.Size = size
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		fpos := p.cur().Pos
		fname := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		ftype := p.expect(lexer.TOKEN_IDENT).Text
		field := ast.LayoutFieldDecl{Position: fpos, Name: fname, Type: ftype, Offset: -1}
		if p.matchIdentText("at") {
			field.Offset = p.parseLayoutInt()
		} else {
			p.errorf("guest overlay field %q needs an explicit offset: `%s: %s at OFFSET`", fname, fname, ftype)
		}
		if p.matchIdentText("requires") {
			p.expectIdentText("size")
			p.expect(lexer.TOKEN_GTEQ)
			field.RequiresSizeAtLeast = p.parseLayoutInt()
		}
		p.expectNewline()
		decl.Fields = append(decl.Fields, field)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decl
}

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
	var invariants []ast.Expr
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "derive" {
			derivedStates = append(derivedStates, p.parseDerivedStateBlock()...)
			continue
		}
		// `invariant <bool-expr>` field-contract over the struct's fields (referencing self.field).
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "invariant" && p.looksLikeContractStmt() {
			p.advance()
			invariants = append(invariants, p.parseExpr())
			p.expectNewline()
			continue
		}
		fields = append(fields, p.parseFieldDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.StructDecl{Position: pos, Annotations: append([]ast.Annotation(nil), annotations...), Name: name, TypeParams: typeParams, RegionParams: regionParams, RegionOwner: regionOwner, GenericParams: genericParams, HasStateParam: hasStateParam, StateParamCount: stateParamCount, NamedStateCases: append([]string(nil), namedStateCases...), DerivedStates: derivedStates, Affine: affine, Droppable: droppable, ReprC: reprC, Layout: layout, Fields: fields, Invariants: invariants}
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
	// `ghost name: T` — a verification-only model field. The `ghost` keyword is a leading IDENT
	// modifier; it is only consumed when followed by another identifier (the field name), so a
	// field literally named `ghost` (`ghost: T`) still parses as an ordinary field.
	ghost := false
	if p.peekIdentText("ghost") && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
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
