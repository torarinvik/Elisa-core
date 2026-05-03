package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

func matchPatternContainsBindNames(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case nil, *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return false
	case *ast.MatchBindPattern:
		return p.Name != "" && p.Name != "_"
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			if matchPatternContainsBindNames(elem) {
				return true
			}
		}
		return false
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			if matchPatternContainsBindNames(elem) {
				return true
			}
		}
		return false
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
func (p *Parser) looksLikeExpectPatternStmt() bool {
	if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "let" {
		depth := 0
		for i := p.pos + 2; i < len(p.tokens); i++ {
			tok := p.tokens[i]
			switch tok.Kind {
			case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
				depth++
			case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
				if depth > 0 {
					depth--
				}
			case lexer.TOKEN_ASSIGN:
				if depth == 0 {
					return true
				}
			case lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
				if depth == 0 {
					return false
				}
			}
		}
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
		case lexer.TOKEN_AS:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_ASSIGN, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}
func (p *Parser) parseExpectPatternStmt() ast.Stmt {
	pos := p.cur().Pos
	p.expectIdentText("expect")
	var value ast.Expr
	var patterns []ast.MatchPattern
	if p.matchIdentText("let") {
		patterns = p.parseTopLevelMatchPatterns()
		p.expect(lexer.TOKEN_ASSIGN)
		value = p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
	} else {
		value = p.withInMembershipDisabled(func() ast.Expr { return p.withAsCastDisabled(p.parseExpr) })
		p.expect(lexer.TOKEN_AS)
		patterns = p.parseTopLevelMatchPatterns()
	}
	var body []ast.Stmt
	hasBody := false
	if p.match(lexer.TOKEN_COLON) {
		hasBody = true
		p.expectNewline()
		body = p.parseBlock()
	} else {
		p.expectNewline()
	}
	if !hasBody {
		return &ast.ExpectPatternStmt{Position: pos, Value: value, Patterns: patterns}
	}
	if len(body) == 0 {
		body = []ast.Stmt{&ast.PassStmt{Position: pos}}
	}

	arms := make([]ast.MatchArm, 0, len(patterns)+1)
	for _, pattern := range patterns {
		arms = append(arms, ast.MatchArm{
			Position: pattern.Pos(),
			Pattern:  pattern,
			Body:     body,
		})
	}
	arms = append(arms, ast.MatchArm{
		Position: pos,
		Pattern:  &ast.MatchWildcardPattern{Position: pos},
		Body: []ast.Stmt{&ast.PanicStmt{
			Position: pos,
			Message:  &ast.StringLit{Position: pos, Value: "expect pattern failed"},
		}},
	})
	return &ast.MatchStmt{Position: pos, Value: value, Arms: arms}
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
				Position:                       clause.Position,
				Value:                          clause.Value,
				Store:                          clause.Store,
				Arms:                           arms,
				DeprecatedIfStorePatternBinder: clause.Store != nil,
			}}
			continue
		}
		if len(clause.OptionalBindings) != 0 {
			tail = []ast.Stmt{buildOptionalIfLetChain(clause.Position, clause.OptionalBindings, clause.Cond, clause.Body, tail, 0)}
			continue
		}
		tail = []ast.Stmt{&ast.IfStmt{Position: clause.Position, Hint: clause.Hint, Cond: clause.Cond, Then: clause.Body, Else: tail}}
	}
	if len(tail) == 0 {
		return &ast.PassStmt{}
	}
	return tail[0]
}
func buildOptionalIfLetChain(pos lexer.Pos, bindings []optionalReturnWithBinding, cond ast.Expr, body []ast.Stmt, elseBlock []ast.Stmt, index int) ast.Stmt {
	if index >= len(bindings) {
		if cond != nil {
			return &ast.IfStmt{Position: pos, Cond: cond, Then: body, Else: elseBlock}
		}
		if len(body) == 1 {
			return body[0]
		}
		return &ast.IfStmt{Position: pos, Cond: &ast.BoolLit{Position: pos, Value: true}, Then: body, Else: elseBlock}
	}
	binding := bindings[index]
	return &ast.IfStmt{
		Position: pos,
		Cond:     &ast.OptionalBindExpr{Position: pos, Name: binding.name, Value: binding.value},
		Then:     []ast.Stmt{buildOptionalIfLetChain(pos, bindings, cond, body, elseBlock, index+1)},
		Else:     elseBlock,
	}
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
			afterColon == lexer.TOKEN_HEAP || afterColon == lexer.TOKEN_STACK || afterColon == lexer.TOKEN_STATIC {
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

	if p.peek() == lexer.TOKEN_RETURN {
		returnPos := p.cur().Pos
		p.advance()
		if !p.match(lexer.TOKEN_IF) {
			p.errorAt(returnPos, "postfix return guard expects `if`")
			p.expectNewlineAfterValueExpr(expr)
			return &ast.ReturnStmt{Position: returnPos, Value: expr}
		}
		cond := p.parseExpr()
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
