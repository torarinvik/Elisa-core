package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/125 §5 — `state`/`start` local-state sugar over `machine from`.
//
// A `machine from` needs a pre-declared enum for its states, which forces a top-level enum
// that exists only to name the states of one function. This sugar lets a function declare its
// states inline:
//
//	def count_blocks(dh: i32) -> i32:
//	    state Scan
//	    state Grow(first: i32, last: i32)
//	    state Emit(first: i32, last: i32)
//	    return start Scan |r: i32 = 0, blocks: i32 = 0| decreases 3 * (dh - r) + 2:
//	        Scan:
//	            next Grow(r - 1, r - 1) if changed
//	            next Scan
//	        Grow(first, last):
//	            ...
//	        Emit(first, last):
//	            next Scan
//
// The `state` statements accumulate on the parser; the `start State …` expression synthesizes
// an enum from them (hoisted to file scope, like `machine over`'s mode enum), then delegates to
// the identical `machine from` tail parser with the synthesized enum as the state type. So it
// reuses ALL of `machine from`: payloads, the header pipe, `decreases`, and every R2/R3/R4/R5
// refusal — zero new analysis or codegen.

// looksLikeStateDeclStmt gates the statement form `state Name` / `state Name(field: T, …)`.
// `state` stays a contextual keyword: a state decl is `state` immediately followed by an
// identifier (the state name). A variable named `state` is used as `state = …`, `state.x`,
// `state(…)`, or `state: T` — none of which put a bare identifier directly after `state`.
func (p *Parser) looksLikeStateDeclStmt() bool {
	return p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "state" &&
		p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
}

// parseStateDeclStmt consumes a `state` declaration into pendingStateDecls and produces no
// statement (nil) — the declaration is a parser directive that materializes as a synthesized
// enum when a `start` consumes it. parseBlock skips nil statements.
func (p *Parser) parseStateDeclStmt() ast.Stmt {
	p.pendingStateDecls = append(p.pendingStateDecls, p.parseMachineStateDecl())
	return nil
}

// looksLikeStartExpr gates the expression form `start State …`. Like `state`, `start` stays
// contextual: it is the machine keyword only when directly followed by an identifier (the
// entry state). A variable named `start` appears as `start + 1`, `start(…)`, `start.x`,
// `start[…]` — never `start <IDENT>`.
func (p *Parser) looksLikeStartExpr() bool {
	return p.cur().Text == "start" &&
		p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
}

// parseStartExpr lowers `start State …:` <arms> to `machine from <synth>.State …:` <arms>.
// It consumes the accumulated `state` declarations, synthesizes an enum from them (hoisted to
// file scope with a program-unique name), and hands off to the shared `machine from` tail
// parser. The synthesized enum's variants carry the states' payload fields, so payload states
// (`state Bar(baz: usize)`) flow through the existing machine-from payload machinery unchanged.
func (p *Parser) parseStartExpr() ast.Expr {
	pos := p.cur().Pos
	p.advance() // start
	startState := p.expect(lexer.TOKEN_IDENT).Text

	states := p.pendingStateDecls
	p.pendingStateDecls = nil // consumed: a later `start` in the same function re-declares
	if len(states) == 0 {
		p.errorf("`start %s` has no `state` declarations to run over — declare the states first (`state %s`, …) (docs/125 §5)", startState, startState)
	}

	enumName := "__StartStates_" + machineNameSuffix(pos)
	variants := make([]ast.EnumVariantDecl, 0, len(states))
	for _, st := range states {
		payload := make([]ast.EnumPayloadDecl, 0, len(st.fields))
		for _, f := range st.fields {
			payload = append(payload, ast.EnumPayloadDecl{Position: f.pos, Name: f.name, Type: f.typ})
		}
		variants = append(variants, ast.EnumVariantDecl{Position: st.pos, Name: st.name, Payload: payload})
	}
	p.pendingDecls = append(p.pendingDecls, &ast.EnumDecl{Position: pos, Name: enumName, Variants: variants})

	machine := p.parseMachineFromTail(pos, enumName, startState)
	if mf, ok := machine.(*ast.MachineFromExpr); ok {
		mf.FromStartSugar = true
	}
	return machine
}
