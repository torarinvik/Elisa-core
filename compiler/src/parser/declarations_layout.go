package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

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
	if !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	for j := p.pos + 1; p.hasToken(j); j++ {
		switch p.tokenAt(j).Kind {
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
		text = strings.ReplaceAll(text, "_", "")
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
	members := p.parseConstEnumMemberBlock()
	return &ast.ConstEnumDecl{Position: pos, Name: name, Storage: storage, Members: members}
}

// parseConstEnumMemberBlock parses the `:` NEWLINE INDENT members DEDENT block shared by
// `const enum` and `extern enum` (docs/127 D9).
func (p *Parser) parseConstEnumMemberBlock() []ast.ConstEnumMemberDecl {
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
	return members
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

// parseGlobalRegionDecl parses `global region NAME` (docs/128).
func (p *Parser) parseGlobalRegionDecl() *ast.GlobalRegionDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_GLOBAL)
	p.advance() // the contextual `region`
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.GlobalRegionDecl{Position: pos, Name: name}
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
