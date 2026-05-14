package parser

import (
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
		case "emit":
			if p.looksLikeSequenceEmitStmt() {
				return p.parseEmitStmt()
			}
		case "guard", "require":
			if p.looksLikeGuardStmt() {
				return p.parseGuardStmt()
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
		case "params", "parameters", "args":
			if p.looksLikeLocalParamsStmt() {
				return p.parseLocalParamsStmt()
			}
		case "bundle":
			if p.looksLikeLocalBundleStmt() {
				return p.parseLocalBundleStmt()
			}
		case "lock":
			if p.looksLikeLockStmt() {
				return p.parseLockStmt()
			}
		case "region":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseRegion()
			}
		case "destroy":
			if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
				return p.parseDestroy()
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
		}
	}
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		return p.parseReturn()
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
		if p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "args" && p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN {
			return p.parseArgsScopeStmt()
		}
		if p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "arena" && p.tokens[p.pos+2].Kind == lexer.TOKEN_IDENT {
			return p.parseWithArenaStmt()
		}
		return p.parseWithStmt()
	case lexer.TOKEN_WHILE:
		return p.parseWhile()
	case lexer.TOKEN_STATIC:
		return p.parseStaticStmt()
	default:
		return p.parseExprOrAssignStmt()
	}
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
func (p *Parser) looksLikeLocalParamsStmt() bool {
	return p.pos+2 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+2].Kind == lexer.TOKEN_COLON
}
func (p *Parser) parseLocalParamsStmt() *ast.LocalParamsStmt {
	pos := p.cur().Pos
	syntax := p.cur().Text
	if p.peek() == lexer.TOKEN_IDENT && (p.cur().Text == "params" || p.cur().Text == "parameters" || p.cur().Text == "args") {
		p.advance()
	} else {
		p.expectIdentText("params")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	params := p.parseParamDeclBlock(true)
	stmt := &ast.LocalParamsStmt{Position: pos, Name: name, Params: params}
	if syntax != "args" {
		stmt.DeprecatedSyntax = syntax + " " + name + ":"
		stmt.DeprecatedReplacement = "args " + name + ":"
	}
	return stmt
}
func (p *Parser) looksLikeLocalBundleStmt() bool {
	return p.pos+3 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT &&
		p.tokens[p.pos+2].Kind == lexer.TOKEN_IDENT &&
		p.tokens[p.pos+3].Kind == lexer.TOKEN_COLON
}
func (p *Parser) parseLocalBundleStmt() *ast.LocalParamsStmt {
	pos := p.cur().Pos
	p.expectIdentText("bundle")
	name := p.expect(lexer.TOKEN_IDENT).Text
	mode := p.expect(lexer.TOKEN_IDENT).Text
	if mode != "explicit" {
		p.errorf("local bundle declarations only support `explicit` mode, got %q", mode)
	}
	params := p.parseParamDeclBlock(true)
	return &ast.LocalParamsStmt{Position: pos, Name: name, Params: params, DeprecatedSyntax: "bundle " + name + " explicit:", DeprecatedReplacement: "args " + name + ":"}
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
func (p *Parser) parseIterBindMode() ast.IterBindMode {
	if p.match(lexer.TOKEN_MUTABLE) {
		if !p.peekIdentText("ref") {
			p.errorf("for mutable binder expects `mutable ref`")
			return ast.IterBindMutableRef
		}
		p.advance()
		return ast.IterBindMutableRef
	}
	if p.peekIdentText("ref") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_IN {
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
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT {
		namePattern, ok := pattern.(*ast.MoveBindNamePattern)
		if !ok {
			p.errorf("range for loop requires a simple loop name")
			namePattern = &ast.MoveBindNamePattern{Position: pos, Name: "_"}
		}
		if mode != ast.IterBindValue {
			p.errorf("range for loop does not support ref binders")
		}
		op := p.advance()
		end := p.parseForHeaderExpr()
		var step ast.Expr
		if p.match(lexer.TOKEN_RANGE) {
			step = p.parseForHeaderExpr()
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
	body := p.parseForStmtBody()
	return &ast.IterForStmt{Position: pos, Reverse: reverse, Pattern: pattern, Mode: mode, Source: startOrSource, PatternFilter: patternFilter, PatternFilterSubject: patternFilterSubject, WhereFilter: whereFilter, Filter: filter, Body: body}
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
