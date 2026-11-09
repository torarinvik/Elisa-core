package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseMatchValuePatternExpr() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_INT_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString(), IsHex: false}
	case lexer.TOKEN_HEX_LIT:
		tok := p.advance()
		return &ast.IntLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString(), IsHex: true}
	case lexer.TOKEN_FLOAT_LIT:
		tok := p.advance()
		return &ast.FloatLit{Position: tok.Pos, Value: tok.Text, Suffix: tok.SuffixString()}
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
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
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
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_ASSIGN {
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
	for i := p.pos + 1; p.hasToken(i); i++ {
		switch p.tokenAt(i).Kind {
		case lexer.TOKEN_NEWLINE:
			return false
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && p.tokenAt(i).Text == "into" {
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
		if p.peekAt(1) != lexer.TOKEN_COLON && p.returnIfIsValueForm() {
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
		cond := p.parseGuardCondition()
		p.expectNewlineAfterValueExpr(cond)
		return &ast.IfStmt{Position: pos, Cond: cond, Then: []ast.Stmt{&ast.ReturnStmt{Position: pos}}}
	}
	// docs/119 §2.1: `return` is one of the four sites (`=`, `<-`, `return`, an arm
	// colon) where NEWLINE INDENT opens a block expression. A bare `return` never
	// continues onto an indented line, so the claim is backward compatible.
	if blockValue, ok := p.parseValueBlockRHS(pos); ok {
		p.expectNewlineAfterValueExpr(blockValue)
		return &ast.ReturnStmt{Position: pos, Value: blockValue}
	}
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		// A postfix guard may be consumed by the expression parser while parsing
		// the return value, especially when the value is unary (`return -1 if
		// stop`). Arm the statement-guard path so the parser normalizes that
		// spelling to the same IfStmt used by `value return if stop`.
		p.stmtGuardArmed = true
		value = p.parseValueExprAllowTuple()
		p.stmtGuardArmed = false
	}
	p.expectNewlineAfterValueExpr(value)
	return p.takeStmtGuard(&ast.ReturnStmt{Position: pos, Value: value})
}

// returnIfIsValueForm decides whether the `if` following `return` opens a value-if block
// (`return if cond: …`) rather than a bare postfix guard (`return if cond` NEWLINE). It
// scans the condition tokens (from the `if`) and reports true iff a `:` appears at bracket
// depth 0 before the line-ending newline — a colon inside `()`/`[]`/`{}` (a dict literal,
// call, or index) does not count.
func (p *Parser) returnIfIsValueForm() bool {
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		switch p.tokenAt(i).Kind {
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
