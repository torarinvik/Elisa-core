package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseValueExprAllowTuple() ast.Expr {
	first := p.parseExpr()
	if p.peek() != lexer.TOKEN_COMMA {
		return first
	}
	return p.parseTupleExprFromFirst(first.Pos(), first)
}
func (p *Parser) tryParseTupleBindStmt(pos lexer.Pos) ast.Stmt {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_COMMA {
		return nil
	}
	savedPos := p.pos
	names := make([]ast.TupleBindName, 0, 4)
	for {
		if p.peek() != lexer.TOKEN_IDENT {
			p.seek(savedPos)
			return nil
		}
		tok := p.advance()
		names = append(names, ast.TupleBindName{Position: tok.Pos, Name: tok.Text})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	declare := false
	switch p.peek() {
	case lexer.TOKEN_ASSIGN:
		declare = true
		p.advance()
	case lexer.TOKEN_LARROW:
		p.advance()
	default:
		p.seek(savedPos)
		return nil
	}
	var value ast.Expr
	if blockValue, ok := p.parseValueBlockRHS(pos); ok {
		// docs/119 §2/§6: `c, d = [|caps|]` NEWLINE INDENT ... — (captured) block
		// expression whose tail tuple binds the targets.
		value = blockValue
		p.expectNewlineAfterValueExpr(value)
	} else {
		// docs/125 §6b: like single-place `x <- v if cond`, the `<-` (reassign) form
		// admits a postfix statement guard — `a, b <- call() if cond` is do-or-skip.
		// The `=` (declare) form does not: a conditionally-declared binding would be
		// unusable afterwards.
		if !declare {
			p.stmtGuardArmed = true
		}
		value = p.parseValueExprAllowTuple()
		p.stmtGuardArmed = false
		// Not the plain expectNewline: a multi-line match-expression RHS ends with its
		// arm block's DEDENT (the trailing newline was consumed inside the final arm).
		p.expectNewlineAfterValueExpr(value)
	}
	// A postfix guard must scope the WHOLE desugar — thread effects, per-place
	// pendingStmts writes, and the bind itself — so route every return below through
	// guardTupleBind, which captures newly appended pending statements into the
	// guard's then-block.
	pendingBase := len(p.pendingStmts)
	// docs/120 §6: in the `<-` (reassign) form, a slot naming its own target — the bare
	// binding or a mutating call on it — is a THREAD slot and erases; only value slots
	// remain bound. The `=` (declare) form introduces fresh names and never threads.
	if !declare {
		places := make([]ast.Expr, len(names))
		for i, nm := range names {
			places[i] = &ast.Ident{Position: nm.Position, Name: nm.Name}
		}
		if stmt, handled := p.desugarThreadSlots(pos, places, value); handled {
			return p.guardPendingStmts(stmt, pendingBase)
		}
		// docs/120 implicit threading — mixed claim form: `lexer, suffix <-
		// lexer.read_type_suffix()`. When the RHS is a single direct call, a target
		// naming an argument (or UFCS receiver) root claims that lmut thread — the
		// mutation is in place, so the target has no value slot: drop it from the
		// bind and record the claim for the semantic layer to validate (mirrors the
		// rebind form; a claim on a non-lmut argument is a compile error there).
		// The remaining targets bind the call's real return.
		if call := claimableCall(value); call != nil {
			roots := callArgRootNames(call)
			kept := names[:0]
			for _, nm := range names {
				if roots[nm.Name] {
					call.LmutRebindClaims = append(call.LmutRebindClaims, ast.LmutRebindClaim{Position: nm.Position, Name: nm.Name})
					continue
				}
				kept = append(kept, nm)
			}
			names = kept
			switch len(names) {
			case 0:
				// Every target was a claimed thread: the call stands alone.
				return p.guardPendingStmts(&ast.ExprStmt{Position: pos, Expr: value}, pendingBase)
			case 1:
				// One value target: the return is a scalar. `_` discards it explicitly
				// (`parser, _ <- parser.advance()` — the thread is claimed, the Token
				// dropped visibly); otherwise a plain reassignment.
				if names[0].Name == "_" {
					return p.guardPendingStmts(&ast.DiscardStmt{Position: pos, Value: value}, pendingBase)
				}
				return p.guardPendingStmts(&ast.AssignStmt{Position: names[0].Position, Target: &ast.Ident{Position: names[0].Position, Name: names[0].Name}, Value: value}, pendingBase)
			}
		}
	}
	return p.guardPendingStmts(&ast.TupleBindStmt{Position: pos, Names: names, Declare: declare, Value: value}, pendingBase)
}
func (p *Parser) parseLetDestructureStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("let")
	pattern := p.parseLetDestructurePattern()
	p.expect(lexer.TOKEN_ASSIGN)
	value := p.parseValueExprAllowTuple()
	p.expectNewlineAfterValueExpr(value)
	return &ast.LetDestructureStmt{Position: pos, Pattern: pattern, Value: value}
}
func (p *Parser) parseLetDestructurePattern() *ast.MoveBindStructPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(pos, "")
	}
	if p.peekQualifiedStructDestructurePattern() {
		typeName := p.parseQualifiedDeclName()
		return p.parseMoveBindStructBracePattern(pos, typeName)
	}
	p.errorf("let destructuring expects {...} or Type{...}")
	return &ast.MoveBindStructPattern{Position: pos, Brace: true}
}
func (p *Parser) parsePass() *ast.PassStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PASS)
	p.expectNewline()
	return &ast.PassStmt{Position: pos}
}
func (p *Parser) parseSignalStmt() *ast.SignalStmt {
	pos := p.cur().Pos
	p.expectIdentText("signal")
	ref := p.parsePermissionRef()
	p.expectNewline()
	return &ast.SignalStmt{Position: pos, Permissions: []ast.PermissionRef{ref}}
}

// parsePanic parses `panic(msg)`, optionally with a trailing postfix guard
// `panic(msg) if COND` (docs/125 §6b: a guarded divergence — do-or-skip, no else — the
// analogue of `break if`/`continue if`/`VALUE return if`). The guard desugars to a
// NON-source `if COND: panic(msg)`, so the strict block-`if` ban never fires on it and a
// conditional panic reads as one line instead of a two-line block.
func (p *Parser) parsePanic() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PANIC)
	p.expect(lexer.TOKEN_LPAREN)
	msg := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	panicStmt := &ast.PanicStmt{Position: pos, Message: msg}
	if p.match(lexer.TOKEN_IF) {
		cond := p.parseGuardCondition()
		p.expectNewlineAfterValueExpr(cond)
		return &ast.IfStmt{Position: pos, Cond: cond, Then: []ast.Stmt{panicStmt}}
	}
	p.expectNewline()
	return panicStmt
}

type ifClause struct {
	Position         lexer.Pos
	Hint             ast.BranchHint
	Cond             ast.Expr
	Value            ast.Expr
	Store            ast.Expr
	Patterns         []ast.MatchPattern
	OptionalBindings []optionalIfBinding
	Body             []ast.Stmt
}

type optionalIfBinding struct {
	name  string
	value ast.Expr
}

func (p *Parser) parseBranchHint() ast.BranchHint {
	if p.matchIdentText("likely") {
		return ast.BranchHintLikely
	}
	if p.matchIdentText("unlikely") {
		return ast.BranchHintUnlikely
	}
	return ast.BranchHintNone
}
func (p *Parser) parseIf() ast.Stmt {
	p.expect(lexer.TOKEN_IF)
	first := p.parseIfClause(false)
	clauses := []ifClause{first}

	for p.peek() == lexer.TOKEN_ELIF || p.isElseIfChain() {
		if p.peek() == lexer.TOKEN_ELSE {
			// Two-word `else if` chain has been removed in favor of `elif`. Give a
			// directed diagnostic but recover by treating `else if` as `elif`, so the
			// rest of the chain still parses into a correct AST (no error cascade).
			p.errorf("`else if` is not valid; use `elif COND:` for a chained condition")
			p.advance() // `else`
			p.advance() // `if`
		} else {
			p.advance() // `elif`
		}
		clauses = append(clauses, p.parseIfClause(true))
	}

	var elseBlock []ast.Stmt
	if p.match(lexer.TOKEN_ELSE) {
		p.expect(lexer.TOKEN_COLON)
		// Same body grammar as `if COND:` / `elif COND:` — an indented block or one
		// statement on the header line. docs/119 §2.3/§3.2 write value chains as
		// `if c: "gold"` … `else: "bronze"`; only the `else` arm demanded a newline,
		// so the spec's own examples failed to parse.
		elseBlock = p.parseStmtBodyAfterColon()
	}

	return lowerIfClauses(clauses, elseBlock)
}

// isElseIfChain reports whether the cursor is at a removed two-word `else if` chain
// (an `else` immediately followed by `if`), as opposed to a plain trailing `else:` block.
func (p *Parser) isElseIfChain() bool {
	return p.peek() == lexer.TOKEN_ELSE && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IF
}
func (p *Parser) parseIfClause(isElif bool) ifClause {
	pos := p.cur().Pos
	hint := p.parseBranchHint()
	if p.peekIdentText("let") {
		return p.parseIfLetClause(pos, hint, isElif)
	}
	headStart := p.pos
	head := p.withInMembershipDisabled(p.parseExpr)
	if p.peek() == lexer.TOKEN_IN && p.hasToken(p.pos+1) && (p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LBRACKET || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LBRACE) {
		p.seek(headStart)
		cond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseStmtBodyAfterColon()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	if p.notInMembershipAhead() {
		p.seek(headStart)
		cond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseStmtBodyAfterColon()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	if p.match(lexer.TOKEN_AS) {
		if hint != ast.BranchHintNone {
			if isElif {
				p.errorf("elif likely/unlikely hint cannot be combined with pattern binders")
			} else {
				p.errorf("if likely/unlikely hint cannot be combined with pattern binders")
			}
		}
		patterns := p.parseTopLevelMatchPatterns()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseStmtBodyAfterColon()
		return ifClause{Position: pos, Hint: hint, Value: head, Patterns: patterns, Body: body}
	}
	if p.match(lexer.TOKEN_IN) {
		if hint != ast.BranchHintNone {
			if isElif {
				p.errorf("elif likely/unlikely hint cannot be combined with pattern binders")
			} else {
				p.errorf("if likely/unlikely hint cannot be combined with pattern binders")
			}
		}
		// Parse the store with `is` suppressed so `value in store is Pattern`
		// keeps the trailing `is` for the store-pattern clause (docs/80 Phase B).
		store := p.withIsComparisonDisabled(p.parseExpr)
		if p.peek() == lexer.TOKEN_AS {
			p.errorf("`value in store as Pattern` is no longer supported; use `value in store is Pattern:` instead")
		}
		if p.match(lexer.TOKEN_AS) || p.match(lexer.TOKEN_IS) {
			patterns := p.parseTopLevelMatchPatterns()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			body := p.parseBlock()
			return ifClause{Position: pos, Hint: hint, Value: head, Store: store, Patterns: patterns, Body: body}
		}
		p.seek(headStart)
		cond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseStmtBodyAfterColon()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	p.expect(lexer.TOKEN_COLON)
	body := p.parseStmtBodyAfterColon()
	return ifClause{Position: pos, Hint: hint, Cond: head, Body: body}
}
func (p *Parser) parseIfLetClause(pos lexer.Pos, hint ast.BranchHint, isElif bool) ifClause {
	p.errorf("`if let` is no longer supported; use `if EXPR is NAME:` instead (e.g. `if v is value:`)")
	p.expectIdentText("let")
	bindings := make([]optionalIfBinding, 0, 2)
	for {
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseNot()
		bindings = append(bindings, optionalIfBinding{name: name, value: value})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		p.skipOptionalBindingContinuationTrivia()
	}
	var cond ast.Expr
	if p.match(lexer.TOKEN_AND) {
		cond = p.parseExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	for p.peek() == lexer.TOKEN_DEDENT {
		p.advance()
	}
	body := p.parseBlock()
	return ifClause{Position: pos, Cond: cond, OptionalBindings: bindings, Body: body}
}
func (p *Parser) skipOptionalBindingContinuationTrivia() {
	p.skipNewlines()
	if p.peek() == lexer.TOKEN_INDENT {
		p.advance()
	}
}
func (p *Parser) parseGuardConditionExpr() ast.Expr {
	depth := 0
	start := p.pos
	end := start
	for p.hasToken(end) {
		tok := p.tokenAt(end)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_ELSE:
			if depth == 0 {
				goto parse
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			goto parse
		}
		end++
	}

parse:
	if end == start {
		p.errorf("guard requires a condition before else")
		return &ast.BoolLit{Position: p.cur().Pos, Value: false}
	}
	slice := append([]lexer.Token(nil), p.tokenSpan(start, end)...)
	eofPos := slice[len(slice)-1].Pos
	slice = append(slice, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: eofPos})
	sub := New(slice)
	sub.poolScopes = append([]string(nil), p.poolScopes...)
	expr := sub.parseExpr()
	p.errors = append(p.errors, sub.Errors()...)
	p.seek(end)
	return expr
}
func guardConditionIntroducesBindings(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.OptionalBindExpr:
		return true
	case *ast.ParenExpr:
		return guardConditionIntroducesBindings(n.Inner)
	case *ast.UnaryExpr:
		return guardConditionIntroducesBindings(n.Operand)
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND || n.Op == lexer.TOKEN_OR {
			return guardConditionIntroducesBindings(n.Left) || guardConditionIntroducesBindings(n.Right)
		}
		if n.Op != lexer.TOKEN_IS {
			return false
		}
		switch test := n.Right.(type) {
		case *ast.StructTestExpr:
			return matchPatternContainsBindNames(test.Pattern)
		case *ast.VariantTestExpr:
			return matchPatternContainsBindNames(test.Pattern)
		default:
			return false
		}
	default:
		return false
	}
}
