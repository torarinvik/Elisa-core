package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §5 — `with NAME = LITERAL` or-alternative distinguishing bindings.
//
// An or-arm whose alternatives share a body but differ in shape can give each
// alternative a distinguishing COMPILE-TIME CONSTANT, so the body is written once and
// reads the constant uniformly:
//
//	Expr.IntLit(magnitude) with negated = false
//	| Expr.Unary(TokenKind.Minus, Expr.IntLit(magnitude), _) with negated = true:
//	    ...uses `magnitude` and `negated`...
//
// It is a PARSER-LEVEL desugar (like `machine`/`when`): the top-level `|` already fans
// an or-arm out into one sibling MatchArm per alternative sharing the body, so a
// `with` clause simply prepends its `NAME = LITERAL` bindings — as ordinary immutable
// locals — to THAT alternative's body copy. No AST, semantic, or backend change: the
// constant is a normal local the body already knows how to read.
//
// `with` binds at the arm-ALTERNATIVE (top) level. A `with` nested inside a payload
// argument's or-group is not yet supported (it would require the backend or-pattern
// matcher to bind the constant deep in the pattern); the same table is expressible by
// lifting the or to the arm level. `with` is a reserved keyword (TOKEN_WITH) with no
// other parser use, so the clause needs no contextual gating.

// matchArmAlternative is one `|`-separated alternative of a match/when arm together with
// the `with` constants (already lowered to prepend-able VarDecl statements) that
// distinguish it from its siblings. withDecls is nil for an alternative with no `with`.
type matchArmAlternative struct {
	pattern   ast.MatchPattern
	withDecls []ast.Stmt
}

// parseTopLevelMatchArmAlternatives parses `PATTERN [with …] (| PATTERN [with …])*`, the
// arm-header alternatives with their optional per-alternative `with` bindings. It is the
// `with`-aware superset of parseTopLevelMatchPatterns used by the two arm sites (statement
// and expression match) that own a body to prepend into.
func (p *Parser) parseTopLevelMatchArmAlternatives() []matchArmAlternative {
	alts := []matchArmAlternative{p.parseMatchArmAlternative()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		alts = append(alts, p.parseMatchArmAlternative())
	}
	return alts
}

func (p *Parser) parseMatchArmAlternative() matchArmAlternative {
	pattern := p.parseMatchPatternNoOr()
	return matchArmAlternative{pattern: pattern, withDecls: p.parseOptionalWithBindings()}
}

// parseOptionalWithBindings parses a trailing `with NAME = LITERAL [, NAME = LITERAL]*`
// clause into immutable, type-inferred VarDecl statements, or returns nil when absent.
// The value grammar is the literal/qualified-member form (parseMatchValuePatternExpr) —
// a `with` constant is a compile-time literal, never a computed expression.
func (p *Parser) parseOptionalWithBindings() []ast.Stmt {
	if p.peek() != lexer.TOKEN_WITH {
		return nil
	}
	p.advance() // with
	var decls []ast.Stmt
	for {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseMatchValuePatternExpr()
		decls = append(decls, &ast.VarDeclStmt{Position: pos, Name: name, Value: value})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return decls
}

// matchArmAlternativeBody returns the arm body an alternative should carry: the shared
// body with the alternative's `with` bindings prepended (a fresh slice so alternatives
// don't share prepended decls), or the shared body unchanged when there are none.
func matchArmAlternativeBody(withDecls, body []ast.Stmt) []ast.Stmt {
	if len(withDecls) == 0 {
		return body
	}
	combined := make([]ast.Stmt, 0, len(withDecls)+len(body))
	combined = append(combined, withDecls...)
	combined = append(combined, body...)
	return combined
}
