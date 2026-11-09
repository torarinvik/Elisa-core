package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseTypeAliasDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	// Optional `[param, ...]` for parametric refinement aliases: `type Name[cap] = base is Law[..cap..]`.
	// parseFuncGenericParams already peeks for `[` itself, so call it unconditionally.
	typeParams, _, _, _ := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_ASSIGN)
	target := p.parseTypeExpr()
	p.expectNewline()
	return &ast.TypeAliasDecl{Position: pos, Name: name, TypeParams: typeParams, Target: target}
}

// parseLayoutStructDeclWithAnnotations handles the REMOVED prefix form
// `layout soa struct Name:` — the layout clause is a postfix header clause now
// (`struct Name layout(soa):`, one grammar across struct/enum/overlay). Directed
// error, recover-as-canonical (same mode, member block parses, no cascade).
func (p *Parser) parseLayoutStructDeclWithAnnotations(annotations []ast.Annotation) ast.Decl {
	pos := p.cur().Pos
	p.expectIdentText("layout")
	mode := p.cur()
	if mode.Kind != lexer.TOKEN_IDENT && mode.Kind != lexer.TOKEN_PACKED {
		p.errorf("expected layout mode before struct, got %s", mode)
		return p.parseStructDeclWithLeadingLayout(annotations, ast.StructLayoutDefault, false, pos)
	}
	p.advance()
	p.errorf("`layout %s struct Name:` has been removed; use the postfix clause `struct Name layout(%s):`", mode.Text, mode.Text)
	layout := ast.StructLayoutDefault
	reprC := false
	switch mode.Text {
	case "aos":
		layout = ast.StructLayoutAOS
	case "soa":
		layout = ast.StructLayoutSOA
	case "c":
		layout = ast.StructLayoutC
		reprC = true
	case "packed":
		layout = ast.StructLayoutPacked
	default:
		p.errorf("unsupported layout-prefixed struct mode %q; expected `aos`, `soa`, `c`, or `packed`", mode.Text)
	}
	return p.parseStructDeclWithLeadingLayout(annotations, layout, reprC, pos)
}
func (p *Parser) parseAnnotations() []ast.Annotation {
	annotations := make([]ast.Annotation, 0, 1)
	for p.peek() == lexer.TOKEN_AT {
		p.advance() // @
		// Bracketed list form `@[a, b(args), ...]` — a comma-separated group of annotations that
		// desugars to the same flat list as stacking them one per `@` line. Self-delimiting, so it
		// composes cleanly with arg-bearing entries. Commas may be followed by newlines (multiline list).
		if p.match(lexer.TOKEN_LBRACKET) {
			p.skipNewlines()
			for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
				annotations = append(annotations, p.parseAnnotationEntry())
				if p.match(lexer.TOKEN_COMMA) {
					p.skipNewlines()
					continue
				}
				break
			}
			p.skipNewlines()
			p.expect(lexer.TOKEN_RBRACKET)
		} else {
			annotations = append(annotations, p.parseAnnotationEntry())
		}
		p.skipNewlines()
	}
	return annotations
}

// parseAnnotationEntry parses a single annotation `name` with an optional `(args)` suffix, shared by
// the per-`@` form and the bracketed `@[...]` list form.
func (p *Parser) parseAnnotationEntry() ast.Annotation {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	var args []string
	if p.match(lexer.TOKEN_LPAREN) {
		for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
			args = append(args, p.parseAnnotationArg())
			if p.match(lexer.TOKEN_COMMA) {
				continue
			}
			if !annotationArgCanStart(p.peek()) {
				break
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return ast.Annotation{Position: pos, Name: name, Args: args}
}
func (p *Parser) parseFuncAnnotations() []ast.Annotation {
	return p.parseAnnotations()
}
func annotationArgCanStart(kind lexer.TokenKind) bool {
	switch kind {
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT, lexer.TOKEN_STRING_LIT, lexer.TOKEN_IDENT:
		return true
	default:
		return false
	}
}
func (p *Parser) parseAnnotationArg() string {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		if suffix := tok.SuffixString(); suffix != "" {
			return tok.Text + suffix
		}
		return tok.Text
	case lexer.TOKEN_STRING_LIT:
		return p.advance().Text
	case lexer.TOKEN_IDENT:
		// handled below
	default:
		p.errorf("expected annotation argument, got %s", p.cur())
		return p.advance().Text
	}
	root := p.expect(lexer.TOKEN_IDENT).Text
	var b strings.Builder
	b.WriteString(root)
	for {
		switch p.peek() {
		case lexer.TOKEN_DOT:
			p.advance()
			b.WriteByte('.')
			b.WriteString(p.expect(lexer.TOKEN_IDENT).Text)
		case lexer.TOKEN_LBRACKET:
			p.advance()
			b.WriteByte('[')
			switch p.peek() {
			case lexer.TOKEN_STAR:
				p.advance()
				b.WriteByte('*')
			case lexer.TOKEN_INT_LIT:
				b.WriteString(p.advance().Text)
			default:
				p.errorf("expected * or integer index in annotation path, got %s", p.cur())
			}
			p.expect(lexer.TOKEN_RBRACKET)
			b.WriteByte(']')
		default:
			return b.String()
		}
	}
}
func (p *Parser) parsePackedEnumDecl() *ast.EnumDecl {
	return p.parsePackedEnumDeclWithAnnotations(nil)
}
func (p *Parser) parsePackedEnumDeclWithAnnotations(annotations []ast.Annotation) *ast.EnumDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PACKED)
	p.expect(lexer.TOKEN_ENUM)
	return p.parseEnumDeclRest(pos, true, annotations)
}
func (p *Parser) parseErrorDecl() *ast.ErrorDecl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_ERROR)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var tags []ast.ErrorVariantDecl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		tagTok := p.expect(lexer.TOKEN_IDENT)
		var payload []ast.ParamDecl
		if p.match(lexer.TOKEN_LPAREN) {
			for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
				payload = append(payload, p.parseErrorPayloadDecl())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		tags = append(tags, ast.ErrorVariantDecl{Position: tagTok.Pos, Name: tagTok.Text, Payload: payload})
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.ErrorDecl{Position: pos, Name: name, Tags: tags}
}

// parseErrorPayloadDecl parses one error-variant payload field. `name: T` is a
// named field; a bare `T` (e.g. `LexerError(LexError)`) is positional — error
// payloads are matched by position, so the name is optional.
func (p *Parser) parseErrorPayloadDecl() ast.ParamDecl {
	pos := p.cur().Pos
	i := p.pos
	if p.hasToken(i) && p.tokenAt(i).Kind == lexer.TOKEN_MUTABLE {
		i++
	}
	if p.hasToken(i+1) && p.tokenAt(i).Kind == lexer.TOKEN_IDENT && p.tokenAt(i+1).Kind == lexer.TOKEN_COLON {
		return p.parseParam(false)
	}
	return ast.ParamDecl{Position: pos, Type: p.parseTypeExpr()}
}
func (p *Parser) parseAliasDecl() *ast.AliasDecl {
	pos := p.cur().Pos
	// `alias Name = cap1, cap2, …` declares a capability set, referenced via `can`.
	// (The AST node is still named AliasDecl for historical reasons.)
	p.expectIdentText("alias")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	refs := p.parsePermissionRefs(false)
	p.expectNewline()
	return &ast.AliasDecl{Position: pos, Name: name, Refs: refs}
}
func (p *Parser) parsePermissionDecl() *ast.PermissionDecl {
	pos := p.cur().Pos
	p.expectIdentText("permission")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	if p.peek() == lexer.TOKEN_PASS {
		p.advance()
		p.expectNewline()
		return &ast.PermissionDecl{Position: pos, Name: name}
	}
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	members := make([]string, 0, p.estimateIndentedItemCount())
	var includes []string
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_PASS {
			p.advance()
			p.expectNewline()
			continue
		}
		// `includes Disk, FileSystem` — families this permission subsumes.
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "includes" {
			p.advance()
			for {
				includes = append(includes, p.expect(lexer.TOKEN_IDENT).Text)
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
			p.expectNewline()
			continue
		}
		members = append(members, p.expect(lexer.TOKEN_IDENT).Text)
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.PermissionDecl{Position: pos, Name: name, Members: members, Includes: includes}
}
func (p *Parser) parseParamDeclBlock(allowDefaults bool) []ast.ParamDecl {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	params := make([]ast.ParamDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		params = append(params, p.parseParam(allowDefaults))
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return params
}
