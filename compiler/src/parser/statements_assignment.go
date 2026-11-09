package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

// parseExprOrAssignStmt: handles expressions, assignments, var decls, discards
func (p *Parser) parseExprOrAssignStmt() ast.Stmt {
	pos := p.cur().Pos

	// Discard: _ = expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ASSIGN {
		p.advance() // _
		p.advance() // =
		value := p.parseValueExprAllowTuple()
		p.expectNewline()
		return &ast.DiscardStmt{Position: pos, Value: value}
	}

	if tupleStmt := p.tryParseTupleBindStmt(pos); tupleStmt != nil {
		return tupleStmt
	}

	// An explicit local binding qualifier is independent of the declared type:
	// `mutable p: T&` makes the local pointer slot rebindable, while `const p:
	// mutable T&` keeps the slot fixed but permits writes through the reference.
	// Keep the historical `name: mutable T` spelling unchanged below.
	bindingExplicit := false
	bindingMutable := false
	if (p.peek() == lexer.TOKEN_MUTABLE || p.peek() == lexer.TOKEN_CONST) && p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_COLON {
		bindingMutable = p.peek() == lexer.TOKEN_MUTABLE
		p.advance()
		pos = p.cur().Pos
		bindingExplicit = true
	}

	// Variable declaration: name: [mutable] Type [= value]
	// But NOT name:mutable (no space) which would be field:Type
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
		colonPos := p.pos + 1
		afterColon := lexer.TOKEN_EOF
		if p.hasToken(colonPos + 1) {
			afterColon = p.tokenAt(colonPos + 1).Kind
		}
		// TOKEN_LPAREN opens a tuple type (`t: (a: i64, b: i64) = …`), which
		// parseTypeExpr already handles and which the parameter and return
		// positions already accept — only this statement-level lookahead was
		// blind to it, so a tuple-annotated local fell through to expression
		// parsing and died on the colon.
		if afterColon == lexer.TOKEN_IDENT || afterColon == lexer.TOKEN_MUTABLE || afterColon == lexer.TOKEN_TAIL ||
			afterColon == lexer.TOKEN_HEAP || afterColon == lexer.TOKEN_STACK || afterColon == lexer.TOKEN_STATIC ||
			afterColon == lexer.TOKEN_LPAREN {
			name := p.cur().Text
			p.advance()
			p.advance()

			mutable := bindingMutable
			if !bindingExplicit && p.match(lexer.TOKEN_MUTABLE) {
				mutable = true
			}

			typ := p.parseTypeExpr()

			var value ast.Expr
			var owner ast.Expr
			if p.peek() == lexer.TOKEN_IN {
				p.advance()
				owner = p.parseExpr()
				value = &ast.CallExpr{
					Position: owner.Pos(),
					Func: &ast.FieldExpr{
						Position: owner.Pos(),
						Object:   owner,
						Field:    "builder",
					},
				}
				mutable = true
			}
			if p.match(lexer.TOKEN_ASSIGN) {
				if owner != nil {
					p.errorf("builder owner declarations cannot also use an initializer")
				}
				if blockValue, ok := p.parseValueBlockRHS(pos); ok {
					// docs/119 §2/§6: `x: T = [|caps|]` NEWLINE INDENT ... — (captured) block expression.
					value = blockValue
				} else {
					value = p.parseValueExprAllowTuple()
				}
			}
			p.expectNewlineAfterValueExpr(value)
			value = desugarDStrStringLiteralInit(typ, value)
			return &ast.VarDeclStmt{Position: pos, Name: name, Mutable: mutable, BindingExplicit: bindingExplicit, Type: typ, Value: value, Owner: owner}
		}
	}

	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ASSIGN {
		name := p.cur().Text
		p.advance()
		p.advance()
		var value ast.Expr
		if blockValue, ok := p.parseValueBlockRHS(pos); ok {
			// docs/119 §2/§6: `x = [|caps|]` NEWLINE INDENT ... — (captured) block expression.
			value = blockValue
		} else {
			value = p.parseValueExprAllowTuple()
		}
		p.expectNewlineAfterValueExpr(value)
		return &ast.VarDeclStmt{Position: pos, Name: name, Value: value}
	}

	var expr ast.Expr
	p.stmtGuardArmed = true
	if p.peekIdentText("move") {
		expr = p.withInMembershipDisabled(p.parseExpr)
	} else {
		expr = p.parseExpr()
	}
	p.stmtGuardArmed = false
	if p.stmtGuardCond != nil {
		// docs/125 §6b: `EXPR if COND` — a guarded expression statement. The guard was
		// recorded at a statement terminator, so no assignment/postfix continuation can
		// follow; wrap and finish.
		p.expectNewlineAfterValueExpr(expr)
		return p.takeStmtGuard(&ast.ExprStmt{Position: pos, Expr: expr})
	}

	if _, ok := expr.(*ast.MoveExpr); ok {
		var store ast.Expr
		if p.peek() == lexer.TOKEN_IN {
			p.advance()
			store = p.parseExpr()
		}
		if p.peek() == lexer.TOKEN_AS {
			p.advance()
			pattern := p.parseMoveBindPattern()
			p.expectNewline()
			return &ast.MoveBindStmt{Position: pos, Value: expr, Store: store, Pattern: pattern}
		}
		if store != nil {
			p.errorf("move binding with in-store requires an as pattern")
			p.expectNewline()
			return &ast.ExprStmt{Position: pos, Expr: expr}
		}
		// A bare `move x` with no `in`/`as` and nothing after it discards the moved
		// value: it compiles to NOTHING. That is almost never what was written --
		// `move` is a contextual keyword, so a call to a function actually named
		// `move` parses as this operator applied to the parenthesised argument list
		// and vanishes without a word. stage1 already refuses to build such a unit;
		// stage0 used to emit a no-op, so the two compilers disagreed and the
		// program silently did nothing.
		if store == nil && (p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_EOF) {
			p.errorf("a bare `move` statement discards the moved value and compiles to nothing; use `move x in store as pattern`, or -- if this is a call to a function named `move` -- qualify it (`Module::move(...)`) or rename it, because `move` in call position is the ownership operator")
			p.expectNewline()
			return &ast.ExprStmt{Position: pos, Expr: expr}
		}
	}

	// A catch expression in statement position is always a complete statement: its
	// arm block terminates it, and postfix forms (`<- x`, `return if`, `as <-`) do
	// not apply. Return immediately so a following statement is not mis-parsed as a
	// postfix continuation of the catch.
	if _, ok := expr.(*ast.CatchExpr); ok {
		p.expectNewlineAfterValueExpr(expr)
		return &ast.ExprStmt{Position: pos, Expr: expr}
	}

	if p.peek() == lexer.TOKEN_RETURN {
		returnPos := p.cur().Pos
		p.advance()
		if !p.match(lexer.TOKEN_IF) {
			p.errorAt(returnPos, "postfix return guard expects `if`")
			p.expectNewlineAfterValueExpr(expr)
			return &ast.ReturnStmt{Position: returnPos, Value: expr}
		}
		p.noticeAt(returnPos, "prefer `return value if condition` to `value return if condition`")
		cond := p.parseGuardCondition()
		p.expectNewlineAfterValueExpr(cond)
		return &ast.IfStmt{
			Position: pos,
			Cond:     cond,
			Then:     []ast.Stmt{&ast.ReturnStmt{Position: returnPos, Value: expr}},
		}
	}

	if p.matchIdentText("then") {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
		return &ast.IfStmt{Position: pos, Cond: expr, Then: body}
	}

	switch p.peek() {
	case lexer.TOKEN_COMMA:
		// docs/120: multi-place mutation `place1, place2 <- <tuple expr>` — several places
		// updated from one tuple-valued RHS (typically a value-if), so the branches stay
		// pure values and mutation happens at exactly one visible `<-`. Desugars like
		// `rebind`: the RHS binds to fresh temps via the §2 temp-tuple bind, then one
		// `place <- temp` per target rides pendingStmts.
		//
		// Guarded by a token scan: the comma is a place separator ONLY when a top-level
		// `<-` follows on this line. A bare `expr, expr` (a value block's tuple TAIL —
		// exactly what rebind/if-value branches yield) must fall through untouched.
		if p.multiPlaceArrowAhead() {
			return p.parseMultiPlaceAssignStmt(pos, expr)
		}

	case lexer.TOKEN_LARROW:
		p.advance()
		var value ast.Expr
		if blockValue, ok := p.parseValueBlockRHS(pos); ok {
			// docs/119 §2/§6: `x <- [|caps|]` NEWLINE INDENT ... — (captured) block expression.
			value = blockValue
		} else {
			// docs/125 §6b: the RHS admits a postfix statement guard — `x <- v if cond`
			// is do-or-skip assignment (a ternary still spells its `else`).
			p.stmtGuardArmed = true
			value = p.parseValueExprAllowTuple()
			p.stmtGuardArmed = false
		}
		p.expectNewlineAfterValueExpr(value)
		// docs/120 §6 single-place: a branchy RHS whose every leaf threads the target
		// (a mutating call rooted at it) or yields it unchanged erases to a statement-if.
		if stmt, handled := desugarStateThreadBlock(expr, value); handled {
			return p.takeStmtGuard(stmt)
		}
		if stmt, handled := p.desugarSingleThreadAssign(expr, value); handled {
			return p.takeStmtGuard(stmt)
		}
		return p.takeStmtGuard(&ast.AssignStmt{Position: pos, Target: expr, Value: value})

	case lexer.TOKEN_WITH:
		return p.parseWithFieldsStmt(pos, expr)

	case lexer.TOKEN_QASSIGN:
		p.advance()
		value := p.parseValueExprAllowTuple()
		p.expectNewlineAfterValueExpr(value)
		return &ast.AssignStmt{Position: pos, Target: expr, Value: value, Optional: true}

	case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ, lexer.TOKEN_STAREQ, lexer.TOKEN_SLASHEQ, lexer.TOKEN_PERCENTEQ,
		lexer.TOKEN_CARETEQ, lexer.TOKEN_PIPEEQ, lexer.TOKEN_AMPEQ,
		lexer.TOKEN_LSHIFTEQ, lexer.TOKEN_RSHIFTEQ:
		op := p.advance()
		// docs/125 §6b: compound assignment admits a postfix statement guard too
		// (`total += i if keep(i)`).
		p.stmtGuardArmed = true
		value := p.parseExpr()
		p.stmtGuardArmed = false
		p.expectNewlineAfterValueExpr(value)
		return p.takeStmtGuard(&ast.AugAssignStmt{Position: pos, Op: op.Kind, Target: expr, Value: value})

	case lexer.TOKEN_AS:
		p.advance()
		var asKind string
		if p.match(lexer.TOKEN_AMPERSAND) {
			asKind = "&"
		} else if p.match(lexer.TOKEN_BANG) {
			asKind = "!"
		}
		p.expect(lexer.TOKEN_LARROW)
		// docs/125 §6b: the reinterpret-assign RHS admits a postfix statement guard too —
		// `x as & <- v if cond` is do-or-skip (a ternary still spells its `else`).
		p.stmtGuardArmed = true
		value := p.parseValueExprAllowTuple()
		p.stmtGuardArmed = false
		p.expectNewlineAfterValueExpr(value)
		return p.takeStmtGuard(&ast.AsRefAssignStmt{Position: pos, Target: expr, AsKind: asKind, Value: value})
	}

	// A bare comma-list is a tuple expression, including in function tails.
	// Tail normalization gives it the same return semantics as a scalar.
	if p.peek() == lexer.TOKEN_COMMA {
		comma := p.cur()
		commaIndex := p.pos
		errorsBefore, noticesBefore := len(p.errors), len(p.notices)
		tuple := p.parseTupleExprFromFirst(expr.Pos(), expr)
		if p.exprBlockDepth == 0 && p.peek() != lexer.TOKEN_EOF && !(p.peek() == lexer.TOKEN_NEWLINE && p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == lexer.TOKEN_DEDENT || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_EOF)) {
			// The tuple parse is abandoned and the tokens after the comma are parsed again, so
			// drop what it reported: kept, every rewind re-reported each later comma, making a
			// run of n commas cost O(n^2) duplicate diagnostics (4000 commas: 2M lines, ~40 s).
			p.errors, p.notices = p.errors[:errorsBefore], p.notices[:noticesBefore]
			p.errorAt(comma.Pos, "unexpected token , in expression")
			p.seek(commaIndex + 1)
			p.expectNewline()
			return &ast.ExprStmt{Position: pos, Expr: expr}
		}
		p.expectNewlineAfterValueExpr(tuple)
		return &ast.ExprStmt{Position: pos, Expr: tuple}
	}

	if ident, ok := expr.(*ast.Ident); ok && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		arg := p.parseExpr()
		p.expectNewlineAfterValueExpr(arg)
		return &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
			Position: pos,
			Func:     ident,
			Args:     []ast.Expr{arg},
		}}
	}

	p.expectNewlineAfterValueExpr(expr)
	return &ast.ExprStmt{Position: pos, Expr: expr}
}
func (p *Parser) parseSingleExprBlockValue() ast.Expr {
	pos := p.cur().Pos
	return p.parseExprBlockValue(pos, true)
}

// parseWithFieldsStmt parses the multi-field place update
//
//	place with Type{
//	    field <- value
//	    ...
//	}
//
// and desugars it, parser-level like `machine` and the match `with`, into a hidden
// reference to the place followed by one ordinary assignment per field:
//
//	if true:
//	    __with_N: mutable Type& = &place     (the type is optional; omitted, it is inferred)
//	    __with_N.field <- value
//	    ...
//
// The place is evaluated ONCE, the fields are assigned in the order written, and the
// `if true:` block scopes the hidden binding. Nothing new reaches the analyzer or the
// backend, so the feature is identical in both compilers by construction.
func (p *Parser) parseWithFieldsStmt(pos lexer.Pos, place ast.Expr) ast.Stmt {
	p.expect(lexer.TOKEN_WITH)
	var typeName string
	if p.peek() == lexer.TOKEN_IDENT {
		typeName = p.advance().Text
	}
	p.expect(lexer.TOKEN_LBRACE)
	// A bare identifier is already a place that costs nothing to re-evaluate,
	// and taking its address would turn a reference parameter into a
	// reference to a reference; assign through it directly. Any other place
	// (an index, a call) is bound ONCE to a hidden reference. The typed form
	// always binds, since the binding is what checks the type.
	var target ast.Expr
	var body []ast.Stmt
	if ident, isIdent := place.(*ast.Ident); isIdent && typeName == "" {
		target = ident
	} else {
		tempName := fmt.Sprintf("__with_%d_%d", pos.Line, pos.Col)
		temp := &ast.Ident{Position: pos, Name: tempName}
		decl := &ast.VarDeclStmt{Position: pos, Name: tempName, Mutable: false, Value: &ast.AddrOfExpr{Position: pos, Operand: place}}
		if typeName != "" {
			decl.Type = &ast.MutableType{Position: pos, Elem: &ast.RefType{Position: pos, Elem: &ast.NamedType{Position: pos, Name: typeName}}}
		}
		target = temp
		body = []ast.Stmt{decl}
	}
	for {
		for p.peek() == lexer.TOKEN_NEWLINE || p.peek() == lexer.TOKEN_INDENT || p.peek() == lexer.TOKEN_DEDENT {
			p.advance()
		}
		if p.peek() == lexer.TOKEN_RBRACE {
			p.advance()
			break
		}
		if p.peek() == lexer.TOKEN_EOF {
			p.errorf("unterminated `with` block: expected `}`")
			break
		}
		fieldTok := p.expect(lexer.TOKEN_IDENT)
		var value ast.Expr
		op := p.peek()
		if op == lexer.TOKEN_LARROW || op == lexer.TOKEN_PLUSEQ {
			p.advance()
			// Not the tuple form: a comma ends the field, it does not extend
			// the value (`{ kind <- 7, x <- 1.5 }`).
			value = p.parseExpr()
		} else {
			// Field punning: `with { kind, parent }` assigns each field from the
			// local of the same name.
			value = &ast.Ident{Position: fieldTok.Pos, Name: fieldTok.Text}
		}
		field := &ast.FieldExpr{Position: fieldTok.Pos, Object: target, Field: fieldTok.Text}
		if op == lexer.TOKEN_PLUSEQ {
			body = append(body, &ast.AugAssignStmt{Position: fieldTok.Pos, Target: field, Op: op, Value: value})
		} else {
			body = append(body, &ast.AssignStmt{Position: fieldTok.Pos, Target: field, Value: value})
		}
		if p.peek() == lexer.TOKEN_COMMA {
			p.advance()
		}
	}
	p.expectNewline()
	if ident, direct := place.(*ast.Ident); direct && typeName == "" && p.exprBlockDepth > 0 {
		return &ast.ExprStmt{Position: pos, Expr: &ast.ExprBlock{Position: pos, Stmts: body, Value: place, Captures: []string{ident.Name}}}
	}
	return &ast.IfStmt{Position: pos, Cond: &ast.BoolLit{Position: pos, Value: true}, Then: body}
}
