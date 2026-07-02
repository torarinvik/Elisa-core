package parser

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseBlock() []ast.Stmt {
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
	if p.peek() == lexer.TOKEN_IDENT {
		switch p.cur().Text {
		case "assert":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_QUESTION {
				return p.parseAssertHoleStmt()
			}
			// `assert COND by:` — a Dafny-style proof-carrying assert (the `by:` proof block is
			// verification-only). Only matched when a top-level `by:` opener is present on this logical
			// line; a plain `assert(COND)` call has no `by:` and falls through to expression parsing.
			if p.looksLikeAssertByStmt() {
				return p.parseAssertByStmt()
			}
		case "proof":
			if p.looksLikeProofBlockStmt() {
				return p.parseProofBlockStmt()
			}
		case "use":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseProofUseStmt()
			}
		case "can":
			if p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET) {
				return p.parseCanStmt()
			}
		case "trusted":
			if p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET) {
				return p.parseTrustedStmt()
			}
		case "signal":
			if p.looksLikeSignalStmt() {
				return p.parseSignalStmt()
			}
		case "wait":
			if p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "all" {
				return p.parseWaitAllStmt()
			}
		case "notify":
			if p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && (p.tokens[p.pos+1].Text == "one" || p.tokens[p.pos+1].Text == "all") {
				return p.parseNotifyStmt()
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
				proof, scoped := p.parseOptionalContractScopedProof()
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
				proof, scoped := p.parseOptionalContractScopedProof()
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
				p.expectNewline()
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
			if p.looksLikeForStmt() {
				return p.parseForStmt()
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
		case "lock":
			if p.looksLikeLockStmt() {
				return p.parseLockStmt()
			}
		case "ghost":
			if p.looksLikeGhostStmt() {
				return p.parseGhostStmt()
			}
		case "region":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseRegion()
			}
		case "destroy":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseDestroy()
			}
		case "leak":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseLeak()
			}
		case "mark":
			if p.pos+3 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_AS && p.tokens[p.pos+3].Kind == lexer.TOKEN_IDENT {
				return p.parseMark()
			}
		case "checkpoint":
			if p.looksLikeCheckpointStmt() {
				return p.parseCheckpointStmt()
			}
		case "restore":
			if p.pos+3 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Text == "from" && p.tokens[p.pos+3].Kind == lexer.TOKEN_IDENT {
				return p.parseRestore()
			}
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseRestoreCheckpointStmt()
			}
		case "reset":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseReset()
			}
		case "promote":
			if p.looksLikePromoteStmt() {
				return p.parsePromote()
			}
		case "adopt":
			if p.pos+3 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Text == "into" && p.tokens[p.pos+3].Kind == lexer.TOKEN_IDENT {
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
		// Postfix guard: `break if <cond>` desugars to `if <cond>: break` (mirrors the
		// postfix `return if` form). No new AST node — the loop lowering is unchanged.
		if p.match(lexer.TOKEN_IF) {
			cond := p.parseExpr()
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
			cond := p.parseExpr()
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
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	switch next.Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE:
		// `ghost x ...` / `ghost mutable ...` — but reject `ghost mutable` as a standalone if it is
		// really `ghost` being assigned (cannot happen: MUTABLE can't follow a value name). Accept.
		return true
	case lexer.TOKEN_COLON:
		// Block form only when a newline (then indent) follows the colon.
		if p.pos+2 < len(p.tokens) {
			return p.tokens[p.pos+2].Kind == lexer.TOKEN_NEWLINE
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
	if p.pos+1 >= len(p.tokens) {
		return true
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_DOT:
		return false
	default:
		return true
	}
}
func (p *Parser) looksLikeSignalStmt() bool {
	if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if p.pos+2 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT || p.tokens[p.pos+2].Kind != lexer.TOKEN_LPAREN {
		return false
	}
	depth := 0
	for i := p.pos + 2; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && tok.Text == "by" && i+1 < len(p.tokens) {
				// `by:` or the closed-world `by scoped:` (docs/99) both open a proof block.
				if p.tokens[i+1].Kind == lexer.TOKEN_COLON {
					return true
				}
				if p.tokens[i+1].Kind == lexer.TOKEN_IDENT && p.tokens[i+1].Text == "scoped" &&
					i+2 < len(p.tokens) && p.tokens[i+2].Kind == lexer.TOKEN_COLON {
					return true
				}
				if p.tokens[i+1].Kind == lexer.TOKEN_IDENT && p.tokens[i+1].Text == "scoped" &&
					i+3 < len(p.tokens) && p.tokens[i+2].Kind == lexer.TOKEN_COLON &&
					p.tokens[i+3].Kind == lexer.TOKEN_IDENT && isScopedProofTheory(p.tokens[i+3].Text) {
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
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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

func (p *Parser) parseOptionalContractScopedProof() ([]ast.Stmt, bool) {
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
	p.expectNewline()
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
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT {
		return false
	}
	mode := p.tokens[p.pos+1].Text
	if mode != "block" && mode != "function" {
		return false
	}
	return p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
}
func (p *Parser) looksLikeParallelForStmt() bool {
	if p.pos+4 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_IDENT || p.tokens[p.pos+1].Text != "for" {
		return false
	}
	if p.tokens[p.pos+2].Kind != lexer.TOKEN_IDENT {
		return false
	}
	sourcePos := p.pos + 3
	if p.tokens[sourcePos].Kind == lexer.TOKEN_IDENT && p.tokens[sourcePos].Text == "at" {
		if p.pos+6 >= len(p.tokens) {
			return false
		}
		if p.tokens[p.pos+4].Kind != lexer.TOKEN_IDENT {
			return false
		}
		if p.tokens[p.pos+5].Kind != lexer.TOKEN_IN {
			return false
		}
		sourcePos = p.pos + 6
	} else if p.tokens[sourcePos].Kind != lexer.TOKEN_IN {
		return false
	}
	depth := 0
	for i := sourcePos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_ASSIGN {
		return true
	}
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if pos < 0 || pos+2 >= len(p.tokens) {
		return false
	}
	if p.tokens[pos].Kind != lexer.TOKEN_IDENT || p.tokens[pos].Text != "for" {
		return false
	}
	switch p.tokens[pos+1].Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_LBRACE:
	default:
		return false
	}
	depth := 0
	seenIn := false
	for i := pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
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
	if p.peekIdentText("ref") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_IN {
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
	for i := p.pos + 1; i < len(p.tokens); i++ {
		tok := p.tokens[i]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_AS:
			if depth == 0 && i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_IDENT {
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
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	if next.Kind == lexer.TOKEN_COLON {
		return true
	}
	return next.Kind == lexer.TOKEN_IDENT && next.Text == "workers"
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
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "workers" {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		workers = p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
	} else {
		// Default: query at runtime so Apple-Silicon P-cores, Linux logical CPUs, etc.
		// are automatically picked up rather than a hardcoded fallback.
		workers = &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "perf_cores"}}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.nurseryCounter++
	poolName := fmt.Sprintf("__nursery_pool_%d", p.nurseryCounter)
	grpName := fmt.Sprintf("__nursery_grp_%d", p.nurseryCounter)
	p.poolScopes = append(p.poolScopes, poolName)
	p.nurseryGroupByPool[poolName] = grpName
	body := p.parseBlock()
	delete(p.nurseryGroupByPool, poolName)
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
	return &ast.PoolStmt{Position: pos, Name: poolName, Workers: workers, Body: fullBody}
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
func (p *Parser) parseParallelForStmt() *ast.ParallelForStmt {
	pos := p.cur().Pos
	p.expectIdentText("parallel")
	p.expectIdentText("for")
	name := p.expect(lexer.TOKEN_IDENT).Text
	indexName := ""
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "at" {
		p.advance()
		indexName = p.expect(lexer.TOKEN_IDENT).Text
	}
	p.expect(lexer.TOKEN_IN)
	source := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.ParallelForStmt{Position: pos, Name: name, IndexName: indexName, Source: source, Body: body}
}
func (p *Parser) parseForStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("for")
	reverse := false
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "rev" && p.pos+1 < len(p.tokens) {
		nextKind := p.tokens[p.pos+1].Kind
		if nextKind == lexer.TOKEN_IDENT || nextKind == lexer.TOKEN_MUTABLE {
			p.errorf("legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported; use `for ... in rev(...):` instead")
			reverse = true
			p.advance()
		}
	}
	mode := p.parseIterBindMode()
	pattern := p.parseIterLoopPattern()
	p.expect(lexer.TOKEN_IN)
	startOrSource := p.parseForHeaderExpr()
	if iterSource, sourceReverse := unwrapReverseIterableSource(startOrSource); sourceReverse {
		if reverse {
			p.errorf("reverse iterable loop specified twice; use either `for rev ... in ...:` or `for ... in rev(...):`")
		}
		reverse = true
		startOrSource = iterSource
	}
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT || p.peek() == lexer.TOKEN_RANGE_LE {
		namePattern, ok := pattern.(*ast.MoveBindNamePattern)
		if !ok {
			p.errorf("range for loop requires a simple loop name")
			namePattern = &ast.MoveBindNamePattern{Position: pos, Name: "_"}
		}
		if mode != ast.IterBindValue {
			p.errorf("range for loop does not support ref binders")
		}
		op := p.advance()
		if op.Kind == lexer.TOKEN_RANGE {
			// Bare `lo .. hi` as a range is ambiguous about its endpoint (historically inclusive in
			// Elisa, but exclusive in Rust/Python/Swift — a footgun either way). Require an explicit
			// operator. Parsing recovers as the historical inclusive form so a single clear error is
			// reported rather than a cascade.
			p.errorAt(op.Pos, ambiguousBareRangeMsg)
			op.Kind = lexer.TOKEN_RANGE_LE
		}
		end := p.parseForHeaderExpr()
		if op.Kind == lexer.TOKEN_RANGE_LE {
			// Inclusive `lo ..= hi` desugars to the half-open `lo ..< (hi + 1)`, so everything
			// downstream (step, `by par`, the ForStmt lowering) treats it as the existing `..<` form.
			end = &ast.BinaryExpr{Position: op.Pos, Op: lexer.TOKEN_PLUS, Left: end, Right: &ast.IntLit{Position: op.Pos, Value: "1"}}
			op.Kind = lexer.TOKEN_RANGE_LT
		}
		var step ast.Expr
		if p.match(lexer.TOKEN_RANGE) {
			step = p.parseForHeaderExpr()
		} else if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "step" {
			// `for i in lo..<hi step N:` was never syntax — it used to be silently
			// truncated (the loop stepped by 1). Directed error, recover as the
			// canonical range stride `lo..<hi..N` so the loop parses with the
			// intended step and no cascade.
			p.errorf("`step N` is not range syntax; spell the stride on the range: `lo..<hi..N`")
			p.advance()
			step = p.parseForHeaderExpr()
		}
		// Optional `by par` data-parallel marker on a range for-loop: lowers to the runtime
		// `for_indices_par` combinator (disjoint index bands over a structured nursery). `by simd`
		// is rejected -- recognized loop shapes vectorize by default (the -Wperf verifier checks).
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
			byPos := p.cur().Pos
			p.advance()
			mode := p.expect(lexer.TOKEN_IDENT).Text
			switch mode {
			case "par":
				if reverse {
					p.errorAt(byPos, "`by par` is not supported on a reverse range loop")
				}
				if step != nil {
					p.errorAt(byPos, "`by par` is not supported on a stepped range loop")
				}
				if op.Kind != lexer.TOKEN_RANGE_LT {
					p.errorAt(byPos, "`by par` requires a half-open `..<` range loop")
				}
				body := p.parseForStmtBody()
				return p.buildParallelIndexFor(pos, namePattern.Name, startOrSource, end, body)
			case "simd":
				p.errorAt(byPos, "`by simd` was removed; recognized loop shapes vectorize by default (a non-vectorizable shape warns under -Wperf)")
			default:
				p.errorAt(byPos, "expected `par` after `by`, got %q", mode)
			}
		}
		body := p.parseForStmtBody()
		return &ast.ForStmt{Position: pos, Reverse: reverse, Name: namePattern.Name, Start: startOrSource, End: end, Step: step, Op: op.Kind, Body: body}
	}
	var patternFilter ast.MatchPattern
	var patternFilterSubject string
	var whereFilter ast.Expr
	if p.matchIdentText("where") {
		if subject, ok := p.peekForWhereSubjectPattern(pattern); ok {
			patternFilterSubject = subject
			p.expect(lexer.TOKEN_IDENT)
			p.expect(lexer.TOKEN_IS)
			patternFilter = p.parseMatchPattern()
			if p.peek() == lexer.TOKEN_COLON && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_NEWLINE {
				p.advance()
				whereFilter = p.parseForHeaderExpr()
			}
		} else if p.peekForWherePatternFilter() {
			patternFilter = p.parseMatchPattern()
			if p.peek() == lexer.TOKEN_COLON && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_NEWLINE {
				p.advance()
				whereFilter = p.parseForHeaderExpr()
			}
		} else {
			whereExpr := p.parseForHeaderExpr()
			if p.isForWherePredicateShorthand(whereExpr) {
				startOrSource = &ast.CallExpr{
					Position: pos,
					Func:     &ast.Ident{Position: pos, Name: "where"},
					Args:     []ast.Expr{startOrSource, whereExpr},
				}
			} else {
				whereFilter = whereExpr
			}
		}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	// Optional `by par` data-parallel marker on a collection for-loop: lowers to the same
	// `for_indices_par` disjoint-band machinery as the range form, indexing the source through a
	// thread-shareable Slice (so reading each element is race-free) and binding the loop name to
	// the band's element. `by simd` is rejected; recognized loop shapes vectorize by default.
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "by" {
		byPos := p.cur().Pos
		p.advance()
		marker := p.expect(lexer.TOKEN_IDENT).Text
		switch marker {
		case "par":
			namePattern, ok := pattern.(*ast.MoveBindNamePattern)
			if !ok {
				p.errorAt(byPos, "`by par` collection loop requires a simple loop name")
				namePattern = &ast.MoveBindNamePattern{Position: pos, Name: "_"}
			}
			if reverse {
				p.errorAt(byPos, "`by par` is not supported on a reverse loop")
			}
			if mode != ast.IterBindValue {
				p.errorAt(byPos, "`by par` collection loop does not support ref binders")
			}
			if patternFilter != nil || whereFilter != nil || filter != nil {
				p.errorAt(byPos, "`by par` is not supported on a filtered loop (`where`/`if`)")
			}
			body := p.parseForStmtBody()
			return p.buildParallelCollectionFor(pos, namePattern.Name, startOrSource, body)
		case "simd":
			p.errorAt(byPos, "`by simd` was removed; recognized loop shapes vectorize by default (a non-vectorizable shape warns under -Wperf)")
		default:
			p.errorAt(byPos, "expected `par` after `by`, got %q", marker)
		}
	}
	body := p.parseForStmtBody()
	// `for x in move c:` is a consuming move-drain. The `move c` source parses as a MoveExpr;
	// unwrap it to the container and flag MovedSource (Mode stays IterBindValue so the binding is
	// by value and backend lowering is reused). Only the value-binding form supports a moved
	// source — `for ref x in move c` / `for mutable x in move c` is contradictory.
	source := startOrSource
	movedSource := false
	if moveExpr, ok := source.(*ast.MoveExpr); ok {
		if mode != ast.IterBindValue {
			p.errorAt(moveExpr.Pos(), "`for ref`/`for mutable` cannot bind a moved source; `move` requires a by-value drain")
		}
		source = moveExpr.Operand
		movedSource = true
	}
	return &ast.IterForStmt{Position: pos, Reverse: reverse, Pattern: pattern, Mode: mode, Source: source, MovedSource: movedSource, PatternFilter: patternFilter, PatternFilterSubject: patternFilterSubject, WhereFilter: whereFilter, Filter: filter, Body: body}
}

func (p *Parser) peekForWhereSubjectPattern(pattern ast.MoveBindPattern) (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_IS {
		return "", false
	}
	subject := p.cur().Text
	switch bind := pattern.(type) {
	case *ast.MoveBindNamePattern:
		return subject, bind.Name != "" && bind.Name == subject
	case *ast.MoveBindTuplePattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	case *ast.MoveBindStructPattern:
		for _, arg := range bind.Args {
			if arg.Name == subject && subject != "_" {
				return subject, true
			}
		}
	}
	return "", false
}

// buildParallelIndexFor lowers `for <name> in <start> ..< <end> by par: <body>` into a call to the
// runtime `for_indices_par(count, lambda(idx) => { ...; 0 })` combinator, which partitions [0,count)
// into disjoint index bands on a structured nursery. Race-safety is the same as the Slice-band path:
// each global index is visited exactly once, so writing one's own `out[i]` slot cannot collide.
// Sharing a non-thread-shareable value (e.g. a darray) into the body is still rejected at the
// lambda/nursery transfer boundary. When the range starts at integer 0 the loop name binds the band
// index directly; otherwise the name is rebound to `start + idx` inside the body.
func (p *Parser) buildParallelIndexFor(pos lexer.Pos, name string, start ast.Expr, end ast.Expr, body []ast.Stmt) ast.Stmt {
	startIsZero := false
	if lit, ok := start.(*ast.IntLit); ok && lit.Value == "0" {
		startIsZero = true
	}
	usizeT := &ast.NamedType{Position: pos, Name: "usize"}
	idxName := name
	var lambdaBody []ast.Stmt
	if !startIsZero {
		idxName = "__by_par_off"
		// name: usize = start + __by_par_off
		lambdaBody = append(lambdaBody, &ast.VarDeclStmt{
			Position: pos, Name: name, Type: usizeT,
			Value: &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS,
				Left: start, Right: &ast.Ident{Position: pos, Name: idxName}},
		})
	}
	lambdaBody = append(lambdaBody, body...)
	lambdaBody = append(lambdaBody, &ast.ReturnStmt{Position: pos, Value: &ast.IntLit{Position: pos, Value: "0"}})
	lambda := &ast.LambdaExpr{
		Position: pos, Keyword: "lambda", ParallelBody: true,
		Params:     []ast.ParamDecl{{Position: pos, Name: idxName, Type: usizeT}},
		ReturnType: &ast.NamedType{Position: pos, Name: "i64"},
		Body:       lambdaBody,
	}
	var count ast.Expr = end
	if !startIsZero {
		count = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Left: end, Right: start}
	}
	call := &ast.CallExpr{Position: pos,
		Func: &ast.Ident{Position: pos, Name: "for_indices_par"},
		Args: []ast.Expr{count, lambda}}
	return &ast.ExprStmt{Position: pos, Expr: call}
}

// buildParallelCollectionFor lowers `for <name> in <src> by par: <body>` over a darray (or Slice)
// `src` onto the same disjoint-band machinery as the range form. It first borrows the source's index
// space as a thread-shareable Slice (`__by_par_src = as_slice(src)`), then runs
// `for_indices_par(__by_par_src.len(), lambda(idx) => { <name> = __by_par_src.get(idx); ...; 0 })`.
// Each global index is visited exactly once across the bands, so reading `src[idx]` is race-free; the
// Slice is all-scalar so capturing it for the read is thread-safe. Capturing a non-partitioned
// darray for OUTPUT is still rejected by the parallel-body data-race guard. A `[]Stmt{decl, expr}`
// scope is returned so `__by_par_src` is hoisted once before the parallel region.
func (p *Parser) buildParallelCollectionFor(pos lexer.Pos, name string, src ast.Expr, body []ast.Stmt) ast.Stmt {
	usizeT := &ast.NamedType{Position: pos, Name: "usize"}
	p.byParSeq++
	srcName := fmt.Sprintf("__by_par_src_%d", p.byParSeq)
	srcDecl := &ast.VarDeclStmt{
		Position: pos, Name: srcName,
		Value: &ast.CallExpr{Position: pos,
			Func: &ast.Ident{Position: pos, Name: "as_slice"},
			Args: []ast.Expr{src}},
	}
	idxName := "__by_par_idx"
	var lambdaBody []ast.Stmt
	// <name> = __by_par_src.get(__by_par_idx)
	lambdaBody = append(lambdaBody, &ast.VarDeclStmt{
		Position: pos, Name: name,
		Value: &ast.CallExpr{Position: pos,
			Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcName}, Field: "get"},
			Args: []ast.Expr{&ast.Ident{Position: pos, Name: idxName}}},
	})
	lambdaBody = append(lambdaBody, body...)
	lambdaBody = append(lambdaBody, &ast.ReturnStmt{Position: pos, Value: &ast.IntLit{Position: pos, Value: "0"}})
	lambda := &ast.LambdaExpr{
		Position: pos, Keyword: "lambda", ParallelBody: true,
		Params:     []ast.ParamDecl{{Position: pos, Name: idxName, Type: usizeT}},
		ReturnType: &ast.NamedType{Position: pos, Name: "i64"},
		Body:       lambdaBody,
	}
	count := &ast.CallExpr{Position: pos,
		Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcName}, Field: "len"},
		Args: nil}
	call := &ast.CallExpr{Position: pos,
		Func: &ast.Ident{Position: pos, Name: "for_indices_par"},
		Args: []ast.Expr{count, lambda}}
	// The `__by_par_src` Slice decl must precede the parallel loop; emit it first and buffer the
	// loop call as a pending statement so both land flat in the enclosing block (the parser drains
	// pendingStmts right after this statement, preserving decl-then-loop order).
	p.pendingStmts = append(p.pendingStmts, &ast.ExprStmt{Position: pos, Expr: call})
	return srcDecl
}

func (p *Parser) parseForStmtBody() []ast.Stmt {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
		return []ast.Stmt{p.parseForStmt()}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	return p.parseBlock()
}

func (p *Parser) peekForWherePatternFilter() bool {
	switch p.peek() {
	case lexer.TOKEN_DOT, lexer.TOKEN_CARET, lexer.TOKEN_STRING_LIT, lexer.TOKEN_INT_LIT, lexer.TOKEN_FLOAT_LIT,
		lexer.TOKEN_HEX_LIT, lexer.TOKEN_CHAR_LIT, lexer.TOKEN_TRUE, lexer.TOKEN_FALSE, lexer.TOKEN_NULL,
		lexer.TOKEN_MINUS, lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
		return true
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "_" {
			return true
		}
		if p.pos+1 >= len(p.tokens) {
			return false
		}
		next := p.tokens[p.pos+1].Kind
		if next == lexer.TOKEN_LPAREN || next == lexer.TOKEN_LBRACE {
			return forWhereIdentLooksLikePatternType(p.cur().Text)
		}
		if next == lexer.TOKEN_DOT {
			index := p.pos + 1
			for index+1 < len(p.tokens) && p.tokens[index].Kind == lexer.TOKEN_DOT && p.tokens[index+1].Kind == lexer.TOKEN_IDENT {
				index += 2
			}
			if index >= len(p.tokens) {
				return false
			}
			switch p.tokens[index].Kind {
			case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACE:
				return true
			case lexer.TOKEN_COLON:
				return forWhereIdentLooksLikePatternType(p.cur().Text)
			case lexer.TOKEN_IDENT:
				return p.tokens[index].Text == "for" && forWhereIdentLooksLikePatternType(p.cur().Text)
			default:
				return false
			}
		}
		return false
	default:
		return false
	}
}

func (p *Parser) isForWherePredicateShorthand(expr ast.Expr) bool {
	_, ok := expr.(*ast.Ident)
	return ok
}

func forWhereIdentLooksLikePatternType(name string) bool {
	if name == "" {
		return false
	}
	ch := name[0]
	return ch >= 'A' && ch <= 'Z'
}

// looksLikeUsesStmt reports whether a leading `uses` is a named-contract application `uses Name(`
// (docs/97) rather than a variable named `uses`.
func (p *Parser) looksLikeUsesStmt() bool {
	return p.pos+2 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT &&
		(p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN || p.tokens[p.pos+2].Kind == lexer.TOKEN_LBRACKET)
}

// looksLikeContractStmt reports whether a leading `requires` is a value-contract precondition
// rather than an identifier named "requires" being assigned/typed. It is a contract unless the next
// token ends the line or begins an assignment/binding (=, :, <-).
func (p *Parser) looksLikeContractStmt() bool {
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_ASSIGN, lexer.TOKEN_COLON, lexer.TOKEN_LARROW:
		return false
	}
	return true
}
