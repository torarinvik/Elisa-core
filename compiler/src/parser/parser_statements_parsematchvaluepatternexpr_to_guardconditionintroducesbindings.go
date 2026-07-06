package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseMatchValuePatternExpr() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: false}
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix, IsHex: true}
	case lexer.TOKEN_FLOAT_LIT:
		tok := p.advance()
		return &ast.FloatLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.Suffix}
	case lexer.TOKEN_CHAR_LIT:
		tok := p.advance()
		return &ast.CharLit{Position: tok.Pos, Value: tok.Text}
	case lexer.TOKEN_TRUE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: true}
	case lexer.TOKEN_FALSE:
		tok := p.advance()
		return &ast.BoolLit{Position: tok.Pos, Value: false}
	case lexer.TOKEN_NULL:
		tok := p.advance()
		return &ast.NullLit{Position: tok.Pos}
	case lexer.TOKEN_DOT:
		return p.parsePrimary()
	case lexer.TOKEN_IDENT:
		pos := p.cur().Pos
		parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
		if !p.match(lexer.TOKEN_DOT) {
			p.errorf("value pattern expects a literal or qualified member")
			return &ast.Ident{Position: pos, Name: parts[0]}
		}
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		for p.match(lexer.TOKEN_DOT) {
			parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		}
		return buildQualifiedMatchValueExpr(pos, parts)
	case lexer.TOKEN_MINUS:
		pos := p.cur().Pos
		p.advance()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: p.parseMatchValuePatternExpr()}
	case lexer.TOKEN_LPAREN:
		pos := p.cur().Pos
		p.advance()
		inner := p.parseMatchValuePatternExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: pos, Inner: inner}
	default:
		p.errorf("match value pattern expects a literal or qualified member")
		return &ast.IntLit{Position: p.cur().Pos, Value: "0"}
	}
}
func (p *Parser) parseMatchPatternArg() ast.MatchPatternArg {
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		pattern := p.parseNestedOrMatchPattern()
		return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
	}
	pattern := p.parseNestedOrMatchPattern()
	return ast.MatchPatternArg{Position: pattern.Pos(), Pattern: pattern}
}
func (p *Parser) parseRegion() *ast.RegionStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	var capacity ast.Expr
	if p.match(lexer.TOKEN_LPAREN) {
		capacity = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	}
	// Optional `using <allocator>` selects the region's backing allocator (e.g. `using malloc`).
	// `using` is a contextual identifier here, not a reserved keyword.
	allocator := ""
	if p.matchIdentText("using") {
		allocator = p.expect(lexer.TOKEN_IDENT).Text
	}
	// `region NAME(cap):` is the scoped form: the region owner is discharged
	// automatically at block exit (RAII), so no explicit `destroy` is needed.
	// The plain `region NAME(cap)` form must be matched by `destroy NAME` on
	// every path (it is an affine must-consume owner).
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		body := p.parseBlock()
		return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity, Allocator: allocator, Body: body}
	}
	p.expectNewline()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity, Allocator: allocator}
}
func (p *Parser) parseScopeStmt() *ast.ScopeStmt {
	pos := p.cur().Pos
	p.expectIdentText("scope")
	guard := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return &ast.ScopeStmt{Position: pos, Guard: guard, Body: p.parseBlock()}
}
func (p *Parser) parseDestroy() *ast.DestroyStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.DestroyStmt{Position: pos, Name: name}
}
func (p *Parser) parseLeak() *ast.LeakStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	p.errorAt(pos, "explicit `leak NAME` has been removed; region lifetimes are inferred")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.LeakStmt{Position: pos, Name: name}
}
func (p *Parser) parseMark() *ast.MarkStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	p.errorAt(pos, "explicit `mark NAME as SAVE` has been removed; use a nested scoped `region NAME:` block instead of manual high-water marks")
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_AS)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.MarkStmt{Position: pos, RegionName: regionName, Name: name}
}
func (p *Parser) parseCheckpointStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("checkpoint")
	p.errorAt(pos, "`checkpoint …:` has been removed; snapshot the darray lengths (`n: usize = xs.count`) and restore them on exit with `defer block:` + `xs.truncate(n)`")
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		target := p.parseExpr()
		var body []ast.Stmt
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			body = p.parseBlock()
		} else {
			p.expectNewline()
		}
		return &ast.CheckpointStmt{Position: pos, Name: name, Target: target, Body: body}
	}
	firstTarget := p.parseExpr()
	targets := []ast.Expr{firstTarget}
	if !p.match(lexer.TOKEN_COMMA) {
		p.errorf("grouped checkpoint requires at least 2 targets")
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		return &ast.GroupedCheckpointStmt{Position: pos, Targets: targets, Body: p.parseBlock()}
	}
	targets = append(targets, p.parseExpr())
	for p.match(lexer.TOKEN_COMMA) {
		targets = append(targets, p.parseExpr())
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return &ast.GroupedCheckpointStmt{Position: pos, Targets: targets, Body: p.parseBlock()}
}
func (p *Parser) parseRestore() *ast.RestoreStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	p.errorAt(pos, "explicit `restore NAME from SAVE` has been removed; use a nested scoped `region NAME:` block instead of manual high-water marks")
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("from")
	markName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreStmt{Position: pos, RegionName: regionName, MarkName: markName}
}
func (p *Parser) parseRestoreCheckpointStmt() *ast.RestoreCheckpointStmt {
	pos := p.cur().Pos
	p.expectIdentText("restore")
	p.errorAt(pos, "explicit `restore NAME` has been removed; region lifetimes are inferred and scoped")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreCheckpointStmt{Position: pos, Name: name}
}
func (p *Parser) parseReset() *ast.ResetStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	p.errorAt(pos, "explicit `reset NAME` has been removed; use a nested scoped `region NAME:` block whose allocations are freed at block exit")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.ResetStmt{Position: pos, Name: name}
}
func (p *Parser) parsePromote() *ast.PromoteStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT) // "promote"
	p.errorAt(pos, "explicit `promote VALUE into REGION` has been removed; region-polymorphic returns thread the caller's region automatically")
	value := p.parseExpr()
	p.expectIdentText("into")
	region := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.PromoteStmt{Position: pos, Value: value, Region: region}
}
func (p *Parser) parseAdopt() *ast.AdoptStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT) // "adopt"
	p.errorAt(pos, "explicit `adopt CHILD into PARENT` has been removed; region ownership transfer is inferred")
	child := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("into")
	parent := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.AdoptStmt{Position: pos, Child: child, Parent: parent}
}

// looksLikePromoteStmt reports whether the statement starting at the current
// `promote` identifier is a `promote <value> into <region>` statement, by
// scanning for a depth-0 `into` keyword before the end of the line. This lets the
// value be an arbitrary expression (not just a bare identifier) while keeping
// `promote` usable as an ordinary identifier elsewhere.
func (p *Parser) looksLikePromoteStmt() bool {
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_NEWLINE:
			return false
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && p.tokens[i].Text == "into" {
				return true
			}
		}
	}
	return false
}
func (p *Parser) parseReturn() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_RETURN)
	if p.match(lexer.TOKEN_QUESTION) {
		p.errorAt(pos, "`return?` is no longer supported; use `return value else return null`")
		p.skipRemovedQuestionStmtTail()
		return &ast.ReturnStmt{Position: pos}
	}
	// An `if` immediately after `return` is one of two forms, disambiguated by whether the
	// condition line ends in a `:` (a block header) or a newline:
	//   • `return if <cond>` NEWLINE      → bare postfix guard, desugars to `if <cond>: return`.
	//   • `return if <cond>: A else: B`   → value-if block whose branch value is returned
	//     (docs/120 §10 / docs/119 §4). Branches may carry statements before their tail, so a
	//     `rebind` claim can thread an lmut value inside a branch before yielding the tuple.
	if p.peek() == lexer.TOKEN_IF {
		if p.returnIfIsValueForm() {
			// The branches are value blocks: their tail may be a bare `a, b` tuple (the
			// threaded slots), so parse them in expression-block context (docs/119 §2).
			p.exprBlockDepth++
			ifStmt := p.parseIf()
			p.exprBlockDepth--
			value, ok := p.tailStmtToExpr(ifStmt)
			if !ok {
				return &ast.ReturnStmt{Position: pos}
			}
			return &ast.ReturnStmt{Position: pos, Value: value}
		}
		p.advance()
		cond := p.parseExpr()
		p.expectNewlineAfterValueExpr(cond)
		return &ast.IfStmt{Position: pos, Cond: cond, Then: []ast.Stmt{&ast.ReturnStmt{Position: pos}}}
	}
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		value = p.parseValueExprAllowTuple()
	}
	p.expectNewlineAfterValueExpr(value)
	return &ast.ReturnStmt{Position: pos, Value: value}
}

// returnIfIsValueForm decides whether the `if` following `return` opens a value-if block
// (`return if cond: …`) rather than a bare postfix guard (`return if cond` NEWLINE). It
// scans the condition tokens (from the `if`) and reports true iff a `:` appears at bracket
// depth 0 before the line-ending newline — a colon inside `()`/`[]`/`{}` (a dict literal,
// call, or index) does not count.
func (p *Parser) returnIfIsValueForm() bool {
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COLON:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_DEDENT:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

func (p *Parser) skipRemovedQuestionStmtTail() {
	sawColon := false
	for p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT {
		if p.peek() == lexer.TOKEN_COLON {
			sawColon = true
		}
		p.advance()
	}
	p.match(lexer.TOKEN_NEWLINE)
	if sawColon && p.match(lexer.TOKEN_INDENT) {
		depth := 1
		for depth > 0 && p.peek() != lexer.TOKEN_EOF {
			switch p.peek() {
			case lexer.TOKEN_INDENT:
				depth++
			case lexer.TOKEN_DEDENT:
				depth--
			}
			p.advance()
		}
	}
}
func (p *Parser) parseValueExprAllowTuple() ast.Expr {
	first := p.parseExpr()
	if p.peek() != lexer.TOKEN_COMMA {
		return first
	}
	return p.parseTupleExprFromFirst(first.Pos(), first)
}
func (p *Parser) tryParseTupleBindStmt(pos lexer.Pos) ast.Stmt {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_COMMA {
		return nil
	}
	savedPos := p.pos
	names := make([]ast.TupleBindName, 0, 4)
	for {
		if p.peek() != lexer.TOKEN_IDENT {
			p.pos = savedPos
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
		p.pos = savedPos
		return nil
	}
	var value ast.Expr
	if blockValue, ok := p.parseValueBlockRHS(pos); ok {
		// docs/119 §2/§6: `c, d = [|caps|]` NEWLINE INDENT ... — (captured) block
		// expression whose tail tuple binds the targets.
		value = blockValue
		p.expectNewlineAfterValueExpr(value)
	} else {
		value = p.parseValueExprAllowTuple()
		// Not the plain expectNewline: a multi-line match-expression RHS ends with its
		// arm block's DEDENT (the trailing newline was consumed inside the final arm).
		p.expectNewlineAfterValueExpr(value)
	}
	// docs/120 §6: in the `<-` (reassign) form, a slot naming its own target — the bare
	// binding or a mutating call on it — is a THREAD slot and erases; only value slots
	// remain bound. The `=` (declare) form introduces fresh names and never threads.
	if !declare {
		places := make([]ast.Expr, len(names))
		for i, nm := range names {
			places[i] = &ast.Ident{Position: nm.Position, Name: nm.Name}
		}
		if stmt, handled := p.desugarThreadSlots(pos, places, value); handled {
			return stmt
		}
		// docs/120 implicit threading — mixed claim form: `lexer, suffix <-
		// lexer.read_type_suffix()`. When the RHS is a single direct call, a target
		// naming an argument (or UFCS receiver) root claims that lmut thread — the
		// mutation is in place, so the target has no value slot: drop it from the
		// bind and record the claim for the semantic layer to validate (mirrors the
		// rebind form; a claim on a non-lmut argument is a compile error there).
		// The remaining targets bind the call's real return.
		if call, isCall := value.(*ast.CallExpr); isCall {
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
				return &ast.ExprStmt{Position: pos, Expr: value}
			case 1:
				// One value target: the return is a scalar — plain reassignment.
				return &ast.AssignStmt{Position: names[0].Position, Target: &ast.Ident{Position: names[0].Position, Name: names[0].Name}, Value: value}
			}
		}
	}
	return &ast.TupleBindStmt{Position: pos, Names: names, Declare: declare, Value: value}
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
func (p *Parser) parsePanic() *ast.PanicStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PANIC)
	p.expect(lexer.TOKEN_LPAREN)
	msg := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	p.expectNewline()
	return &ast.PanicStmt{Position: pos, Message: msg}
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
		p.expectNewline()
		elseBlock = p.parseBlock()
	}

	return lowerIfClauses(clauses, elseBlock)
}

// isElseIfChain reports whether the cursor is at a removed two-word `else if` chain
// (an `else` immediately followed by `if`), as opposed to a plain trailing `else:` block.
func (p *Parser) isElseIfChain() bool {
	return p.peek() == lexer.TOKEN_ELSE && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IF
}
func (p *Parser) parseIfClause(isElif bool) ifClause {
	pos := p.cur().Pos
	hint := p.parseBranchHint()
	if p.peekIdentText("let") {
		return p.parseIfLetClause(pos, hint, isElif)
	}
	headStart := p.pos
	head := p.withInMembershipDisabled(p.parseExpr)
	if p.peek() == lexer.TOKEN_IN && p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACE) {
		p.pos = headStart
		cond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		body := p.parseStmtBodyAfterColon()
		return ifClause{Position: pos, Hint: hint, Cond: cond, Body: body}
	}
	if p.notInMembershipAhead() {
		p.pos = headStart
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
		p.pos = headStart
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
	for end < len(p.tokens) {
		tok := p.tokens[end]
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
	slice := append([]lexer.Token(nil), p.tokens[start:end]...)
	eofPos := slice[len(slice)-1].Pos
	slice = append(slice, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: eofPos})
	sub := New(slice)
	sub.poolScopes = append([]string(nil), p.poolScopes...)
	expr := sub.parseExpr()
	p.errors = append(p.errors, sub.Errors()...)
	p.pos = end
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
