package parser

import (
	"strings"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

// ---------- Block / Statements ----------

func (p *Parser) parseBlock() []ast.Stmt {
	p.expect(lexer.TOKEN_INDENT)
	stmts := make([]ast.Stmt, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		stmt := p.parseStmt()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return stmts
}

func (p *Parser) parseStmt() ast.Stmt {
	if p.peek() == lexer.TOKEN_IDENT {
		switch p.cur().Text {
		case "can":
			if p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET) {
				return p.parseCanStmt()
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
		case "params":
			if p.looksLikeLocalParamsStmt() {
				return p.parseLocalParamsStmt()
			}
		case "open":
			if p.looksLikeOpenOrViewStmt() {
				return p.parseOpenStmt()
			}
		case "view":
			if p.looksLikeOpenOrViewStmt() {
				return p.parseViewStmt()
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
	p.expectIdentText("params")
	name := p.expect(lexer.TOKEN_IDENT).Text
	params := p.parseParamDeclBlock(true)
	return &ast.LocalParamsStmt{Position: pos, Name: name, Params: params}
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
	if p.pos+2 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_IDENT, lexer.TOKEN_MUTABLE, lexer.TOKEN_LBRACE:
	default:
		return false
	}
	depth := 0
	seenIn := false
	for i := p.pos + 1; i < len(p.tokens); i++ {
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

func (p *Parser) looksLikeOpenOrViewStmt() bool {
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
			if depth == 0 {
				seenAs = true
			}
		case lexer.TOKEN_COLON:
			if depth == 0 {
				return seenAs
			}
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
		end := p.parseExpr()
		var step ast.Expr
		if p.match(lexer.TOKEN_RANGE) {
			step = p.parseExpr()
		}
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
		return &ast.ForStmt{Position: pos, Reverse: reverse, Name: namePattern.Name, Start: startOrSource, End: end, Step: step, Op: op.Kind, Body: body}
	}
	var filter ast.Expr
	if p.match(lexer.TOKEN_IF) {
		filter = p.parseExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.IterForStmt{Position: pos, Reverse: reverse, Pattern: pattern, Mode: mode, Source: startOrSource, Filter: filter, Body: body}
}

func (p *Parser) parseForHeaderExpr() ast.Expr {
	end := p.pos
	depth := 0
	for end < len(p.tokens) {
		tok := p.tokens[end]
		switch tok.Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_IF, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				subTokens := append([]lexer.Token(nil), p.tokens[p.pos:end]...)
				subTokens = append(subTokens, lexer.Token{Kind: lexer.TOKEN_EOF, Pos: tok.Pos})
				sub := New(subTokens)
				sub.poolScopes = append(sub.poolScopes, p.poolScopes...)
				expr := sub.parseExpr()
				p.errors = append(p.errors, sub.Errors()...)
				p.pos = end
				return expr
			}
		}
		end++
	}
	return p.parseExpr()
}

func unwrapReverseIterableSource(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return expr, false
	}
	if len(call.Args) != 1 || len(call.WithArgs) != 0 || len(call.WithBundles) != 0 || len(call.WithItemOrder) != 0 {
		return expr, false
	}
	if call.ArgName(0) != "" {
		return expr, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || ident.Name != "rev" {
		return expr, false
	}
	return call.Args[0], true
}

func (p *Parser) parseIterLoopPattern() ast.MoveBindPattern {
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindPattern()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COMMA {
		pos := p.cur().Pos
		args := make([]ast.MoveBindArg, 0, 4)
		for {
			tok := p.expect(lexer.TOKEN_IDENT)
			args = append(args, ast.MoveBindArg{Position: tok.Pos, Name: tok.Text})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		return &ast.MoveBindTuplePattern{Position: pos, Args: args}
	}
	if p.peekQualifiedStructDestructurePattern() {
		pos := p.cur().Pos
		typeName := p.parseQualifiedDeclName()
		return p.parseMoveBindStructBracePattern(pos, typeName)
	}
	return p.parseMoveBindPattern()
}

func (p *Parser) looksLikeLetDestructureStmt() bool {
	if p.peek() != lexer.TOKEN_IDENT || p.cur().Text != "let" || p.pos+1 >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.pos+1].Kind {
	case lexer.TOKEN_LBRACE:
		return true
	case lexer.TOKEN_IDENT:
		i := p.pos + 2
		for i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_DOT && p.tokens[i+1].Kind == lexer.TOKEN_IDENT {
			i += 2
		}
		return i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_LBRACE
	default:
		return false
	}
}

func (p *Parser) parseWaitAllStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("wait")
	p.expectIdentText("all")
	target := p.parseExpr()
	p.expectNewline()
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "task_group_wait_all"},
			Args: []ast.Expr{&ast.CastExpr{
				Position: pos,
				Operand:  &ast.AddrOfExpr{Position: pos, Operand: target},
				Target: &ast.RefType{
					Position: pos,
					Elem:     &ast.NamedType{Position: pos, Name: "TaskGroup"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}},
		},
	}
}

func (p *Parser) parseNotifyStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("notify")
	kind := p.expect(lexer.TOKEN_IDENT)
	target := p.parseExpr()
	p.expectNewline()
	callee := "notify_one"
	if kind.Text == "all" {
		callee = "notify_all"
	}
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: callee},
			Args: []ast.Expr{&ast.CastExpr{
				Position: pos,
				Operand:  &ast.AddrOfExpr{Position: pos, Operand: target},
				Target: &ast.RefType{
					Position: pos,
					Elem:     &ast.NamedType{Position: pos, Name: "CondVar"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}},
		},
	}
}

func (p *Parser) parseLockStmt() *ast.LockStmt {
	pos := p.cur().Pos
	p.expectIdentText("lock")
	mutex := p.withAsCastDisabled(p.parseExpr)
	p.expect(lexer.TOKEN_AS)
	guardName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.LockStmt{Position: pos, Mutex: mutex, GuardName: guardName, Body: body}
}

func (p *Parser) parseMatch() *ast.MatchStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_MATCH)
	value := p.withInMembershipDisabled(p.parseExpr)
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.parseExpr()
	}
	arms := p.parseMatchArms()
	return &ast.MatchStmt{Position: pos, Value: value, Store: store, Arms: arms}
}

func (p *Parser) parseInStore() *ast.InStoreStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IN)
	store := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.InStoreStmt{Position: pos, Store: store, Body: body}
}

func (p *Parser) parseOpenStmt() *ast.OpenStmt {
	pos := p.cur().Pos
	p.expectIdentText("open")
	value := p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.withAsCastDisabled(p.parseExpr)
	}
	p.expect(lexer.TOKEN_AS)
	pattern := p.parseMoveBindPattern()
	variantPattern, ok := pattern.(*ast.MoveBindVariantPattern)
	if !ok {
		p.errorf("open requires Enum.Variant(...) payload binding syntax")
		variantPattern = &ast.MoveBindVariantPattern{Position: pos}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.OpenStmt{Position: pos, Value: value, Store: store, Pattern: variantPattern, Body: body}
}

func (p *Parser) parseViewStmt() *ast.ViewStmt {
	pos := p.cur().Pos
	p.expectIdentText("view")
	value := p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	var store ast.Expr
	if p.match(lexer.TOKEN_IN) {
		store = p.withAsCastDisabled(p.parseExpr)
	}
	p.expect(lexer.TOKEN_AS)
	pattern := p.parseViewBindPattern()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.ViewStmt{Position: pos, Value: value, Store: store, Pattern: pattern, Body: body}
}

func (p *Parser) parseViewBindPattern() *ast.ViewBindPattern {
	enumName, variant, pos := p.parseQualifiedVariantTarget()
	p.expect(lexer.TOKEN_LPAREN)
	pattern := &ast.ViewBindPattern{Position: pos, EnumName: enumName, Variant: variant}
	if p.match(lexer.TOKEN_RPAREN) {
		return pattern
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_RPAREN {
		pattern.Name = p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_RPAREN)
		return pattern
	}
	for {
		pattern.Args = append(pattern.Args, p.parseMoveBindVariantArg())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return pattern
}

func (p *Parser) parseCanStmt() *ast.CanStmt {
	pos := p.cur().Pos
	p.expectIdentText("can")
	permissions := p.parsePermissionRefs(false)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CanStmt{Position: pos, Permissions: permissions, Body: body}
}

func (p *Parser) parseArgsScopeItems() ([]ast.WithArg, []ast.ParamPackUse, []ast.ArgsScopeItem) {
	args := make([]ast.WithArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	packs := make([]ast.ParamPackUse, 0, 1)
	items := make([]ast.ArgsScopeItem, 0, cap(args))
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil, nil
	}
	sawPack := false
	sawArg := false
	for {
		if p.matchIdentText("use") {
			if sawPack {
				p.errorf("args-scope parameter-pack application may appear at most once")
			}
			if sawArg {
				p.errorf("args-scope parameter-pack application must appear before ordinary named arguments")
			}
			pack := p.parseValueParamPackUse()
			packs = append(packs, pack)
			items = append(items, ast.ArgsScopeItem{Position: pack.Position, Pack: pack, IsPack: true})
			sawPack = true
		} else {
			pos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			arg := ast.WithArg{Position: pos, Name: name}
			switch {
			case p.match(lexer.TOKEN_ASSIGN):
				arg.Value = p.parseExpr()
			case p.match(lexer.TOKEN_COLON):
				if p.peek() == lexer.TOKEN_COMMA || p.peek() == lexer.TOKEN_RPAREN {
					arg.Value = &ast.Ident{Position: pos, Name: name}
					arg.Shorthand = true
				} else {
					arg.Value = p.parseExpr()
				}
			default:
				p.errorf("expected `:` or `=` after args-scope binding %q", name)
				arg.Value = &ast.Ident{Position: pos, Name: name}
				arg.Shorthand = true
			}
			args = append(args, arg)
			items = append(items, ast.ArgsScopeItem{Position: pos, Arg: arg})
			sawArg = true
		}
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
		if p.peek() == lexer.TOKEN_RPAREN {
			break
		}
	}
	if len(packs) == 0 {
		items = nil
	}
	return args, packs, items
}

func (p *Parser) parseArgsScopeStmt() *ast.ArgsScopeStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WITH)
	p.expectIdentText("args")
	p.expect(lexer.TOKEN_LPAREN)
	args, packs, items := p.parseArgsScopeItems()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.ArgsScopeStmt{Position: pos, Args: args, ParamPacks: packs, ItemOrder: items, Body: body}
}

func (p *Parser) parseWithStmt() *ast.WithStmt {
	pos := p.cur().Pos
	args, bundles, items := p.parseWithValueClause()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.WithStmt{Position: pos, Args: args, Bundles: bundles, WithItemOrder: items, Body: body}
}

func (p *Parser) parseCascadeStmt() *ast.CascadeStmt {
	pos := p.cur().Pos
	p.expectIdentText("cascade")
	target := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.CascadeStmt{Position: pos, Target: target, Body: body}
}

func (p *Parser) parseMatchArms() []ast.MatchArm {
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	arms := make([]ast.MatchArm, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseMatchArm()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	return arms
}

func (p *Parser) parseMatchArm() []ast.MatchArm {
	pos := p.cur().Pos
	patterns := p.parseTopLevelMatchPatterns()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	arms := make([]ast.MatchArm, 0, len(patterns))
	for _, pattern := range patterns {
		arms = append(arms, ast.MatchArm{Position: pos, Pattern: pattern, Body: body})
	}
	return arms
}

func (p *Parser) parseTopLevelMatchPatterns() []ast.MatchPattern {
	patterns := []ast.MatchPattern{p.parseMatchPattern()}
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		patterns = append(patterns, p.parseMatchPattern())
	}
	return patterns
}

func (p *Parser) parseMatchPattern() ast.MatchPattern {
	pattern := p.parseNestedMatchPattern()
	switch pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchVariantPattern, *ast.MatchStructPattern:
		return pattern
	default:
		p.errorf("top-level match arm must use Enum.Variant(...), Struct(...), a string literal, or _")
		return pattern
	}
}

func (p *Parser) parseNestedMatchPattern() ast.MatchPattern {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_DOT {
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseMatchValuePatternExpr()}
	}
	if p.peek() == lexer.TOKEN_STRING_LIT {
		return &ast.MatchStringLiteralPattern{Position: pos, Value: p.advance().Text}
	}
	if p.peek() == lexer.TOKEN_INT_LIT || p.peek() == lexer.TOKEN_FLOAT_LIT || p.peek() == lexer.TOKEN_HEX_LIT ||
		p.peek() == lexer.TOKEN_CHAR_LIT || p.peek() == lexer.TOKEN_TRUE || p.peek() == lexer.TOKEN_FALSE ||
		p.peek() == lexer.TOKEN_NULL || p.peek() == lexer.TOKEN_MINUS || p.peek() == lexer.TOKEN_LPAREN {
		return &ast.MatchLiteralPattern{Position: pos, Value: p.parseMatchValuePatternExpr()}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		return &ast.MatchWildcardPattern{Position: pos}
	}
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	if p.peek() == lexer.TOKEN_LPAREN {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMatchStructPatternAfterName(pos, parts[0])
	}
	if !p.match(lexer.TOKEN_DOT) {
		return &ast.MatchBindPattern{Position: pos, Name: parts[0]}
	}
	parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	name := strings.Join(parts[:len(parts)-1], ".")
	variant := parts[len(parts)-1]
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseMatchPatternArg())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return &ast.MatchVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
}

func buildQualifiedMatchValueExpr(pos lexer.Pos, parts []string) ast.Expr {
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: "<error>"}
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for i := 1; i < len(parts); i++ {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: parts[i]}
	}
	return expr
}

func (p *Parser) parseMatchStructPatternAfterName(pos lexer.Pos, typeName string) ast.MatchPattern {
	brace := false
	open := p.peek()
	close := lexer.TOKEN_RPAREN
	switch open {
	case lexer.TOKEN_LPAREN:
		p.advance()
	case lexer.TOKEN_LBRACE:
		p.advance()
		brace = true
		close = lexer.TOKEN_RBRACE
	default:
		p.errorf("struct pattern expects (...) or {...}")
	}
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(close))
	if p.peek() != close {
		for {
			if brace {
				args = append(args, p.parseBraceMatchStructPatternArg())
			} else {
				args = append(args, p.parseMatchStructPatternArg())
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == close {
				break
			}
		}
	}
	p.expect(close)
	return &ast.MatchStructPattern{Position: pos, TypeName: typeName, Args: args, Brace: brace}
}

func (p *Parser) parseMatchStructPatternArg() ast.MatchPatternArg {
	if p.peek() != lexer.TOKEN_IDENT || p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_COLON {
		pos := p.cur().Pos
		p.errorf("struct pattern fields must use name: pattern")
		pattern := p.parseNestedMatchPattern()
		return ast.MatchPatternArg{Position: pos, Pattern: pattern}
	}
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	pattern := p.parseNestedMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}

func (p *Parser) parseBraceMatchStructPatternArg() ast.MatchPatternArg {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COLON) {
		return ast.MatchPatternArg{
			Position: pos,
			Name:     name,
			Pattern:  &ast.MatchBindPattern{Position: pos, Name: name},
		}
	}
	pattern := p.parseNestedMatchPattern()
	return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
}

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
		pattern := p.parseNestedMatchPattern()
		return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
	}
	pattern := p.parseNestedMatchPattern()
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
	p.expectNewline()
	return &ast.RegionStmt{Position: pos, Name: name, Capacity: capacity}
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

func (p *Parser) parseMark() *ast.MarkStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_AS)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.MarkStmt{Position: pos, RegionName: regionName, Name: name}
}

func (p *Parser) parseCheckpointStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("checkpoint")
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
	regionName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectIdentText("from")
	markName := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreStmt{Position: pos, RegionName: regionName, MarkName: markName}
}

func (p *Parser) parseRestoreCheckpointStmt() *ast.RestoreCheckpointStmt {
	pos := p.cur().Pos
	p.expectIdentText("restore")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.RestoreCheckpointStmt{Position: pos, Name: name}
}

func (p *Parser) parseReset() *ast.ResetStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return &ast.ResetStmt{Position: pos, Name: name}
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_RETURN)
	var value ast.Expr
	if p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		value = p.parseValueExprAllowTuple()
	}
	p.expectNewlineAfterValueExpr(value)
	return &ast.ReturnStmt{Position: pos, Value: value}
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
	value := p.parseValueExprAllowTuple()
	p.expectNewline()
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
	Position lexer.Pos
	Hint     ast.BranchHint
	Cond     ast.Expr
	Value    ast.Expr
	Store    ast.Expr
	Patterns []ast.MatchPattern
	Body     []ast.Stmt
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

	for p.peek() == lexer.TOKEN_ELIF {
		p.advance()
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

func (p *Parser) parseIfClause(isElif bool) ifClause {
	pos := p.cur().Pos
	hint := p.parseBranchHint()
	headStart := p.pos
	head := p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	if p.peek() == lexer.TOKEN_IN && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACKET {
		p.pos = headStart
		cond := p.withAsCastDisabled(p.parseExpr)
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		body := p.parseBlock()
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
		p.expectNewline()
		body := p.parseBlock()
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
		store := p.withAsCastDisabled(p.parseExpr)
		if p.match(lexer.TOKEN_AS) {
			patterns := p.parseTopLevelMatchPatterns()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			body := p.parseBlock()
			return ifClause{Position: pos, Hint: hint, Value: head, Store: store, Patterns: patterns, Body: body}
		}
		if isElif {
			p.errorf("elif pattern binder requires `as Enum.Variant(...)` after store expression")
		} else {
			p.errorf("if pattern binder requires `as Enum.Variant(...)` after store expression")
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return ifClause{Position: pos, Hint: hint, Cond: head, Body: body}
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

func matchPatternContainsBindNames(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case nil, *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return false
	case *ast.MatchBindPattern:
		return p.Name != "" && p.Name != "_"
	case *ast.MatchStructPattern:
		for _, arg := range p.Args {
			if matchPatternContainsBindNames(arg.Pattern) {
				return true
			}
		}
		return false
	case *ast.MatchVariantPattern:
		for _, arg := range p.Args {
			if matchPatternContainsBindNames(arg.Pattern) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (p *Parser) parseGuardStmt() ast.Stmt {
	pos := p.cur().Pos
	p.advance()
	cond := p.parseGuardConditionExpr()
	if guardConditionIntroducesBindings(cond) {
		p.errorf("guard conditions do not support bindings; use `if let ...` or `if ... is Variant(bind)` directly")
	}
	p.expect(lexer.TOKEN_ELSE)
	elseStmt := p.parseStmt()
	return &ast.IfStmt{
		Position: pos,
		Cond:     &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: cond},
		Then:     []ast.Stmt{elseStmt},
	}
}

func lowerIfClauses(clauses []ifClause, elseBlock []ast.Stmt) ast.Stmt {
	tail := elseBlock
	for i := len(clauses) - 1; i >= 0; i-- {
		clause := clauses[i]
		if len(clause.Patterns) != 0 {
			arms := make([]ast.MatchArm, 0, len(clause.Patterns)+1)
			for _, pattern := range clause.Patterns {
				arms = append(arms, ast.MatchArm{Position: pattern.Pos(), Pattern: pattern, Body: clause.Body})
			}
			arms = append(arms, ast.MatchArm{Position: clause.Position, Pattern: &ast.MatchWildcardPattern{Position: clause.Position}, Body: tail})
			tail = []ast.Stmt{&ast.MatchStmt{
				Position: clause.Position,
				Value:    clause.Value,
				Store:    clause.Store,
				Arms:     arms,
			}}
			continue
		}
		tail = []ast.Stmt{&ast.IfStmt{Position: clause.Position, Hint: clause.Hint, Cond: clause.Cond, Then: clause.Body, Else: tail}}
	}
	if len(tail) == 0 {
		return &ast.PassStmt{}
	}
	return tail[0]
}

func (p *Parser) parseWhile() *ast.WhileStmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_WHILE)
	hint := p.parseBranchHint()
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	body := p.parseBlock()
	return &ast.WhileStmt{Position: pos, Hint: hint, Cond: cond, Body: body}
}

func (p *Parser) parseStaticStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)

	if p.peek() == lexer.TOKEN_ERROR {
		p.advance()
		p.expect(lexer.TOKEN_LPAREN)
		msg := p.parseExpr()
		p.expect(lexer.TOKEN_RPAREN)
		p.expectNewline()
		return &ast.StaticErrorStmt{Position: pos, Message: msg}
	}

	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	thenBlock := p.parseBlock()

	var elifs []ast.StaticElifClause
	var elseBlock []ast.Stmt

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseBlock()
			elifs = append(elifs, ast.StaticElifClause{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfStmt{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseMoveBindPattern() ast.MoveBindPattern {
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(p.cur().Pos, "")
	}
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_DOT) {
		parts := []string{name, p.expect(lexer.TOKEN_IDENT).Text}
		for p.match(lexer.TOKEN_DOT) {
			parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
		}
		name = strings.Join(parts[:len(parts)-1], ".")
		variant := parts[len(parts)-1]
		args := make([]ast.MatchPatternArg, 0)
		if p.match(lexer.TOKEN_LPAREN) {
			if p.peek() != lexer.TOKEN_RPAREN {
				for {
					args = append(args, p.parseMoveBindVariantArg())
					if !p.match(lexer.TOKEN_COMMA) {
						break
					}
				}
			}
			p.expect(lexer.TOKEN_RPAREN)
		}
		return &ast.MoveBindVariantPattern{Position: pos, EnumName: name, Variant: variant, Args: args}
	}
	if p.peek() == lexer.TOKEN_LBRACE {
		return p.parseMoveBindStructBracePattern(pos, name)
	}
	if !p.match(lexer.TOKEN_LPAREN) {
		return &ast.MoveBindNamePattern{Position: pos, Name: name}
	}
	args := make([]ast.MoveBindArg, 0)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			argPos := p.cur().Pos
			argName := p.expect(lexer.TOKEN_IDENT).Text
			args = append(args, ast.MoveBindArg{Position: argPos, Name: argName})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.MoveBindStructPattern{Position: pos, TypeName: name, Args: args}
}

func (p *Parser) peekQualifiedStructDestructurePattern() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	i := p.pos + 1
	for i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_DOT && p.tokens[i+1].Kind == lexer.TOKEN_IDENT {
		i += 2
	}
	return i < len(p.tokens) && p.tokens[i].Kind == lexer.TOKEN_LBRACE
}

func (p *Parser) parseMoveBindStructBracePattern(pos lexer.Pos, typeName string) *ast.MoveBindStructPattern {
	p.expect(lexer.TOKEN_LBRACE)
	args := make([]ast.MoveBindArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RBRACE))
	if p.peek() != lexer.TOKEN_RBRACE {
		for {
			args = append(args, p.parseMoveBindStructBraceArg())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
			if p.peek() == lexer.TOKEN_RBRACE {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RBRACE)
	return &ast.MoveBindStructPattern{Position: pos, TypeName: typeName, Args: args, Brace: true}
}

func (p *Parser) parseMoveBindStructBraceArg() ast.MoveBindArg {
	pos := p.cur().Pos
	field := p.expect(lexer.TOKEN_IDENT).Text
	name := field
	if p.match(lexer.TOKEN_COLON) {
		name = p.expect(lexer.TOKEN_IDENT).Text
	}
	return ast.MoveBindArg{Position: pos, Field: field, Name: name}
}

func (p *Parser) parseMoveBindVariantArg() ast.MatchPatternArg {
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_COLON)
		pattern := p.parseMoveBindVariantBindingPattern()
		return ast.MatchPatternArg{Position: pos, Name: name, Pattern: pattern}
	}
	pattern := p.parseMoveBindVariantBindingPattern()
	return ast.MatchPatternArg{Position: pattern.Pos(), Pattern: pattern}
}

func (p *Parser) parseMoveBindVariantBindingPattern() ast.MatchPattern {
	return p.parseNestedMatchPattern()
}

// parseExprOrAssignStmt: handles expressions, assignments, var decls, discards
func (p *Parser) parseExprOrAssignStmt() ast.Stmt {
	pos := p.cur().Pos

	// Discard: _ = expr
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		p.advance() // _
		p.advance() // =
		value := p.parseValueExprAllowTuple()
		p.expectNewline()
		return &ast.DiscardStmt{Position: pos, Value: value}
	}

	if tupleStmt := p.tryParseTupleBindStmt(pos); tupleStmt != nil {
		return tupleStmt
	}

	// Variable declaration: name: [mutable] Type [= value]
	// But NOT name:mutable (no space) which would be field:Type
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
		colonPos := p.pos + 1
		afterColon := lexer.TOKEN_EOF
		if colonPos+1 < len(p.tokens) {
			afterColon = p.tokens[colonPos+1].Kind
		}
		if afterColon == lexer.TOKEN_IDENT || afterColon == lexer.TOKEN_MUTABLE || afterColon == lexer.TOKEN_TAIL ||
			afterColon == lexer.TOKEN_ANY || afterColon == lexer.TOKEN_HEAP || afterColon == lexer.TOKEN_STACK || afterColon == lexer.TOKEN_STATIC {
			name := p.cur().Text
			p.advance()
			p.advance()

			mutable := false
			if p.match(lexer.TOKEN_MUTABLE) {
				mutable = true
			}

			typ := p.parseTypeExpr()

			var value ast.Expr
			if p.match(lexer.TOKEN_ASSIGN) {
				value = p.parseValueExprAllowTuple()
			}
			p.expectNewlineAfterValueExpr(value)
			return &ast.VarDeclStmt{Position: pos, Name: name, Mutable: mutable, Type: typ, Value: value}
		}
	}

	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		name := p.cur().Text
		p.advance()
		p.advance()
		value := p.parseValueExprAllowTuple()
		usedBlockDefault := false
		if p.peek() == lexer.TOKEN_COLON {
			value = p.rewriteGetOrInsertBlockValue(pos, value)
			usedBlockDefault = true
		}
		if !usedBlockDefault {
			p.expectNewlineAfterValueExpr(value)
		}
		return &ast.VarDeclStmt{Position: pos, Name: name, Value: value}
	}

	var expr ast.Expr
	if p.peekIdentText("move") {
		expr = p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	} else {
		expr = p.parseExpr()
	}

	if _, ok := expr.(*ast.MoveExpr); ok {
		var store ast.Expr
		if p.peek() == lexer.TOKEN_IN {
			p.advance()
			store = p.withAsCastDisabled(p.parseExpr)
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
	}

	switch p.peek() {
	case lexer.TOKEN_LARROW:
		p.advance()
		value := p.parseValueExprAllowTuple()
		usedBlockDefault := false
		if p.peek() == lexer.TOKEN_COLON {
			value = p.rewriteGetOrInsertBlockValue(pos, value)
			usedBlockDefault = true
		}
		if !usedBlockDefault {
			p.expectNewlineAfterValueExpr(value)
		}
		return &ast.AssignStmt{Position: pos, Target: expr, Value: value}

	case lexer.TOKEN_QASSIGN:
		p.advance()
		value := p.parseValueExprAllowTuple()
		p.expectNewlineAfterValueExpr(value)
		return &ast.AssignStmt{Position: pos, Target: expr, Value: value, Optional: true}

	case lexer.TOKEN_PLUSEQ, lexer.TOKEN_MINUSEQ, lexer.TOKEN_STAREQ, lexer.TOKEN_SLASHEQ, lexer.TOKEN_PERCENTEQ,
		lexer.TOKEN_CARETEQ, lexer.TOKEN_PIPEEQ, lexer.TOKEN_AMPEQ,
		lexer.TOKEN_LSHIFTEQ, lexer.TOKEN_RSHIFTEQ:
		op := p.advance()
		value := p.parseExpr()
		p.expectNewlineAfterValueExpr(value)
		return &ast.AugAssignStmt{Position: pos, Op: op.Kind, Target: expr, Value: value}

	case lexer.TOKEN_AS:
		p.advance()
		var asKind string
		if p.match(lexer.TOKEN_AMPERSAND) {
			asKind = "&"
		} else if p.match(lexer.TOKEN_BANG) {
			asKind = "!"
		}
		p.expect(lexer.TOKEN_LARROW)
		value := p.parseValueExprAllowTuple()
		p.expectNewlineAfterValueExpr(value)
		return &ast.AsRefAssignStmt{Position: pos, Target: expr, AsKind: asKind, Value: value}
	}

	if ident, ok := expr.(*ast.Ident); ok && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF && p.peek() != lexer.TOKEN_DEDENT {
		arg := p.parseExpr()
		p.expectNewline()
		return &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
			Position: pos,
			Func:     ident,
			Args:     []ast.Expr{arg},
		}}
	}

	p.expectNewline()
	return &ast.ExprStmt{Position: pos, Expr: expr}
}

func (p *Parser) rewriteGetOrInsertBlockValue(pos lexer.Pos, value ast.Expr) ast.Expr {
	call, ok := value.(*ast.CallExpr)
	if !ok || call == nil {
		p.errorf("block default syntax requires a call expression before ':'")
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		_ = p.parseBlock()
		return value
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" {
		p.errorf("block default syntax currently requires a .get_or_insert(...) call before ':'")
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		_ = p.parseBlock()
		return value
	}
	directDictCall := len(call.Args) == 1
	entryDictCall := len(call.Args) == 0 && isDictEntryCallExpr(fieldExpr.Object)
	if !directDictCall && !entryDictCall {
		p.errorf("block default syntax requires either dict.get_or_insert(key) or dict.entry(key).get_or_insert() before ':'")
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		_ = p.parseBlock()
		return value
	}
	defaultExpr := p.parseSingleExprBlockValue()
	call.Args = append(call.Args, defaultExpr)
	return call
}

func isDictEntryCallExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	fieldExpr, ok := call.Func.(*ast.FieldExpr)
	return ok && fieldExpr != nil && fieldExpr.Field == "entry"
}

func (p *Parser) parseSingleExprBlockValue() ast.Expr {
	pos := p.cur().Pos
	return p.parseExprBlockValue(pos, true)
}
