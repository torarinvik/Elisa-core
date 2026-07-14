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
	if blockValue, ok := p.parseValueBlockRHS(pos); ok {
		value = blockValue
	} else {
		value = p.parseValueExprAllowTuple()
	}
	p.expectNewlineAfterValueExpr(value)

	// docs/120 §3: a bare target that names an argument (or UFCS receiver) root of a
	// direct-call RHS claims that argument's declared lmut thread — `rebind ch, lexer =
	// lexer.advance_char()`. The thread is in-place (the callee's lmut slot was erased,
	// docs/120 §2), so the target has no value slot to bind: drop it from the bind and
	// record the claim on the call for the semantic layer to validate (a claim on a
	// non-threading callee, or an unclaimed declared thread, is a compile error there —
	// which is what makes this syntactic matching sound).
	if call := claimableCall(value); call != nil {
		kept := targets[:0]
		for _, t := range targets {
			if t.typ == nil && callArgRootNames(call)[t.name] {
				call.LmutRebindClaims = append(call.LmutRebindClaims, ast.LmutRebindClaim{Position: t.pos, Name: t.name})
				continue
			}
			kept = append(kept, t)
		}
		targets = kept
		// Every target was a claimed thread: nothing to bind — the call stands alone.
		if len(targets) == 0 {
			return &ast.ExprStmt{Position: pos, Expr: value}
		}
	}

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

// claimableCall unwraps a rebind/arrow RHS to the direct call a thread claim can
// attach to: the call itself, possibly wrapped in parens or a plain `get` unwrap
// (`rebind cond: Ast::Expr, parser = get parser.expr()` — the claim belongs to the
// inner call; `get` only unwraps its optional result). A `get` with a fallback or
// recovery clause is not a plain unwrap and is not seen through.
func claimableCall(e ast.Expr) *ast.CallExpr {
	for {
		switch n := e.(type) {
		case *ast.CallExpr:
			return n
		case *ast.ParenExpr:
			e = n.Inner
		case *ast.GetExpr:
			if n.Fallback != nil || n.Recovery != nil {
				return nil
			}
			e = n.Value
		case *ast.CanExpr:
			// `call() can Effects` — the effect grant wraps the call; the thread claim
			// belongs to the inner call. Erasure re-emits the whole CanExpr (grant kept).
			e = n.Expr
		default:
			return nil
		}
	}
}

// callArgRootNames collects the root identifier names of a call's arguments and its
// UFCS receiver (`lexer.advance_char()` — the receiver is the Field object, which the
// semantic UFCS rewrite later prepends to the argument list). Used to spot rebind
// targets that claim a threaded lmut argument.
func callArgRootNames(call *ast.CallExpr) map[string]bool {
	roots := map[string]bool{}
	add := func(e ast.Expr) {
		for {
			switch n := e.(type) {
			case *ast.Ident:
				roots[n.Name] = true
				return
			case *ast.ParenExpr:
				e = n.Inner
			case *ast.AddrOfExpr:
				e = n.Operand
			default:
				return
			}
		}
	}
	for _, arg := range call.Args {
		add(arg)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok {
		add(field.Object)
	}
	return roots
}

// freshRebindName mints a collision-free temp name for one rebind tuple slot.
func (p *Parser) freshRebindName(pos lexer.Pos, slot int) string {
	p.rebindCounter++
	return "__rb_" + itoa(p.rebindCounter) + "_" + itoa(slot)
}

// multiPlaceArrowAhead reports whether the comma at the current position is a
// multi-place-assign separator: a top-level `<-` appears later on this line. A bare
// `expr, expr` line with no arrow is a value block's tuple tail and must not be
// consumed. Pure token scan (no expression parsing, no side effects); brackets nest,
// and the scan stops at the first top-level newline/indent boundary.
func (p *Parser) multiPlaceArrowAhead() bool {
	depth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			depth--
		case lexer.TOKEN_LARROW:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_INDENT, lexer.TOKEN_DEDENT:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

// docs/120 — multi-place mutation: `place1, place2 <- <tuple expr>`.
//
// The sibling of `rebind` for PLACES (field paths / index targets) rather than bindings:
// several places are updated from one tuple-valued RHS, so a conditional update reads as
// pure values flowing into exactly one visible mutation point:
//
//	lexer.line, lexer.column <-
//	    if ch == '\n':
//	        lexer.line + 1, 1
//	    else:
//	        lexer.line, lexer.column + 1
//
// Desugar (pure parser, mirrors parseRebindStmt): the whole RHS binds to fresh temps via
// the §2 temp-tuple bind, then each `place <- temp` assignment rides pendingStmts so it
// lands flat after the bind. Evaluating the RHS fully before any place is written gives
// simultaneous-assignment semantics (a swap `a.x, a.y <- a.y, a.x` is correct), and every
// existing `<-` check (mutability, invariants, named-state transitions, E4) applies to
// the individual assignments unchanged.
//
// The first place has already been parsed by the caller (it is the expression before the
// first comma); this consumes the remaining places, the `<-`, and the RHS.
func (p *Parser) parseMultiPlaceAssignStmt(pos lexer.Pos, first ast.Expr) ast.Stmt {
	places := []ast.Expr{first}
	for p.match(lexer.TOKEN_COMMA) {
		places = append(places, p.parseExpr())
	}
	p.expect(lexer.TOKEN_LARROW)

	var value ast.Expr
	if blockValue, ok := p.parseValueBlockRHS(pos); ok {
		value = blockValue
	} else {
		// docs/125 §6b: like single-place `x <- v if cond`, the multi-place RHS admits a
		// postfix statement guard — `a, b <- call() if cond` is do-or-skip.
		p.stmtGuardArmed = true
		value = p.parseValueExprAllowTuple()
		p.stmtGuardArmed = false
	}
	p.expectNewlineAfterValueExpr(value)

	// The desugars below ride per-place assignments on pendingStmts (they land flat AFTER
	// the returned statement). A postfix guard must scope the WHOLE form — temp bind and
	// every place-write — so capture the newly appended pending statements into the
	// guard's then-block instead of letting them land unconditionally.
	pendingBase := len(p.pendingStmts)

	// docs/120 §6: thread slots. A slot whose expression is the target binding itself, or a
	// mutating call on it, is a THREAD — its effects execute in place and the slot erases.
	stmt, handled := p.desugarThreadSlots(pos, places, value)
	if !handled {
		stmt = p.buildPureMultiPlaceAssign(pos, places, value)
	}
	return p.guardPendingStmts(stmt, pendingBase)
}

// guardPendingStmts finishes a statement whose desugar may ride per-place writes on
// pendingStmts: with no armed guard it returns the statement unchanged (pending writes
// land flat after it, as always); with a captured postfix guard (docs/125 §6b) it moves
// the newly appended pending statements INTO the guard's then-block so the whole form —
// bind and every place-write — is do-or-skip together.
func (p *Parser) guardPendingStmts(stmt ast.Stmt, pendingBase int) ast.Stmt {
	if p.stmtGuardCond == nil {
		return stmt
	}
	then := append([]ast.Stmt{stmt}, p.pendingStmts[pendingBase:]...)
	p.pendingStmts = p.pendingStmts[:pendingBase]
	cond := p.stmtGuardCond
	p.stmtGuardCond = nil
	return &ast.IfStmt{Position: stmt.Pos(), Cond: cond, Then: then}
}

// buildPureMultiPlaceAssign is the docs/120 §1 desugar for value-only slots: the whole
// RHS binds to fresh temps (fully evaluated before any write — simultaneous assignment),
// then one `place <- temp` per target rides pendingStmts.
func (p *Parser) buildPureMultiPlaceAssign(pos lexer.Pos, places []ast.Expr, value ast.Expr) ast.Stmt {
	if len(places) == 1 {
		return &ast.AssignStmt{Position: places[0].Pos(), Target: places[0], Value: value}
	}
	tempNames := make([]ast.TupleBindName, len(places))
	for i, place := range places {
		nm := p.freshRebindName(pos, i)
		tempNames[i] = ast.TupleBindName{Position: place.Pos(), Name: nm}
		p.pendingStmts = append(p.pendingStmts, &ast.AssignStmt{
			Position: place.Pos(),
			Target:   place,
			Value:    &ast.Ident{Position: place.Pos(), Name: nm},
		})
	}
	return &ast.TupleBindStmt{Position: pos, Names: tempNames, Declare: true, Value: value}
}
