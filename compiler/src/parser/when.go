package parser

import (
	"fmt"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §4 — `when`: an order-independent decision table.
//
// Choosing `when` over `match` DECLARES the arms order-independent, and the parser
// enforces that declaration. It is a RESTRICTION construct:
//
//   R1 — overlapping arms are an error (in ordered `match`, shadowing is the contract;
//        here it means the table you wrote is not the table that runs). The full-row
//        `_` default is exempt (it is defined as "everything not covered above"), but
//        a second `_` row is an error.
//   R2 — incompleteness is an error: `when` desugars to `match`, whose existing
//        exhaustiveness machinery demands totality (enum coverage; open scalars and
//        strings require the `_` row).
//   R3 — arm forms are restricted so disjointness stays DECIDABLE at parse time:
//        literals, ranges, payload-less enum tags, `_` per column, or-groups (`|`),
//        and tuples thereof. No bindings, no destructuring, no guards, no pinned
//        (computed) values. Need any of those? That is `match`.
//
// Grammar:
//
//   when SCRUT[, SCRUT]*:
//       PAT[|PAT]*[, PAT[|PAT]*]* -> VALUE-or-BLOCK
//       _ -> VALUE-or-BLOCK
//
// Note the column grammar differs from match's top-level arms: `|` binds WITHIN a
// column and `,` separates columns, so `"u8" | "u16", true` is one two-column row
// (u8-or-u16 × true), not a fan-out.
//
// Because the checked disjointness makes arm order irrelevant, the desugar may (and
// does) move the `_` row to the end of the generated `match`, so a mid-table default
// cannot swallow later arms. Everything else — bindings, typing, exhaustiveness,
// codegen, the docs/119 block-value tail conversion — is inherited from `match`
// unchanged; like `machine` (docs/123), `when` is entirely a parser-level construct.
//
// `when` is a CONTEXTUAL keyword (it stays a legal identifier, and remains free for
// the existing derive-state/catch/grammar uses): the construct is recognized only
// when `when` is directly followed by a token that begins a scrutinee expression —
// no legal expression continues a bare `when` variable with an identifier or literal.

type whenAtomKind int

const (
	whenAtomWild whenAtomKind = iota
	whenAtomInt
	whenAtomChar
	whenAtomBool
	whenAtomFloat
	whenAtomString
	whenAtomTag
	whenAtomRange
	// whenAtomOpaque is an atom the parser could not evaluate to a comparable value.
	// Overlap involving an opaque atom is NOT reported (a missed overlap is a sound
	// miss; a wrong overlap report would be a false positive, which is worse).
	whenAtomOpaque
)

// whenAtom is one or-option of one column, reduced to a comparable value plus the
// original match pattern the desugar reuses.
type whenAtom struct {
	pos    lexer.Pos
	kind   whenAtomKind
	ival   int64 // int/char value; range lo
	hi     int64 // range hi (normalized inclusive)
	text   string
	render string
	pat    ast.MatchPattern
}

type whenRow struct {
	pos      lexer.Pos
	cols     [][]whenAtom
	fullWild bool // the bare `_ ->` default row (or a row of only wildcards)
	body     []ast.Stmt
}

// looksLikeWhenConstruct gates the contextual keyword: `when` begins the construct
// only when the next token can begin a scrutinee expression in a position where a
// variable named `when` could not continue (identifier, literal, or unary minus).
// Postfix/operator continuations (`when(x)`, `when[i]`, `when.f`, `when <- v`) keep
// their identifier meaning.
func (p *Parser) looksLikeWhenConstruct() bool {
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT, lexer.TOKEN_FLOAT_LIT,
		lexer.TOKEN_CHAR_LIT, lexer.TOKEN_STRING_LIT, lexer.TOKEN_TRUE, lexer.TOKEN_FALSE,
		lexer.TOKEN_MINUS:
		return true
	}
	return false
}

func (p *Parser) parseWhenStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // when
	value, ncols := p.parseWhenHead()
	arms := p.parseWhenArms(ncols, false)
	return &ast.MatchStmt{Position: pos, Value: value, Arms: arms}
}

func (p *Parser) parseWhenExpr() ast.Expr {
	pos := p.cur().Pos
	p.advance() // when
	value, ncols := p.parseWhenHead()
	arms := p.parseWhenArms(ncols, true)
	return &ast.MatchExpr{Position: pos, Value: value, Arms: arms}
}

// parseWhenHead parses the comma-separated scrutinee list (mirrors parseMatchHeadExpr)
// and reports the column count the arm rows must satisfy.
func (p *Parser) parseWhenHead() (ast.Expr, int) {
	first := p.withInMembershipDisabled(p.parseExpr)
	if p.peek() != lexer.TOKEN_COMMA {
		return first, 1
	}
	elems := []ast.Expr{first}
	for p.match(lexer.TOKEN_COMMA) {
		elems = append(elems, p.withInMembershipDisabled(p.parseExpr))
	}
	return &ast.TupleExpr{Position: first.Pos(), Elems: elems}, len(elems)
}

func (p *Parser) parseWhenArms(ncols int, exprForm bool) []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var rows []whenRow
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		rows = append(rows, p.parseWhenRow(ncols, exprForm))
	}
	p.expect(lexer.TOKEN_DEDENT)

	if len(rows) == 0 {
		p.errorf("`when` needs at least one arm")
	}
	p.checkWhenDisjoint(rows)
	return desugarWhenRows(rows)
}

func (p *Parser) parseWhenRow(ncols int, exprForm bool) whenRow {
	row := whenRow{pos: p.cur().Pos}

	// The bare `_ ->` default row matches the whole scrutinee (tuple included).
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" &&
		p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ARROW {
		p.advance()
		row.fullWild = true
	} else {
		for {
			col := []whenAtom{p.parseWhenAtom()}
			for p.match(lexer.TOKEN_PIPE) {
				col = append(col, p.parseWhenAtom())
			}
			row.cols = append(row.cols, col)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		if len(row.cols) != ncols {
			p.errorAt(row.pos, "`when` arm has %d column(s) but the scrutinee has %d", len(row.cols), ncols)
		}
		row.fullWild = whenRowAllWild(row.cols)
	}

	if p.peek() == lexer.TOKEN_IF {
		p.errorf("`when` arms are literal/range/tag patterns; computed guards need `match`")
		p.advance()
		_ = p.parseExpr() // recovery: consume the guard expression
	}
	p.expect(lexer.TOKEN_ARROW)

	if p.peek() == lexer.TOKEN_NEWLINE {
		// Indented block body: reuses the statement-block parser; in expression form the
		// tail is converted to the arm value exactly as a match-expression arm's block is.
		p.expectNewline()
		body := p.parseBlock()
		if exprForm {
			body = p.valueBlockTail(body)
		}
		row.body = body
		return row
	}
	// Inline value arm. A top-level comma builds a TupleExpr (mirrors match-expression
	// arms, so `a, b = when k: ...` tuple-destructures).
	value := p.parseExpr()
	if p.peek() == lexer.TOKEN_COMMA {
		elems := []ast.Expr{value}
		for p.match(lexer.TOKEN_COMMA) {
			elems = append(elems, p.parseExpr())
		}
		value = &ast.TupleExpr{Position: value.Pos(), Elems: elems}
	}
	p.expectNewline()
	row.body = []ast.Stmt{&ast.ExprStmt{Position: value.Pos(), Expr: value}}
	return row
}

func whenRowAllWild(cols [][]whenAtom) bool {
	for _, col := range cols {
		if len(col) != 1 || col[0].kind != whenAtomWild {
			return false
		}
	}
	return len(cols) > 0
}

// parseWhenAtom parses one column option through the shared match-pattern grammar,
// then enforces R3: only the forms whose disjointness is decidable survive.
func (p *Parser) parseWhenAtom() whenAtom {
	pos := p.cur().Pos
	pat := p.parseNestedMatchPattern()
	return p.classifyWhenAtom(pos, pat)
}

func (p *Parser) classifyWhenAtom(pos lexer.Pos, pat ast.MatchPattern) whenAtom {
	atom := whenAtom{pos: pos, kind: whenAtomOpaque, render: "?", pat: pat}
	switch pt := pat.(type) {
	case *ast.MatchWildcardPattern:
		atom.kind = whenAtomWild
		atom.render = "_"
	case *ast.MatchStringLiteralPattern:
		atom.kind = whenAtomString
		atom.text = pt.Value
		atom.render = strconv.Quote(pt.Value)
	case *ast.MatchRangePattern:
		lo, loOK := whenLiteralIntValue(pt.Lo)
		hi, hiOK := whenLiteralIntValue(pt.Hi)
		if loOK && hiOK {
			if !pt.Inclusive {
				hi--
			}
			atom.kind = whenAtomRange
			atom.ival, atom.hi = lo, hi
			bound := "..<"
			shownHi := hi
			if pt.Inclusive {
				bound = "..="
			} else {
				shownHi = hi + 1
			}
			atom.render = fmt.Sprintf("%d%s%d", lo, bound, shownHi)
		}
	case *ast.MatchLiteralPattern:
		if pt.Pinned {
			p.errorAt(pos, "`when` arms are literal/range/tag patterns; a pinned `^` value is computed — use `match`")
			return atom
		}
		p.classifyWhenLiteralValue(&atom, pt.Value)
	case *ast.MatchVariantPattern:
		if len(pt.Args) > 0 || pt.Rest || pt.As != "" {
			p.errorAt(pos, "`when` arms cannot destructure payloads or bind; use `match`")
			return atom
		}
		atom.kind = whenAtomTag
		atom.text = pt.Variant
		if pt.EnumName != "" {
			atom.render = pt.EnumName + "." + pt.Variant
		} else {
			atom.render = "." + pt.Variant
		}
	case *ast.MatchBindPattern:
		p.errorAt(pos, "`when` arms are literal/range/tag patterns; bindings and computed guards need `match`")
	default:
		p.errorAt(pos, "`when` arms are literal/range/tag patterns; use `match` for struct/list/deep patterns")
	}
	return atom
}

// classifyWhenLiteralValue reduces a literal-pattern value expression to a comparable
// atom. A value it cannot evaluate stays opaque (excluded from overlap detection).
func (p *Parser) classifyWhenLiteralValue(atom *whenAtom, value ast.Expr) {
	switch v := whenUnparen(value).(type) {
	case *ast.IntLit:
		if n, ok := whenLiteralIntValue(v); ok {
			atom.kind = whenAtomInt
			atom.ival = n
			atom.render = strconv.FormatInt(n, 10)
		}
	case *ast.UnaryExpr:
		if n, ok := whenLiteralIntValue(v); ok {
			atom.kind = whenAtomInt
			atom.ival = n
			atom.render = strconv.FormatInt(n, 10)
		}
	case *ast.CharLit:
		if r, ok := whenCharValue(v.Value); ok {
			atom.kind = whenAtomChar
			atom.ival = int64(r)
		}
		atom.render = "'" + v.Value + "'"
	case *ast.BoolLit:
		atom.kind = whenAtomBool
		if v.Value {
			atom.ival = 1
			atom.render = "true"
		} else {
			atom.render = "false"
		}
	case *ast.FloatLit:
		atom.kind = whenAtomFloat
		atom.text = v.Value
		atom.render = v.Value
	case *ast.NullLit:
		atom.kind = whenAtomTag
		atom.text = "\x00null"
		atom.render = "null"
	case *ast.FieldExpr:
		// Dotted tag value (`.Red` shorthand or `Enum.Red` via the qualified-member
		// grammar). Both arms must typecheck against the same scrutinee enum, so the
		// final variant segment identifies the tag.
		atom.kind = whenAtomTag
		atom.text = v.Field
		atom.render = whenRenderExpr(value)
	}
}

func whenUnparen(e ast.Expr) ast.Expr {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = paren.Inner
	}
}

// whenLiteralIntValue evaluates an int-or-char literal expression (with optional
// leading minus) to its value.
func whenLiteralIntValue(e ast.Expr) (int64, bool) {
	switch v := whenUnparen(e).(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(v.Value, "_", "")
		base := 10
		if v.IsHex {
			text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
			base = 16
		}
		n, err := strconv.ParseInt(text, base, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case *ast.CharLit:
		if r, ok := whenCharValue(v.Value); ok {
			return int64(r), true
		}
		return 0, false
	case *ast.UnaryExpr:
		if v.Op != lexer.TOKEN_MINUS {
			return 0, false
		}
		n, ok := whenLiteralIntValue(v.Operand)
		if !ok {
			return 0, false
		}
		return -n, true
	}
	return 0, false
}

// whenCharValue decodes a char-literal token body to its rune. Unknown escapes stay
// undecoded (the atom becomes opaque — a sound miss, never a wrong overlap).
func whenCharValue(text string) (rune, bool) {
	runes := []rune(text)
	if len(runes) == 1 {
		return runes[0], true
	}
	if len(runes) == 2 && runes[0] == '\\' {
		switch runes[1] {
		case 'n':
			return '\n', true
		case 't':
			return '\t', true
		case 'r':
			return '\r', true
		case '0':
			return 0, true
		case '\\':
			return '\\', true
		case '\'':
			return '\'', true
		case '"':
			return '"', true
		}
	}
	return 0, false
}

func whenRenderExpr(e ast.Expr) string {
	switch v := whenUnparen(e).(type) {
	case *ast.Ident:
		return v.Name
	case *ast.FieldExpr:
		return whenRenderExpr(v.Object) + "." + v.Field
	}
	return "?"
}

// checkWhenDisjoint enforces R1: every pair of non-default rows must be disjoint —
// some column must be provably non-overlapping. The full-row `_` default is exempt
// against other rows, but a second default row is itself an overlap error.
func (p *Parser) checkWhenDisjoint(rows []whenRow) {
	defaultSeen := -1
	for i, row := range rows {
		if !row.fullWild {
			continue
		}
		if defaultSeen >= 0 {
			p.errorAt(row.pos, "duplicate `_` default row (first at line %d); `when` arms must be disjoint", rows[defaultSeen].pos.Line)
			continue
		}
		defaultSeen = i
	}
	for i := 0; i < len(rows); i++ {
		if rows[i].fullWild {
			continue
		}
		for j := i + 1; j < len(rows); j++ {
			if rows[j].fullWild {
				continue
			}
			if whenRowsOverlap(rows[i], rows[j]) {
				p.errorAt(rows[j].pos, "arm (%s) overlaps arm (%s) at line %d; `when` arms must be disjoint",
					whenRenderRow(rows[j]), whenRenderRow(rows[i]), rows[i].pos.Line)
			}
		}
	}
}

func whenRowsOverlap(a, b whenRow) bool {
	if len(a.cols) != len(b.cols) {
		return false // column-arity error already reported; don't cascade
	}
	for c := range a.cols {
		if !whenColumnsOverlap(a.cols[c], b.cols[c]) {
			return false
		}
	}
	return true
}

func whenColumnsOverlap(a, b []whenAtom) bool {
	for _, x := range a {
		for _, y := range b {
			if whenAtomsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

func whenAtomsOverlap(a, b whenAtom) bool {
	if a.kind == whenAtomOpaque || b.kind == whenAtomOpaque {
		return false // not evaluable: never claim an overlap we cannot prove
	}
	if a.kind == whenAtomWild || b.kind == whenAtomWild {
		return true
	}
	// Ranges compare against int/char scalars and other ranges on the numeric value.
	if a.kind == whenAtomRange || b.kind == whenAtomRange {
		aLo, aHi, aOK := whenAtomInterval(a)
		bLo, bHi, bOK := whenAtomInterval(b)
		return aOK && bOK && aLo <= bHi && bLo <= aHi
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case whenAtomInt, whenAtomChar, whenAtomBool:
		return a.ival == b.ival
	case whenAtomString, whenAtomTag, whenAtomFloat:
		return a.text == b.text
	}
	return false
}

func whenAtomInterval(a whenAtom) (int64, int64, bool) {
	switch a.kind {
	case whenAtomRange:
		return a.ival, a.hi, true
	case whenAtomInt, whenAtomChar:
		return a.ival, a.ival, true
	}
	return 0, 0, false
}

func whenRenderRow(row whenRow) string {
	if row.fullWild {
		return "_"
	}
	cols := make([]string, 0, len(row.cols))
	for _, col := range row.cols {
		opts := make([]string, 0, len(col))
		for _, atom := range col {
			opts = append(opts, atom.render)
		}
		cols = append(cols, strings.Join(opts, " | "))
	}
	return strings.Join(cols, ", ")
}

// desugarWhenRows lowers the checked table to ordinary match arms. Disjointness makes
// order irrelevant, so the `_` default is emitted LAST regardless of where it was
// written — a mid-table default must not swallow later arms under match's first-wins.
func desugarWhenRows(rows []whenRow) []ast.MatchArm {
	arms := make([]ast.MatchArm, 0, len(rows))
	var defaults []ast.MatchArm
	for _, row := range rows {
		if row.fullWild {
			defaults = append(defaults, ast.MatchArm{
				Position: row.pos,
				Pattern:  &ast.MatchWildcardPattern{Position: row.pos},
				Body:     row.body,
			})
			continue
		}
		if len(row.cols) == 1 {
			// Single column: or-options fan out to sibling arms sharing the body,
			// exactly as match's top-level `A | B:` does.
			for _, atom := range row.cols[0] {
				arms = append(arms, ast.MatchArm{Position: row.pos, Pattern: atom.pat, Body: row.body})
			}
			continue
		}
		elems := make([]ast.MatchPattern, 0, len(row.cols))
		for _, col := range row.cols {
			if len(col) == 1 {
				elems = append(elems, col[0].pat)
				continue
			}
			options := make([]ast.MatchPattern, 0, len(col))
			for _, atom := range col {
				options = append(options, atom.pat)
			}
			elems = append(elems, &ast.MatchOrPattern{Position: col[0].pos, Options: options})
		}
		arms = append(arms, ast.MatchArm{
			Position: row.pos,
			Pattern:  &ast.MatchTuplePattern{Position: row.pos, Elems: elems},
			Body:     row.body,
		})
	}
	return append(arms, defaults...)
}
