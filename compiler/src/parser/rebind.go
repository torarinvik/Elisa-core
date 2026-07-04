package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/119 §5 — `rebind`: explicit mutation threading (the primitive).
//
//	rebind TARGET (, TARGET)* =
//	    <block whose tail is a tuple matching the target list>
//
// A TARGET is either a bare `name` (an existing `mutable` binding, moved into the
// block and re-moved back out on exit) or `name: T` (a fresh binding declared from
// the corresponding tuple element). The bare-vs-typed spelling is what makes
// rebind-vs-fresh lexically unconfusable (rule 2): a bare name MUST resolve to an
// existing mutable — reassigning an undefined/immutable name is the existing E-class
// error, which is exactly docs/119's E8. Typed targets are ordinary declarations.
//
// Desugar (pure parser, onto already-tested nodes) reuses the §2 bare-block tuple
// bind: the whole yielded tuple is destructured into fresh temps, then each target
// is either reassigned (`existing <- __rbN`) or declared (`fresh: T = __rbN`). The
// temp-tuple bind is returned; the per-target statements ride pendingStmts so they
// land flat in the enclosing block (the fresh bindings stay visible afterwards —
// a scoped wrapper would wrongly hide them).
//
// The guaranteed-move (never-clone) property falls out of the affine reassignment
// path: `existing <- __rbN` moves the fresh value in and the old value is dead
// across the block by construction.

type rebindTarget struct {
	pos     lexer.Pos
	name    string
	typ     ast.TypeExpr // non-nil ⇒ fresh binding (declare); nil ⇒ existing target (reassign)
	tempVar string       // fresh temp holding this slot of the yielded tuple
}

// looksLikeRebindStmt distinguishes the `rebind` keyword from an ordinary variable
// that happens to be named `rebind`. The keyword form is `rebind <ident>` where the
// ident begins a target list; a variable named `rebind` would be `rebind =`,
// `rebind <-`, `rebind:`, `rebind.` … — i.e. an operator directly after the name.
func (p *Parser) looksLikeRebindStmt() bool {
	return p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
}

func (p *Parser) parseRebindStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // consume `rebind`

	targets := make([]rebindTarget, 0, 4)
	for {
		nameTok := p.expect(lexer.TOKEN_IDENT)
		t := rebindTarget{pos: nameTok.Pos, name: nameTok.Text}
		if p.match(lexer.TOKEN_COLON) {
			t.typ = p.parseTypeExpr()
		}
		targets = append(targets, t)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_ASSIGN)

	var value ast.Expr
	if p.bareExprBlockAhead() {
		value = p.parseBareExprBlockValue(pos)
	} else {
		value = p.parseValueExprAllowTuple()
	}
	p.expectNewlineAfterValueExpr(value)

	// Single target: the block yields a scalar (not a 1-tuple), so bind it directly
	// — no destructuring temp needed.
	if len(targets) == 1 {
		return p.rebindTargetStmt(targets[0], value)
	}

	// Fresh temp names per slot, keyed by position so they never collide.
	tempNames := make([]ast.TupleBindName, len(targets))
	for i := range targets {
		nm := p.freshRebindName(pos, i)
		targets[i].tempVar = nm
		tempNames[i] = ast.TupleBindName{Position: targets[i].pos, Name: nm}
	}

	// Per-target reassign/decl statements ride pendingStmts (drained flat after the
	// temp-tuple bind, preserving order).
	for _, t := range targets {
		p.pendingStmts = append(p.pendingStmts, p.rebindTargetStmt(t, &ast.Ident{Position: t.pos, Name: t.tempVar}))
	}

	return &ast.TupleBindStmt{Position: pos, Names: tempNames, Declare: true, Value: value}
}

// rebindTargetStmt builds the statement that lands a rebind slot into its target:
// a reassignment for an existing mutable (bare name; E8 = the existing
// reassign-undefined/immutable diagnostic — moves the value in), or a fresh typed
// declaration for a `name: T` target.
func (p *Parser) rebindTargetStmt(t rebindTarget, value ast.Expr) ast.Stmt {
	if t.typ == nil {
		return &ast.AssignStmt{
			Position: t.pos,
			Target:   &ast.Ident{Position: t.pos, Name: t.name},
			Value:    value,
		}
	}
	return &ast.VarDeclStmt{
		Position: t.pos,
		Name:     t.name,
		Type:     t.typ,
		Value:    value,
	}
}

// freshRebindName mints a collision-free temp name for one rebind tuple slot.
func (p *Parser) freshRebindName(pos lexer.Pos, slot int) string {
	p.rebindCounter++
	return "__rb_" + itoa(p.rebindCounter) + "_" + itoa(slot)
}
