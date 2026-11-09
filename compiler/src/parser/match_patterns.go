package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseMatchArms() []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.MatchArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMatchArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}
func (p *Parser) parseMatchArm() []ast.MatchArm {
	pos := p.cur().Pos
	// A leading `case` is a common habit from other languages; Elisa match arms are
	// the pattern itself. Diagnose it once and skip the keyword so the arm (pattern,
	// colon, body) still parses normally instead of cascading recovery errors.
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "case" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind != lexer.TOKEN_COLON {
		p.errorAt(pos, "match arms do not use a `case` keyword; write the pattern directly (e.g. `E.A(v: v):`)")
		p.advance()
	}
	alternatives := p.parseTopLevelMatchArmAlternatives()
	guard := p.parseOptionalMatchArmGuard()
	p.expect(lexer.TOKEN_COLON)
	// docs/119 §1.6/§4.2: one `match`. A statement arm takes the same colon body as an
	// `if`/`else` branch — an indented block or a single statement on the arm line
	// (`Expr.Ident(n, line): name <- n`). Requiring the block rejected the spec's own
	// example, and made a block-tail `match` (§4) reject the inline arms a `x = match`
	// accepts.
	body := p.parseStmtBodyAfterColon()
	arms := make([]ast.MatchArm, 0, len(alternatives))
	for _, alt := range alternatives {
		arms = append(arms, ast.MatchArm{Position: pos, Pattern: alt.pattern, Body: matchArmAlternativeBody(alt.withDecls, body), Guard: guard})
	}
	return arms
}

// parseOptionalMatchArmGuard parses the docs/122 §5.1 arm-header guard `Pattern if cond:`.
// The guard expression ends at the arm's `:`; pattern bindings are in scope inside it
// (checked by the analyzer). Fanned-out `A | B if cond:` alternatives share the guard.
// Guards are read-only dispatch: a guard that itself introduces `is`-bindings is
// refused (docs/122 §5.8 stays a body `if … is name:` for now).
func (p *Parser) parseOptionalMatchArmGuard() ast.Expr {
	if p.peek() != lexer.TOKEN_IF {
		return nil
	}
	p.advance()
	guard := p.parseExpr()
	if guardConditionIntroducesBindings(guard) {
		p.errorf("match arm guards cannot introduce bindings; bind in the arm body with `if value is name:` (docs/122 §5.8)")
	}
	return guard
}
func (p *Parser) parseTopLevelMatchPatterns() []ast.MatchPattern {
	patterns := []ast.MatchPattern{p.parseMatchPatternNoPipe()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		patterns = append(patterns, p.parseMatchPatternNoPipe())
	}
	return patterns
}
func (p *Parser) parseMatchPattern() ast.MatchPattern {
	return p.parseNestedOrMatchPattern()
}
func (p *Parser) parseNestedOrMatchPattern() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	if p.peek() != lexer.TOKEN_PIPE && p.peek() != lexer.TOKEN_OR {
		return pattern
	}
	options := []ast.MatchPattern{pattern}
	for p.peek() == lexer.TOKEN_PIPE || p.peek() == lexer.TOKEN_OR {
		p.advance()
		options = append(options, p.parseNestedMatchPattern())
	}
	orPattern := &ast.MatchOrPattern{Position: pattern.Pos(), Options: options}
	return orPattern
}

// parseMatchPatternNoPipe parses one top-level arm while leaving `|` available
// to parseTopLevelMatchArmAlternatives, which fans pipe-separated arms out into
// sibling arms. The word `or` is an in-pattern alternative and must therefore be
// consumed here, just as it is for nested payload patterns.
func (p *Parser) parseMatchPatternNoPipe() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	if p.peek() == lexer.TOKEN_OR {
		options := []ast.MatchPattern{pattern}
		for p.match(lexer.TOKEN_OR) {
			options = append(options, p.parseNestedMatchPattern())
		}
		pattern = &ast.MatchOrPattern{Position: pattern.Pos(), Options: options}
	}
	// docs/77 §2 category arm with binder: `Statement s:` — two bare identifiers. Only the
	// top-level arm position accepts the binder; nested payload patterns stay single-ident.
	if bind, ok := pattern.(*ast.MatchBindPattern); ok && bind != nil && bind.Binder == "" &&
		p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "_" {
		bind.Binder = p.advance().Text
	}
	if p.peek() == lexer.TOKEN_COMMA {
		elems := []ast.MatchPattern{pattern}
		for p.match(lexer.TOKEN_COMMA) {
			elems = append(elems, p.parseNestedOrMatchPattern())
		}
		pattern = &ast.MatchTuplePattern{Position: pattern.Pos(), Elems: elems}
	}
	switch pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern, *ast.MatchRangePattern, *ast.MatchBindPattern, *ast.MatchVariantPattern, *ast.MatchStructPattern, *ast.MatchTuplePattern, *ast.MatchListPattern, *ast.MatchOrPattern:
		return pattern
	default:
		p.errorf("top-level match arm must use Enum.Variant(...), Struct(...), a literal, a range, a binding, a tuple pattern, a list pattern, or _")
		return pattern
	}
}
func (p *Parser) parseNestedMatchPattern() ast.MatchPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_DOT {
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseMatchValuePatternExpr()}
	}
	if p.peek() == lexer.TOKEN_CARET {
		p.advance()
		// A pin names an existing value; parse it as a unary operand so `|` keeps its
		// pattern meaning (`^A | ^B:` is two alternatives, not `^(A | ^B)`).
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseUnary(), Pinned: true}
	}
	if p.peek() == lexer.TOKEN_STRING_LIT {
		return &ast.MatchStringLiteralPattern{Position: pos, Value: p.advance().Text}
	}
	if p.peek() == lexer.TOKEN_LBRACKET {
		return p.parseMatchListPattern(pos)
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMatchStructPatternAfterName(pos, "")
	}
	if p.peek() == lexer.TOKEN_INT_LIT || p.peek() == lexer.TOKEN_FLOAT_LIT || p.peek() == lexer.TOKEN_HEX_LIT ||
		p.peek() == lexer.TOKEN_CHAR_LIT || p.peek() == lexer.TOKEN_TRUE || p.peek() == lexer.TOKEN_FALSE ||
		p.peek() == lexer.TOKEN_NULL || p.peek() == lexer.TOKEN_MINUS || p.peek() == lexer.TOKEN_LPAREN {
		value := p.parseMatchValuePatternExpr()
		// docs/122 §5.2 range pattern: `LO..<HI` / `LO..=HI` over int/char literals. The
		// spelling matches loop ranges; a bare `..` is diagnosed toward the explicit forms.
		switch p.peek() {
		case lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_LE:
			inclusive := p.advance().Kind == lexer.TOKEN_RANGE_LE
			return &ast.MatchRangePattern{Position: pos, Lo: value, Hi: p.parseMatchValuePatternExpr(), Inclusive: inclusive}
		case lexer.TOKEN_RANGE:
			p.errorf("range pattern needs an explicit bound spelling: use `..<` (exclusive) or `..=` (inclusive)")
			p.advance()
			return &ast.MatchRangePattern{Position: pos, Lo: value, Hi: p.parseMatchValuePatternExpr(), Inclusive: true}
		}
		return &ast.MatchLiteralPattern{Position: pos, Value: value}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		return &ast.MatchWildcardPattern{Position: pos}
	}
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	if p.peek() == lexer.TOKEN_LPAREN {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	// A `::` or `.` after the first segment introduces a qualified variant path:
	// `Color.Red`, `M::Color.Red` (module-qualified). `::` namespaces the module
	// while `.` selects the variant; both join into the canonical dotted name. A
	// `.` anywhere but last (`M.Color.Red`) is a mis-spelled module path and is rejected.
	if p.peek() != lexer.TOKEN_DOT && p.peek() != lexer.TOKEN_SCOPE {
		return &ast.MatchBindPattern{Position: pos, Name: parts[0]}
	}
	// A qualified path followed by `{` is a STRUCT pattern -- no enum-variant spelling is
	// braced -- so every separator in it walks a module and must be `::`. Reported with
	// the same sentence and span the `let` destructure position already uses for the
	// identical spelling. The head ident is already consumed, so the chain is finished
	// here rather than through parseQualifiedDeclName, which starts at the head.
	if p.peekQualifiedPathTailBefore(lexer.TOKEN_LBRACE) {
		name := parts[0]
		dotted := false
		for p.peek() == lexer.TOKEN_DOT || p.peek() == lexer.TOKEN_SCOPE {
			dotted = dotted || p.peek() == lexer.TOKEN_DOT
			p.advance()
			name += "." + p.expect(lexer.TOKEN_IDENT).Text
		}
		if dotted {
			p.errorAt(pos, "module paths are separated by `::`, not `.`; write `%s`", ast.ModulePathSpelling(name))
		}
		return p.parseMatchStructPatternAfterName(pos, name)
	}
	parts, separators := p.parseVariantPathNameWithSeparators(pos, parts[0])
	// A path walked ONLY by `::` names a TYPE, never a variant (a variant is always
	// reached by a final `.`), so `Pack::Item(v: v)` is the parenthesized spelling of the
	// same module-qualified struct pattern.
	if !containsSeparator(separators, ".") && p.peek() == lexer.TOKEN_LPAREN {
		return p.parseMatchStructPatternAfterName(pos, strings.Join(parts, "."))
	}
	name := strings.Join(parts[:len(parts)-1], ".")
	variant := parts[len(parts)-1]
	var args []ast.MatchPatternArg
	if p.match(lexer.TOKEN_LPAREN) {
		args = make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseMatchPatternArg())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
				if p.peek() == lexer.TOKEN_RPAREN {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	variantPat := &ast.MatchVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
	variantPat.Rest = detachTrailingRestArg(&variantPat.Args)
	variantPat.As = p.parseOptionalPatternAsBinding()
	return variantPat
}

// peekQualifiedStructDestructurePattern's counterpart for a cursor sitting ON the
// separator after an already-consumed head ident: the rest of the `(.|::) IDENT` chain is
// followed by `stop`.
func (p *Parser) peekQualifiedPathTailBefore(stop lexer.TokenKind) bool {
	i := p.pos
	for p.hasToken(i+1) && (p.tokenAt(i).Kind == lexer.TOKEN_DOT || p.tokenAt(i).Kind == lexer.TOKEN_SCOPE) && p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT {
		i += 2
	}
	return i > p.pos && p.hasToken(i) && p.tokenAt(i).Kind == stop
}

func containsSeparator(separators []string, wanted string) bool {
	for _, separator := range separators {
		if separator == wanted {
			return true
		}
	}
	return false
}

// detachTrailingRestArg implements docs/122 §5.7 for named-args patterns: a final bare
// `_` after at least one NAMED arg is the struct-rest marker ("match these fields,
// ignore the remainder"), not a positional one-field wildcard — pure-positional
// patterns keep the existing wildcard-per-field meaning. Returns true (and drops the
// marker arg) when the rest form applies.
func detachTrailingRestArg(args *[]ast.MatchPatternArg) bool {
	list := *args
	if len(list) < 2 {
		return false
	}
	last := list[len(list)-1]
	if last.Name != "" {
		return false
	}
	if _, wild := last.Pattern.(*ast.MatchWildcardPattern); !wild {
		return false
	}
	named := false
	for _, a := range list[:len(list)-1] {
		if a.Name != "" {
			named = true
			break
		}
	}
	if !named {
		return false
	}
	*args = list[:len(list)-1]
	return true
}

// parseOptionalPatternAsBinding parses the docs/122 §5.4 as-binding suffix
// (`Pattern as whole`) on a variant/struct pattern: bind the entire matched value at
// the narrowed type while also destructuring it.
func (p *Parser) parseOptionalPatternAsBinding() string {
	if p.peek() != lexer.TOKEN_AS {
		return ""
	}
	p.advance()
	return p.expect(lexer.TOKEN_IDENT).Text
}
func (p *Parser) parseMatchListPattern(pos lexer.Pos) ast.MatchPattern {
	p.expect(lexer.TOKEN_LBRACKET)
	elems := make([]ast.MatchPattern, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	if p.peek() != lexer.TOKEN_RBRACKET {
		for {
			if p.peek() == lexer.TOKEN_ELLIPSIS {
				restPos := p.advance().Pos
				// docs/122 §5.3: `...rest` binds the unmatched tail; bare `...` discards it.
				restName := ""
				if p.peek() == lexer.TOKEN_IDENT && p.cur().Text != "_" {
					restName = p.advance().Text
				}
				elems = append(elems, &ast.MatchRestPattern{Position: restPos, Name: restName})
				if p.peek() != lexer.TOKEN_RBRACKET {
					p.errorf("list pattern rest marker must be the final element")
				}
				break
			}
			elems = append(elems, p.parseNestedOrMatchPattern())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACKET {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.MatchListPattern{Position: pos, Elems: elems}
}
func buildQualifiedMatchValueExpr(pos lexer.Pos, parts []string) ast.Expr {
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: "<error>"}
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for i := 1; i < len(parts); i++ {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: parts[i]}
	}
	return expr
}
func (p *Parser) parseMatchStructPatternAfterName(pos lexer.Pos, typeName string) ast.MatchPattern {
	brace := false
	open := p.peek()
	close := lexer.TOKEN_RPAREN
	switch open {
	case lexer.TOKEN_LPAREN:
		p.advance()
	case lexer.TOKEN_LBRACE:
		p.advance()
		brace = true
		close = lexer.TOKEN_RBRACE
	default:
		p.errorf("struct pattern expects (...) or {...}")
	}
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(close))
	rest := false
	if p.peek() != close {
		for {
			// docs/122 §5.7: a bare `_` arg is the struct-rest marker — match the named
			// fields, ignore the remainder. It must be the final arg. (`count(...)` keeps
			// its positional grammar untouched.)
			if typeName != "count" && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" && p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == close || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COMMA) {
				p.advance()
				rest = true
				if p.peek() != close {
					p.errorf("struct pattern rest marker `_` must be the final field")
				}
				break
			}
			if brace {
				args = append(args, p.parseBraceMatchStructPatternArg())
			} else if typeName == "count" {
				args = append(args, p.parseMatchPatternArg())
			} else {
				args = append(args, p.parseMatchStructPatternArg())
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == close {
				break
			}
		}
	}
	p.expect(close)
	structPat := &ast.MatchStructPattern{Position: pos, TypeName: typeName, Args: args, Brace: brace, Rest: rest}
	structPat.As = p.parseOptionalPatternAsBinding()
	return structPat
}
func (p *Parser) parseMatchStructPatternArg() ast.MatchPatternArg {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_COLON {
		pos := p.cur().Pos
		p.errorf("struct pattern fields must use name: pattern")
		pattern := p.parseNestedOrMatchPattern()
		return ast.MatchPatternArg{Position: pos, Pattern: pattern}
	}
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	pattern := p.parseNestedOrMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}
func (p *Parser) parseBraceMatchStructPatternArg() ast.MatchPatternArg {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COLON) {
		return ast.MatchPatternArg{
			Position: pos,
			Name:     name,
			Pattern:  &ast.MatchBindPattern{Position: pos, Name: name},
		}
	}
	pattern := p.parseNestedOrMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}
