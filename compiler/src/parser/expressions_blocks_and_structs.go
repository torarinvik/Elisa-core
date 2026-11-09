package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseLayoutIntrospectionTypeArg() ast.TypeExpr {
	if p.match(lexer.TOKEN_LBRACKET) {
		typ := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return typ
	}
	p.expect(lexer.TOKEN_LPAREN)
	typ := p.parseTypeExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return typ
}

func (p *Parser) parseLayoutIntrospectionFieldArg() lexer.Token {
	if p.peek() == lexer.TOKEN_RPAREN {
		p.errorf("expected field name")
		return lexer.Token{}
	}
	if p.match(lexer.TOKEN_DOT) {
		// Dot-prefixed field names make offset_of[T](.field) read like
		// a field selector without requiring a value expression.
	}
	return p.expect(lexer.TOKEN_IDENT)
}

func (p *Parser) parseBraceMembershipCandidateExpr() ast.Expr {
	start := p.parseExpr()
	if !p.membershipRangeOpAhead() {
		return start
	}
	opKind, opPos := p.consumeValueRangeOp()
	end := p.parseExpr()
	return &ast.MembershipRangeExpr{Position: opPos, Start: start, End: end, Op: opKind}
}
func (p *Parser) parseExprBlockValue(pos lexer.Pos, flattenSingle bool) ast.Expr {
	p.expect(lexer.TOKEN_COLON)
	return p.parseExprBlockBody(pos, flattenSingle)
}

// parseBareExprBlockValue parses the docs/119 §2 bare block-expression form: the
// RHS of `x =` / `x: T =` is NEWLINE+INDENT stmts with a tail expression (no
// `do:` introducer). Caller has verified the NEWLINE+INDENT lookahead.
func (p *Parser) parseBareExprBlockValue(pos lexer.Pos) ast.Expr {
	return p.parseExprBlockBody(pos, false)
}

// bareExprBlockAhead reports whether the RHS position starts a bare block
// expression: the `=` is immediately followed by NEWLINE then INDENT. Today
// that sequence is a guaranteed parse error, so claiming it is backward
// compatible.
func (p *Parser) bareExprBlockAhead() bool {
	return p.peek() == lexer.TOKEN_NEWLINE && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_INDENT
}

// valueCaptureHeaderAhead reports whether the RHS position starts a captured
// block expression (docs/119 §6 applied to value blocks): `|IDENT(, IDENT)*|`
// immediately followed by NEWLINE INDENT. A leading `|` directly after `=`/`<-`
// is a guaranteed parse error today (no prefix `|` operator), so claiming it is
// backward compatible — and requiring the block form keeps the claim airtight.
func (p *Parser) valueCaptureHeaderAhead() bool {
	if p.peek() != lexer.TOKEN_PIPE {
		return false
	}
	i := p.pos + 1
	for {
		if !p.hasToken(i) || p.tokenAt(i).Kind != lexer.TOKEN_IDENT {
			return false
		}
		i++
		if p.hasToken(i) && p.tokenAt(i).Kind == lexer.TOKEN_COMMA {
			i++
			continue
		}
		break
	}
	return p.hasToken(i+2) && p.tokenAt(i).Kind == lexer.TOKEN_PIPE && p.tokenAt(i+1).Kind == lexer.TOKEN_NEWLINE && p.tokenAt(i+2).Kind == lexer.TOKEN_INDENT
}

// parseValueBlockRHS parses the two block-expression RHS forms every `=`/`<-`
// site shares: the docs/119 §2 bare block (`=` NEWLINE INDENT …) and the §6
// captured block (`= |caps|` NEWLINE INDENT …), where the header licenses the
// named outer mutables for mutation inside the block (E4) — the value-block
// analogue of the loop capture header. Returns (nil, false) when neither form
// is ahead so the caller falls through to its normal value-expression parse.
func (p *Parser) parseValueBlockRHS(pos lexer.Pos) (ast.Expr, bool) {
	if p.valueCaptureHeaderAhead() {
		p.expect(lexer.TOKEN_PIPE)
		var captures []string
		for {
			captures = append(captures, p.expect(lexer.TOKEN_IDENT).Text)
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_PIPE)
		value := p.parseBareExprBlockValue(pos)
		if block, ok := value.(*ast.ExprBlock); ok {
			block.Captures = captures
		} else {
			value = &ast.ExprBlock{Position: pos, Captures: captures, Value: value}
		}
		return value, true
	}
	if p.bareExprBlockAhead() {
		return p.parseBareExprBlockValue(pos), true
	}
	return nil, false
}

func (p *Parser) parseExprBlockBody(pos lexer.Pos, flattenSingle bool) ast.Expr {
	p.expectNewline()
	p.exprBlockDepth++
	body := p.parseBlock()
	p.exprBlockDepth--
	if len(body) == 0 {
		p.errorf("expression block requires a final expression statement in the block")
		return &ast.NullLit{Position: pos}
	}
	// docs/119 §4: a trailing `if`/`match` statement is the block's value.
	value, ok := p.tailStmtToExpr(body[len(body)-1])
	if !ok {
		if _, isIf := body[len(body)-1].(*ast.IfStmt); !isIf {
			// ifStmtToExpr already emitted the specific E7 diagnostic for a
			// value `if` without `else`; don't pile the generic error on top.
			p.errorf("expression block requires a final expression statement in the block")
		}
		return &ast.NullLit{Position: pos}
	}
	if flattenSingle && len(body) == 1 {
		return value
	}
	stmts := append([]ast.Stmt(nil), body[:len(body)-1]...)
	return &ast.ExprBlock{Position: pos, Stmts: stmts, Value: value}
}
func (p *Parser) peekStructLiteralTypeArgsFollowedBy(close lexer.TokenKind) bool {
	if p.peek() != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for i := p.pos; p.hasToken(i); i++ {
		switch p.tokenAt(i).Kind {
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
			if depth == 0 {
				return p.hasToken(i+1) && p.tokenAt(i+1).Kind == close
			}
		}
	}
	return false
}
func (p *Parser) parseStructLiteralTypeArgs() []ast.TypeExpr {
	p.expect(lexer.TOKEN_LBRACKET)
	args := make([]ast.TypeExpr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACKET))
	if p.peek() != lexer.TOKEN_RBRACKET {
		for {
			args = append(args, p.parseGenericTypeArgExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACKET)
	return args
}
func (p *Parser) parseStructLiteralBraceFields() ([]ast.Expr, []string, []ast.Expr, ast.Expr) {
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	argNames := make([]string, 0, cap(args))
	// Preserve the non-nil empty representation without allocating backing
	// storage for the overwhelmingly common literal with no spread.
	spreads := []ast.Expr{}
	var copyBase ast.Expr
	if p.peek() == lexer.TOKEN_RBRACE {
		return args, argNames, nil, nil
	}
	for entry := 0; ; entry++ {
		if p.peek() == lexer.TOKEN_RANGE {
			// Copy-update `T{..base, field: value}`: every field not written
			// is taken from `base` (semantic analysis desugars it).
			basePos := p.cur().Pos
			p.advance()
			baseExpr := p.parseExpr()
			switch {
			case copyBase != nil:
				p.errorAt(basePos, "struct literal may have at most one `..base` copy source")
			case entry != 0:
				p.errorAt(basePos, "struct literal copy source `..base` must be the first entry")
			case !isStructCopyBasePath(baseExpr):
				p.errorAt(basePos, "struct literal copy source `..base` must be an identifier or field path (`a.b.c`); bind other expressions to a local first")
			default:
				copyBase = baseExpr
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACE {
				break
			}
			continue
		}
		if p.peek() == lexer.TOKEN_ELLIPSIS {
			spreadPos := p.cur().Pos
			p.advance()
			p.errorAt(spreadPos, "struct-literal spread `Type{...base}` has been removed; use record update `base{field = value}`")
			spreads = append(spreads, p.parseExpr()) // recover so the rest of the literal still parses
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACE {
				break
			}
			continue
		}
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		var value ast.Expr
		if p.match(lexer.TOKEN_COLON) {
			value = p.parseExpr()
		} else {
			value = &ast.Ident{Position: pos, Name: name}
		}
		args = append(args, value)
		argNames = append(argNames, name)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RBRACE {
			break
		}
	}
	return args, argNames, spreads, copyBase
}

// isStructCopyBasePath reports whether expr is an identifier or a plain field
// path `a.b.c`, the only forms accepted as a copy-update `..base` (so the base
// is trivially evaluated once per desugared field read).
func isStructCopyBasePath(expr ast.Expr) bool {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return !strings.Contains(e.Name, "::")
		case *ast.FieldExpr:
			if e.Safe {
				return false
			}
			expr = e.Object
		default:
			return false
		}
	}
}
func (p *Parser) parseStructLiteralParenArgs() ([]ast.Expr, []string) {
	args, argNames, _, _, hasArgForward, _ := p.parseCallArgs()
	if hasArgForward {
		p.errorf("struct constructor arguments do not support call forwarding `..`")
	}
	return args, argNames
}
func (p *Parser) parseRecordUpdateFields() ([]ast.Expr, []string) {
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	argNames := make([]string, 0, cap(args))
	if p.peek() == lexer.TOKEN_RBRACE {
		return args, argNames
	}
	for {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		var value ast.Expr
		if p.match(lexer.TOKEN_ASSIGN) {
			value = p.parseExpr()
		} else {
			value = &ast.Ident{Position: pos, Name: name}
		}
		args = append(args, value)
		argNames = append(argNames, name)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RBRACE {
			break
		}
	}
	return args, argNames
}
func (p *Parser) expectNewline() {
	if p.peek() == lexer.TOKEN_SEMICOLON {
		// `;` as a statement separator has been removed: one statement per line.
		// Directed error, recover as a newline (consume it) so following statements
		// still parse — no cascade. A one-line multi-statement body is almost always
		// a stub; `def f(...) -> T = EXPR` is the clean one-expression spelling.
		p.errorf("`;` statement separator has been removed; put each statement on its own line (or use `def f(...) -> T = EXPR` for a one-expression body)")
		p.advance()
	} else if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	} else if p.peek() == lexer.TOKEN_EOF || p.peek() == lexer.TOKEN_DEDENT {
		// OK at end of file or block
	} else {
		p.errorf("expected newline, got %s", p.cur())
	}
}
func (p *Parser) expectNewlineAfterValueExpr(expr ast.Expr) {
	if _, ok := expr.(*ast.QuantifierExpr); ok && p.pos > 0 && p.tokenAt(p.pos-1).Kind == lexer.TOKEN_DEDENT {
		return
	}

	if p.stmtGuardBlockEnd {
		p.stmtGuardBlockEnd = false
		return
	}
	if _, ok := expr.(*ast.ExprBlock); ok {
		return
	}
	// A catch expression always ends with its arm block (COLON NEWLINE INDENT …
	// DEDENT), so in statement position there is no trailing newline to consume —
	// the same as ExprBlock. Without this, a statement-position `catch …:` followed
	// by another statement fails to parse.
	if _, ok := expr.(*ast.CatchExpr); ok {
		return
	}
	// A `match` used in expression position likewise ends with its arm block
	// (COLON NEWLINE INDENT … DEDENT); the trailing newline was consumed inside the
	// final arm, so there is none left here — same as ExprBlock / CatchExpr.
	if _, ok := expr.(*ast.MatchExpr); ok {
		return
	}
	// A `machine from …:` expression ends with its arm block (COLON NEWLINE INDENT …
	// DEDENT) exactly like `match`; the trailing newline was consumed inside the final
	// arm, so there is none left here.
	if _, ok := expr.(*ast.MachineFromExpr); ok {
		return
	}
	if exprHasBlockRecovery(expr) {
		return
	}
	// A completed value expression followed by `as` is a removed `expr as T` cast (the legitimate
	// `as` uses — is/match aliases, visit/fold, with/lock/mark/export — are consumed within
	// their own parsers before reaching here). Point at the modern cast model and recover.
	if p.peek() == lexer.TOKEN_AS {
		pos := p.cur().Pos
		p.advance()
		_ = p.parseTypeExpr() // discard the cast target for recovery
		p.errorAt(pos, "the `expr as T` cast has been removed; use `expr.cast[T]` (reinterpret), a constructor `T(expr)` / `expr.T()` (value conversion), or `&expr` (reference)")
	}
	p.expectNewline()
}

func (p *Parser) skipRemovedQuestionExprTail() {
	for p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_COMMA {
		p.advance()
	}
}

func exprHasBlockRecovery(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.TryExpr:
		return n.Recovery != nil && n.Recovery.Kind == ast.RecoveryBlock
	case *ast.UnwrapElseExpr:
		return n.Recovery != nil && n.Recovery.Kind == ast.RecoveryBlock
	default:
		return false
	}
}
