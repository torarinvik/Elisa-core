package parser

import (
	"sort"

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
	pos       lexer.Pos // start of this alternative, for R1 diagnostics
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
	p.checkWithBindingParity(alts)
	return alts
}

func (p *Parser) parseMatchArmAlternative() matchArmAlternative {
	pos := p.cur().Pos
	pattern := p.parseMatchPatternNoOr()
	return matchArmAlternative{pos: pos, pattern: pattern, withDecls: p.parseOptionalWithBindings()}
}

// checkWithBindingParity is docs/125 §5 refusal R1: when an or-arm's alternatives share a
// body via `with` constants, every alternative must bind the IDENTICAL set of names —
// otherwise a body reading a constant that only some alternatives bind resolves for those
// siblings and fails as a late, confusing `undefined identifier` on the others (or leaves a
// dead binding). Reporting the mismatch here, at the point the alternatives are known,
// turns that into one clear error at the arm.
//
// Zero-false-positive: the check is inert unless SOME alternative carries a `with`, so plain
// or-arms (`A(x) | B(_):`, no `with` anywhere) are never touched — payload/pattern captures
// are a separate concern the analyzer already owns. Names are compared as a set (order and
// the literal values are irrelevant to whether the body can name the constant).
func (p *Parser) checkWithBindingParity(alts []matchArmAlternative) {
	if len(alts) < 2 {
		return
	}
	anyWith := false
	for _, alt := range alts {
		if len(alt.withDecls) > 0 {
			anyWith = true
			break
		}
	}
	if !anyWith {
		return
	}
	first := withBindingNameSet(alts[0].withDecls)
	for _, alt := range alts[1:] {
		got := withBindingNameSet(alt.withDecls)
		if missing, extra, ok := diffNameSets(first, got); !ok {
			p.errorAt(alt.pos, "%s", withBindingParityMessage(missing, extra))
		}
	}
}

func withBindingNameSet(decls []ast.Stmt) map[string]bool {
	set := make(map[string]bool, len(decls))
	for _, d := range decls {
		if vd, ok := d.(*ast.VarDeclStmt); ok {
			set[vd.Name] = true
		}
	}
	return set
}

// diffNameSets reports names present in want but not got (missing) and in got but not want
// (extra); ok is true when the sets are equal.
func diffNameSets(want, got map[string]bool) (missing, extra []string, ok bool) {
	for n := range want {
		if !got[n] {
			missing = append(missing, n)
		}
	}
	for n := range got {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra, len(missing) == 0 && len(extra) == 0
}

func withBindingParityMessage(missing, extra []string) string {
	msg := "every alternative of a `with`-arm must bind the same constants, so the shared body can read them uniformly"
	if len(missing) > 0 {
		msg += "; this alternative is missing " + quoteNameList(missing)
	}
	if len(extra) > 0 {
		msg += "; this alternative also binds " + quoteNameList(extra) + " which the first alternative does not"
	}
	return msg
}

func quoteNameList(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += "`" + n + "`"
	}
	return out
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
