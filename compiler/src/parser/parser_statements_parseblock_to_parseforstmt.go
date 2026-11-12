package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseBlock() []ast.Stmt {
	p.enterNesting()
	defer p.leaveNesting()
	p.expect(lexer.TOKEN_INDENT)
	stmts := make([]ast.Stmt, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		stmt := p.parseContextualStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		// Drain any statements a desugaring (e.g. the grouped `ghost:` block) buffered, so they land
		// flat in this block rather than behind a wrapper node.
		if len(p.pendingStmts) > 0 {
			stmts = append(stmts, p.pendingStmts...)
			p.pendingStmts = nil
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return stmts
}
func (p *Parser) parseContextualStmt() ast.Stmt {
	if p.staticFunctionDepth > 0 {
		pos := p.cur().Pos
		switch p.peek() {
		case lexer.TOKEN_ERROR:
			p.advance()
			p.expect(lexer.TOKEN_LPAREN)
			msg := p.parseExpr()
			p.expect(lexer.TOKEN_RPAREN)
			p.expectNewline()
			return &ast.StaticErrorStmt{Position: pos, Message: msg}
		case lexer.TOKEN_IDENT:
			if p.cur().Text == "assert" {
				p.advance()
				if p.match(lexer.TOKEN_COLON) {
					p.expectNewline()
					return &ast.StaticAssertBlockStmt{Position: pos, Assertions: p.parseStaticAssertItemBlock()}
				}
				cond := p.parseExpr()
				var msg ast.Expr
				if p.match(lexer.TOKEN_COMMA) {
					msg = p.parseExpr()
				}
				p.expectNewline()
				return &ast.StaticAssertStmt{Position: pos, Cond: cond, Message: msg}
			}
		}
	}
	return p.parseStmt()
}
func (p *Parser) parseStmt() ast.Stmt {
	p.recoverReservedWordStatementName()
	if p.peek() == lexer.TOKEN_IDENT {
		switch p.cur().Text {
		case "assert":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_QUESTION {
				return p.parseAssertHoleStmt()
			}
			// `assert COND by:` — a Dafny-style proof-carrying assert (the `by:` proof block is
			// verification-only). Only matched when a top-level `by:` opener is present on this logical
			// line; a plain `assert(COND)` call has no `by:` and falls through to expression parsing.
			if p.looksLikeAssertByStmt() {
				return p.parseAssertByStmt()
			}
			// In the spaced statement form, a parenthesized prefix belongs to
			// the condition: `assert (x > 0) == ok` checks the whole comparison.
			// Keep adjacent `assert(...)` as an ordinary expression call.
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LPAREN && p.tokenAt(p.pos+1).Pos.Offset > p.cur().Pos.EndOffset {
				pos := p.cur().Pos
				callee := &ast.Ident{Position: pos, Name: "assert"}
				p.advance()
				p.stmtGuardArmed = true
				condition := p.parseExpr()
				p.stmtGuardArmed = false
				p.expectNewlineAfterValueExpr(condition)
				return p.takeStmtGuard(&ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{Position: pos, Func: callee, Args: []ast.Expr{condition}}})
			}
		case "proof":
			if p.looksLikeProofBlockStmt() {
				return p.parseProofBlockStmt()
			}
		case "use":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseProofUseStmt()
			}
		case "can":
			if p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LBRACKET) {
				return p.parseCanStmt()
			}
		case "trusted":
			if p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LBRACKET) {
				return p.parseTrustedStmt()
			}
		case "signal":
			if p.looksLikeSignalStmt() {
				return p.parseSignalStmt()
			}
		case "wait":
			if p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "all" {
				return p.parseWaitAllStmt()
			}
		case "notify":
			if p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && (p.tokenAt(p.pos+1).Text == "one" || p.tokenAt(p.pos+1).Text == "all") {
				return p.parseNotifyStmt()
			}
		case "machine":
			if p.looksLikeMachineStmt() {
				return p.parseMachineStmt()
			}
		case "when":
			if p.looksLikeWhenConstruct() {
				return p.parseWhenStmt()
			}
		case "pool":
			if p.looksLikePoolStmt() {
				return p.parsePoolStmt()
			}
		case "nursery":
			if p.looksLikeNurseryStmt() {
				return p.parseNurseryStmt()
			}
		case "emit":
			if p.looksLikeSequenceEmitStmt() {
				return p.parseEmitStmt()
			}
		case "guard", "require":
			if p.looksLikeGuardStmt() {
				return p.parseGuardStmt()
			}
		case "requires":
			// `requires <bool-expr>` value-contract precondition (lifted into the decl when leading).
			// Skip if it's actually a variable named `requires` (followed by =/:/<- or end of line).
			if p.looksLikeContractStmt() {
				pos := p.cur().Pos
				p.advance()
				saved := p.allowQuantifiers
				p.allowQuantifiers = true // docs/90 brick 90-6: quantified preconditions
				cond := p.parseExpr()
				p.allowQuantifiers = saved
				proof, scoped := p.parseOptionalContractScopedProof(cond)
				return &ast.ContractStmt{Position: pos, Kind: ast.ContractRequire, Cond: cond, Proof: proof, ScopedProof: scoped}
			}
		case "uses":
			// `uses Name(args)` / `uses Name[T](args)` — apply a named composable contract (docs/97). Lifted into the decl with
			// the other leading contracts. Only when it looks like `uses <Ident>(` ; otherwise a variable
			// named `uses` falls through to expression parsing.
			if p.looksLikeUsesStmt() {
				pos := p.cur().Pos
				p.advance()
				cname := p.expect(lexer.TOKEN_IDENT).Text
				var typeArgs []ast.TypeExpr
				if p.match(lexer.TOKEN_LBRACKET) {
					typeArgs = make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
					for {
						typeArgs = append(typeArgs, p.parseGenericTypeArgExpr())
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
					p.expect(lexer.TOKEN_RBRACKET)
				}
				p.expect(lexer.TOKEN_LPAREN)
				var args []ast.Expr
				if p.peek() != lexer.TOKEN_RPAREN {
					for {
						args = append(args, p.parseExpr())
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
				}
				p.expect(lexer.TOKEN_RPAREN)
				p.expectNewline()
				return &ast.ContractStmt{Position: pos, Kind: ast.ContractUses, UsesName: cname, UsesTypeArgs: typeArgs, UsesArgs: args}
			}
		case "ensure":
			// `ensure <bool-expr>` value-contract postcondition (may use `result`/`old(...)`).
			if p.looksLikeContractStmt() {
				pos := p.cur().Pos
				p.advance()
				saved := p.allowQuantifiers
				p.allowQuantifiers = true // docs/90 brick 90-6: quantified postconditions
				cond := p.parseExpr()
				p.allowQuantifiers = saved
				proof, scoped := p.parseOptionalContractScopedProof(cond)
				return &ast.ContractStmt{Position: pos, Kind: ast.ContractEnsure, Cond: cond, Proof: proof, ScopedProof: scoped}
			}
		case "invariant":
			// `invariant <bool-expr>` in-body assertion, checked in place (debug builds only). Quantifiers
			// are enabled so a loop invariant can range over an array's contents (`invariant forall k:
			// 0<=k<i implies arr[k] == 0`), proven inductively via the SMT array-store model.
			if p.looksLikeContractStmt() {
				pos := p.cur().Pos
				p.advance()
				saved := p.allowQuantifiers
				p.allowQuantifiers = true
				cond := p.parseExpr()
				p.allowQuantifiers = saved
				p.expectNewlineAfterValueExpr(cond)
				return &ast.ContractStmt{Position: pos, Kind: ast.ContractInvariant, Cond: cond}
			}
		case "decreases":
			// `decreases <int-expr>` termination measure (lifted into the decl when leading; docs/86
			// brick 86-7). Also handles `decreases * "reason"` — a Dafny-style wildcard that opts out
			// of the termination proof obligation (see ast.FuncDecl.DecreasesWild).
			// Skip if it's actually a variable named `decreases`.
			if p.looksLikeContractStmt() {
				pos := p.cur().Pos
				p.advance()
				// `decreases *` — wildcard opt-out. The next token must be `*` (TOKEN_STAR).
				if p.peek() == lexer.TOKEN_STAR {
					p.advance() // consume `*`
					var reason string
					if p.peek() == lexer.TOKEN_STRING_LIT {
						reason = p.cur().Text
						p.advance()
					}
					p.expectNewline()
					return &ast.ContractStmt{Position: pos, Kind: ast.ContractDecreasesWild, WildReason: reason}
				}
				measure := p.parseExpr()
				p.expectNewline()
				return &ast.ContractStmt{Position: pos, Kind: ast.ContractDecreases, Cond: measure}
			}
		case "expect":
			if p.looksLikeExpectPatternStmt() {
				return p.parseExpectPatternStmt()
			}
		case "defer":
			if p.looksLikeDeferStmt() {
				return p.parseDeferStmt()
			}
		case "for":
			p.recoverReservedWordLoopBinder(p.pos + 1)
			if p.looksLikeForStmt() {
				return p.parseForStmt()
			}
		case "state":
			// docs/125 §5 local-state sugar: `state Foo` / `state Bar(baz: T)` accumulate on
			// the parser until a `start Foo:` consumes them. Produces no statement (nil).
			if p.looksLikeStateDeclStmt() {
				return p.parseStateDeclStmt()
			}
		case "let":
			if p.looksLikeLetDestructureStmt() {
				return p.parseLetDestructureStmt()
			}
		case "scope":
			if p.looksLikeScopeStmt() {
				return p.parseScopeStmt()
			}
		case "cascade":
			if p.looksLikeScopeStmt() {
				return p.parseCascadeStmt()
			}
		case "parallel":
			if p.looksLikeParallelForStmt() {
				return p.parseParallelForStmt()
			}
		case "rebind":
			if p.looksLikeRebindStmt() {
				return p.parseRebindStmt()
			}
		case "lock":
			if p.looksLikeLockStmt() {
				return p.parseLockStmt()
			}
		case "ghost":
			if p.looksLikeGhostStmt() {
				return p.parseGhostStmt()
			}
		case "region":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseRegion()
			}
		case "destroy":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseDestroy()
			}
		case "leak":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseLeak()
			}
		case "mark":
			if p.hasToken(p.pos+3) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_AS && p.tokenAt(p.pos+3).Kind == lexer.TOKEN_IDENT {
				return p.parseMark()
			}
		case "checkpoint":
			if p.looksLikeCheckpointStmt() {
				return p.parseCheckpointStmt()
			}
		case "restore":
			if p.hasToken(p.pos+3) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Text == "from" && p.tokenAt(p.pos+3).Kind == lexer.TOKEN_IDENT {
				return p.parseRestore()
			}
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseRestoreCheckpointStmt()
			}
		case "reset":
			if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
				return p.parseReset()
			}
		case "promote":
			if p.looksLikePromoteStmt() {
				return p.parsePromote()
			}
		case "adopt":
			if p.hasToken(p.pos+3) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Text == "into" && p.tokenAt(p.pos+3).Kind == lexer.TOKEN_IDENT {
				return p.parseAdopt()
			}
		}
	}
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		return p.parseReturn()
	case lexer.TOKEN_BREAK:
		pos := p.cur().Pos
		p.advance()
		if p.peek() != lexer.TOKEN_IF && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			// docs/125 §6b: the value admits a postfix statement guard, so
			// `break found if item == sought` is `if item == sought: break found`. It is the
			// only spelling of a conditional value-break that survives -Wflow-strict's
			// block-`if` ban; a ternary value still spells its `else`.
			p.stmtGuardArmed = true
			value := p.parseExpr()
			p.stmtGuardArmed = false
			p.expectNewlineAfterValueExpr(value)
			if len(p.loopBreakTargets) == 0 || p.loopBreakTargets[len(p.loopBreakTargets)-1] == "" {
				p.errorAt(pos, "`break value` needs a loop with a simple `-> accumulator` yield")
				return p.takeStmtGuard(&ast.BreakStmt{Position: pos})
			}
			target := &ast.Ident{Position: pos, Name: p.loopBreakTargets[len(p.loopBreakTargets)-1]}
			return p.takeStmtGuard(&ast.IfStmt{Position: pos, Cond: &ast.BoolLit{Position: pos, Value: true}, Then: []ast.Stmt{&ast.AssignStmt{Position: pos, Target: target, Value: value}, &ast.BreakStmt{Position: pos}}})
		}
		// Postfix guard: `break if <cond>` desugars to `if <cond>: break` (mirrors the
		// postfix `return if` form). No new AST node — the loop lowering is unchanged.
		if p.match(lexer.TOKEN_IF) {
			cond := p.parseGuardCondition()
			p.expectNewlineAfterValueExpr(cond)
			return &ast.IfStmt{Position: pos, Cond: cond, Then: []ast.Stmt{&ast.BreakStmt{Position: pos}}}
		}
		p.expectNewline()
		return &ast.BreakStmt{Position: pos}
	case lexer.TOKEN_CONTINUE:
		pos := p.cur().Pos
		p.advance()
		// Postfix guard: `continue if <cond>` desugars to `if <cond>: continue`.
		if p.match(lexer.TOKEN_IF) {
			cond := p.parseGuardCondition()
			p.expectNewlineAfterValueExpr(cond)
			return &ast.IfStmt{Position: pos, Cond: cond, Then: []ast.Stmt{&ast.ContinueStmt{Position: pos}}}
		}
		p.expectNewline()
		return &ast.ContinueStmt{Position: pos}
	case lexer.TOKEN_PASS:
		return p.parsePass()
	case lexer.TOKEN_PANIC:
		return p.parsePanic()
	case lexer.TOKEN_IF:
		return p.parseIf()
	case lexer.TOKEN_MATCH:
		return p.parseMatch()
	case lexer.TOKEN_IN:
		return p.parseInStore()
	case lexer.TOKEN_WITH:
		// `with arena name(...) as owner:` is the only statement-level `with` form
		// (the implicit-args with-scope and `with args(...)` were removed).
		return p.parseWithArenaStmt()
	case lexer.TOKEN_WHILE:
		return p.parseWhile()
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
}

// looksLikeGhostStmt distinguishes the `ghost` verification-var keyword from an ordinary variable
// that happens to be named `ghost`. The keyword forms are:
//   - single decl: `ghost <ident> : ...` or `ghost <ident> = ...`
//   - grouped block: `ghost :` followed by a newline (the indented block of decls)
//
// A real variable named `ghost` would be `ghost = ...`, `ghost <- ...`, or `ghost: Type` used as a
// field-style decl on the same line WITHOUT a following identifier — i.e. `ghost` then COLON then a
// type. We only treat `ghost` COLON as the keyword when a newline follows (the block form); a
// `ghost: i32 = 5` ordinary decl keeps working because COLON is followed by a type ident, not a
// newline. `ghost = ...` (assignment to a var named ghost) never matches.
func (p *Parser) looksLikeGhostStmt() bool {
	if !p.hasToken(p.pos + 1) {
		return false
	}
	next := p.tokenAt(p.pos + 1)
	switch next.Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE:
		// `ghost x ...` / `ghost mutable ...` — but reject `ghost mutable` as a standalone if it is
		// really `ghost` being assigned (cannot happen: MUTABLE can't follow a value name). Accept.
		return true
	case lexer.TOKEN_COLON:
		// Block form only when a newline (then indent) follows the colon.
		if p.hasToken(p.pos + 2) {
			return p.tokenAt(p.pos+2).Kind == lexer.TOKEN_NEWLINE
		}
		return false
	default:
		return false
	}
}

// parseGhostStmt parses a `ghost` verification-only local declaration (single or grouped block) and
// stamps every produced var decl with Ghost=true. The grouped block's extra decls are buffered in
// p.pendingStmts; parseBlock drains them so they land flat in the enclosing block.
func (p *Parser) parseGhostStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("ghost")

	// Grouped block: `ghost:` then an indented run of `name: T = expr` decls.
	if p.peek() == lexer.TOKEN_COLON {
		p.advance()
		p.expectNewline()
		body := p.parseBlock()
		var first ast.Stmt
		for _, st := range body {
			vd, ok := st.(*ast.VarDeclStmt)
			if !ok {
				p.errorf("a `ghost:` block may contain only ghost variable declarations")
				continue
			}
			vd.Ghost = true
			if first == nil {
				first = vd
			} else {
				p.pendingStmts = append(p.pendingStmts, vd)
			}
		}
		if first == nil {
			return &ast.PassStmt{Position: pos}
		}
		return first
	}

	// Single decl: reuse the ordinary var-decl parser, then stamp it ghost.
	stmt := p.parseExprOrAssignStmt()
	vd, ok := stmt.(*ast.VarDeclStmt)
	if !ok {
		p.errorf("`ghost` must be followed by a variable declaration (`ghost x: T = expr`)")
		return stmt
	}
	vd.Ghost = true
	return vd
}

func (p *Parser) parseEmitStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("emit")
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "all" {
		p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.ExprStmt{Position: pos, Expr: &ast.EmitExpr{Position: pos, Value: value, All: true}}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "nothing" {
		p.advance()
		p.expectNewline()
		return &ast.ExprStmt{Position: pos, Expr: &ast.EmitExpr{Position: pos, Nothing: true}}
	}
	value := p.parseExpr()
	p.expectNewline()
	return &ast.ExprStmt{Position: pos, Expr: &ast.EmitExpr{Position: pos, Value: value}}
}
func (p *Parser) looksLikeSequenceEmitStmt() bool {
	if !p.hasToken(p.pos + 1) {
		return true
	}
	switch p.tokenAt(p.pos + 1).Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_DOT:
		return false
	default:
		return true
	}
}
func (p *Parser) looksLikeSignalStmt() bool {
	if !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_NEWLINE:
			return depth == 0
		case lexer.TOKEN_EOF:
			return true
		}
	}
	return true
}
func (p *Parser) looksLikePoolStmt() bool {
	if !p.hasToken(p.pos+2) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT || p.tokenAt(p.pos+2).Kind != lexer.TOKEN_LPAREN {
		return false
	}
	depth := 0
	for i := p.pos + 2; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COLON:
			return depth == 0
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}
