package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

func (p *Parser) parseAssertHoleStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // `assert`
	p.expect(lexer.TOKEN_QUESTION)
	p.expectNewline()
	return &ast.AssertHoleStmt{Position: pos}
}

// looksLikeAssertByStmt reports whether the current `assert` begins a proof-carrying
// `assert COND by:` form, by scanning the logical line for a top-level `by` identifier immediately
// followed by `:`. A plain `assert(COND)` call has no such opener, so it falls through to ordinary
// expression-statement parsing. Bracket depth is tracked so a `by:` buried inside a nested call/index
// (where it cannot be the proof opener) is ignored.
func (p *Parser) looksLikeAssertByStmt() bool {
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && tok.Text == "by" && p.hasToken(i+1) {
				// `by:` or the closed-world `by scoped:` (docs/99) both open a proof block.
				if p.tokenAt(i+1).Kind == lexer.TOKEN_COLON {
					return true
				}
				if p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(i+1).Text == "scoped" && p.hasToken(i+2) && p.tokenAt(i+2).Kind == lexer.TOKEN_COLON {
					return true
				}
				if p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(i+1).Text == "scoped" && p.hasToken(i+3) && p.tokenAt(i+2).Kind == lexer.TOKEN_COLON && p.tokenAt(i+3).Kind == lexer.TOKEN_IDENT && isScopedProofTheory(p.tokenAt(i+3).Text) {
					return true
				}
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}

// parseAssertByStmt parses `assert COND by:` + an indented proof block. The proof block is a normal
// statement block (it may hold lemma calls and nested asserts); it is verification-only and erased
// from codegen.
func (p *Parser) parseAssertByStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // `assert`
	cond := p.parseExpr()
	if !(p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by") {
		p.errorf("expected `by:` proof block after assert condition")
		return &ast.AssertByStmt{Position: pos, Cond: cond}
	}
	p.advance() // `by`
	scoped := false
	theory := ""
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "scoped" {
		scoped = true // `by scoped:` opens a CLOSED-WORLD proof block (docs/99).
		p.advance()
	}
	p.expect(lexer.TOKEN_COLON)
	if scoped && p.peek() == lexer.TOKEN_IDENT {
		if isScopedProofTheory(p.cur().Text) {
			theory = p.cur().Text
			p.advance()
		} else {
			p.errorf("expected scoped proof theory marker `arithmetic` or `bitvector`")
		}
	}
	p.expectNewline()
	proof := p.parseBlock()
	return &ast.AssertByStmt{Position: pos, Cond: cond, Proof: proof, Scoped: scoped, ScopedTheory: theory}
}

func isScopedProofTheory(name string) bool {
	return name == "arithmetic" || name == "bitvector"
}

func (p *Parser) looksLikeProofBlockStmt() bool {
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
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

func (p *Parser) parseProofBlockStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // `proof`
	goal := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return &ast.ProofBlockStmt{Position: pos, Goal: goal, Proof: p.parseBlock()}
}

func (p *Parser) parseOptionalContractScopedProof(cond ast.Expr) ([]ast.Stmt, bool) {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		p.advance()
		if !(p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "scoped") {
			p.errorf("contract clause proof blocks must use `by scoped:`")
			p.expectNewline()
			return nil, false
		}
		p.advance()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		return p.parseBlock(), true
	}
	p.expectNewlineAfterValueExpr(cond)
	return nil, false
}

func (p *Parser) parseProofUseStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance() // `use`
	citations := []ast.Expr{p.parseExpr()}
	for p.match(lexer.TOKEN_COMMA) {
		citations = append(citations, p.parseExpr())
	}
	p.expectNewline()
	return &ast.ProofUseStmt{Position: pos, Citations: citations}
}

func (p *Parser) looksLikeGuardStmt() bool {
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
		case lexer.TOKEN_ELSE:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}
func (p *Parser) looksLikeDeferStmt() bool {
	if !p.hasToken(p.pos + 2) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT {
		return false
	}
	mode := p.tokenAt(p.pos + 1).Text
	if mode != "block" && mode != "function" {
		return false
	}
	return p.tokenAt(p.pos+2).Kind == lexer.TOKEN_COLON
}
func (p *Parser) looksLikeParallelForStmt() bool {
	if !p.hasToken(p.pos + 4) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IDENT || p.tokenAt(p.pos+1).Text != "for" {
		return false
	}
	if p.tokenAt(p.pos+2).Kind != lexer.TOKEN_IDENT {
		return false
	}
	sourcePos := p.pos + 3
	if p.tokenAt(sourcePos).Kind == lexer.TOKEN_IDENT && p.tokenAt(sourcePos).Text == "at" {
		if !p.hasToken(p.pos + 6) {
			return false
		}
		if p.tokenAt(p.pos+4).Kind != lexer.TOKEN_IDENT {
			return false
		}
		if p.tokenAt(p.pos+5).Kind != lexer.TOKEN_IN {
			return false
		}
		sourcePos = p.pos + 6
	} else if p.tokenAt(sourcePos).Kind != lexer.TOKEN_IN {
		return false
	}
	depth := 0
	for i := sourcePos + 1; p.hasToken(i); i++ {
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
func (p *Parser) looksLikeScopeStmt() bool {
	if !p.hasToken(p.pos + 2) {
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
		case lexer.TOKEN_COLON:
			return depth == 0
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}
func (p *Parser) looksLikeCheckpointStmt() bool {
	if p.hasToken(p.pos+2) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_ASSIGN {
		return true
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
		case lexer.TOKEN_COLON:
			return depth == 0
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}
func (p *Parser) looksLikeForStmt() bool {
	return p.looksLikeForStmtAt(p.pos)
}

func (p *Parser) looksLikeForStmtAt(pos int) bool {
	if pos < 0 || !p.hasToken(pos+2) {
		return false
	}
	if p.tokenAt(pos).Kind != lexer.TOKEN_IDENT || p.tokenAt(pos).Text != "for" {
		return false
	}
	switch p.tokenAt(pos + 1).Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_LBRACE:
	default:
		return false
	}
	depth := 0
	seenIn := false
	for i := pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IN:
			if depth == 0 {
				seenIn = true
			}
		case lexer.TOKEN_COLON:
			if depth == 0 {
				return seenIn
			}
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}

// parseIterBindMode parses the loop-binding intent: `for x in ...` reads (the compiler
// binds by reference under the hood when beneficial or required — see the affine
// auto-borrow in analyzeIterForStmt), `for mutable x in ...` mutates elements in place,
// and `for x in move c` (spelled on the iterable) consumes. The mechanism spellings
// `ref` / `mutable ref` have been removed: copy-vs-ref on an immutable binding is
// semantically invisible, so it is the compiler's decision, not syntax.
func (p *Parser) parseIterBindMode() ast.IterBindMode {
	if p.match(lexer.TOKEN_MUTABLE) {
		// Removed explicit alias `for mutable ref x in ...`: directed error, recover
		// as the canonical `mutable` (same mode) so the loop still parses cleanly.
		if p.peekIdentText("ref") {
			p.errorf("`for mutable ref x in ...` has been removed; use `for mutable x in ...` (same meaning)")
			p.advance()
		}
		return ast.IterBindMutableRef
	}
	if p.peekIdentText("ref") && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IN {
		// Removed `for ref x in ...`: an immutable binding's copy-vs-ref mechanism is
		// inferred now. Directed error; recover as IterBindRef so the semantics the
		// author relied on (borrow, e.g. affine elements) still hold with no cascade.
		p.errorf("`for ref x in ...` has been removed; use `for x in ...` (the compiler binds by reference when needed)")
		p.advance()
		return ast.IterBindRef
	}
	return ast.IterBindValue
}
func (p *Parser) looksLikeLockStmt() bool {
	depth := 0
	seenAs := false
	for i := p.pos + 1; p.hasToken(i); i++ {
		tok := p.tokenAt(i)
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_AS:
			if depth == 0 && p.hasToken(i+1) && p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT {
				seenAs = true
			}
		case lexer.TOKEN_COLON:
			return depth == 0 && seenAs
		case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			return false
		}
	}
	return false
}

// looksLikeNurseryStmt matches `nursery:` or `nursery workers(N):` at statement position.
func (p *Parser) looksLikeNurseryStmt() bool {
	if !p.hasToken(p.pos + 1) {
		return false
	}
	next := p.tokenAt(p.pos + 1)
	if next.Kind == lexer.TOKEN_COLON {
		return true
	}
	if next.Kind != lexer.TOKEN_IDENT {
		return false
	}
	if next.Text == "workers" {
		return true
	}
	return next.Text == "on" && p.hasToken(p.pos+5) && p.tokenAt(p.pos+2).Kind == lexer.TOKEN_LPAREN && p.tokenAt(p.pos+3).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+4).Kind == lexer.TOKEN_RPAREN && p.tokenAt(p.pos+5).Kind == lexer.TOKEN_COLON
}

// parseNurseryStmt desugars a structured nursery to the existing pool machinery:
//
//	nursery:                       pool workers(N):
//	    submit a()           ==>       __grp = task_group_new()
//	    submit b()                     task_group_add(&__grp, submit a())
//	                                   task_group_add(&__grp, submit b())
//	                                   wait all __grp
//
// Every `submit` inside the nursery (without an explicit pool) is auto-collected into the
// implicit group, which is waited at scope exit — so all tasks are guaranteed joined before
// the nursery returns (structured concurrency: no leaked/detached work), with no manual
// `wait all` and no pool to name. N defaults to perf_cores() (P-core count on Apple
// Silicon, logical CPUs on Linux/FreeBSD/Windows); `nursery workers(N):` overrides it.
func (p *Parser) parseNurseryStmt() *ast.PoolStmt {
	pos := p.cur().Pos
	p.expectIdentText("nursery")
	var workers ast.Expr
	borrowedPool := ""
	defaultWorkers := false
	if p.peekIdentText("on") {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		borrowedPool = p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_RPAREN)
	} else if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "workers" {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		workers = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	} else {
		defaultWorkers = true
		// Default: query at runtime so Apple-Silicon P-cores, Linux logical CPUs, etc.
		// are automatically picked up rather than a hardcoded fallback.
		workers = &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "perf_cores"}}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.nurseryCounter++
	// NUL is not accepted in source identifiers, so generated names cannot be
	// shadowed by a declaration inside the nursery body.
	poolName := fmt.Sprintf("__nursery_pool_%d\x00", p.nurseryCounter)
	grpName := fmt.Sprintf("__nursery_grp_%d\x00", p.nurseryCounter)
	p.poolScopes = append(p.poolScopes, poolName)
	previousGroup, hadPreviousGroup := p.nurseryGroupByPool[poolName]
	p.nurseryGroupByPool[poolName] = grpName
	body := p.parseBlock()
	if hadPreviousGroup {
		p.nurseryGroupByPool[poolName] = previousGroup
	} else {
		delete(p.nurseryGroupByPool, poolName)
	}
	p.poolScopes = p.poolScopes[:len(p.poolScopes)-1]

	grpDecl := &ast.VarDeclStmt{
		Position: pos,
		Name:     grpName,
		Mutable:  true,
		Type:     &ast.NamedType{Position: pos, Name: "TaskGroup"},
		Value:    &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "task_group_new"}},
	}
	waitAll := &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "task_group_wait_all"},
		Args:     []ast.Expr{nurseryGroupRef(pos, grpName)},
	}}
	fullBody := make([]ast.Stmt, 0, len(body)+2)
	fullBody = append(fullBody, grpDecl)
	fullBody = append(fullBody, body...)
	fullBody = append(fullBody, waitAll)
	return &ast.PoolStmt{Position: pos, Name: poolName, Workers: workers, Body: fullBody, Nursery: true, BorrowedPool: borrowedPool, DefaultWorkers: defaultWorkers}
}

// nurseryGroupRef builds `(&grpName).cast[mutable TaskGroup&]` for task_group_* calls.
func nurseryGroupRef(pos lexer.Pos, grpName string) ast.Expr {
	return &ast.CastExpr{
		Position: pos,
		Operand:  &ast.AddrOfExpr{Position: pos, Operand: &ast.Ident{Position: pos, Name: grpName}},
		Target: &ast.RefType{
			Position: pos,
			Elem:     &ast.NamedType{Position: pos, Name: "TaskGroup"},
			State:    ast.RefStateNonNull,
			Storage:  ast.RefStorageAny,
			Explicit: true,
		},
	}
}

func (p *Parser) parsePoolStmt() *ast.PoolStmt {
	pos := p.cur().Pos
	p.expectIdentText("pool")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	workers := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.poolScopes = append(p.poolScopes, name)
	body := p.parseBlock()
	p.poolScopes = p.poolScopes[:len(p.poolScopes)-1]
	return &ast.PoolStmt{Position: pos, Name: name, Workers: workers, Body: body}
}
func (p *Parser) parseDeferStmt() *ast.DeferStmt {
	pos := p.cur().Pos
	p.expectIdentText("defer")
	modeText := p.expect(lexer.TOKEN_IDENT).Text
	mode := ast.DeferModeBlock
	switch modeText {
	case "block":
		mode = ast.DeferModeBlock
	case "function":
		mode = ast.DeferModeFunction
	default:
		p.errorf("defer expects either `block` or `function`, got %q", modeText)
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.DeferStmt{Position: pos, Mode: mode, Body: body}
}
