package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseWhereViewExpr(source ast.Expr) ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("where")
	if p.peekWhereTupleSubjectPattern() {
		return p.parseWhereTupleSubjectPatternViewExpr(source, pos)
	}
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IS {
		return p.parseWhereSubjectPatternViewExpr(source, pos)
	}
	if p.peekWhereViewPatternFilter() {
		return p.parseWherePatternViewExpr(source, pos)
	}
	binderPos := p.cur().Pos
	binders := make([]string, 0, 2)
	binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_COMMA) {
		binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	}
	p.expect(lexer.TOKEN_COLON)
	filter := p.parseExpr()
	predicateParam := binders[0]
	if len(binders) > 1 {
		predicateParam = "__where_item"
		filter = rewriteWhereTupleBinderExpr(filter, predicateParam, binders)
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: predicateParam}},
		BodyExpr:            filter,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWhereTupleSubjectPatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	binderPos := p.cur().Pos
	binders := make([]string, 0, 2)
	binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	for p.match(lexer.TOKEN_COMMA) {
		binders = append(binders, p.expect(lexer.TOKEN_IDENT).Text)
	}
	subject := binders[len(binders)-1]
	p.expect(lexer.TOKEN_IS)
	pattern := p.parseMatchPattern()
	subjectField := "value"
	for i, binder := range binders {
		if binder != subject {
			continue
		}
		switch i {
		case 0:
			subjectField = "index"
		case 1:
			subjectField = "value"
		}
	}
	tupleItem := &ast.Ident{Position: binderPos, Name: "__where_item"}
	subjectExpr := &ast.FieldExpr{Position: binderPos, Object: tupleItem, Field: subjectField}
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: subjectExpr, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := rewriteWhereTupleBinderExpr(p.parseExpr(), "__where_item", binders)
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.FieldExpr{Position: binderPos, Object: &ast.Ident{Position: binderPos, Name: "__where_item"}, Field: subjectField},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: "__where_item"}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWhereSubjectPatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	binderPos := p.cur().Pos
	binder := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IS)
	pattern := p.parseMatchPattern()
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	item := &ast.Ident{Position: binderPos, Name: binder}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: item, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := p.parseExpr()
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.Ident{Position: binderPos, Name: binder},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            binderPos,
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: binderPos, Name: binder}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) parseWherePatternViewExpr(source ast.Expr, pos lexer.Pos) ast.Expr {
	pattern := p.parseMatchPattern()
	target := matchPatternAsIsTargetExpr(pattern)
	if target == nil {
		p.errorAt(pattern.Pos(), "expression where pattern filter expects a variant or struct pattern")
		target = &ast.TypeExprExpr{Position: pattern.Pos(), Type: &ast.NamedType{Position: pattern.Pos(), Name: "<error>"}}
	}
	item := &ast.Ident{Position: pattern.Pos(), Name: "__where_item"}
	condition := ast.Expr(&ast.BinaryExpr{Position: pattern.Pos(), Op: lexer.TOKEN_IS, Left: item, Right: target})
	if p.match(lexer.TOKEN_COLON) {
		filter := p.parseExpr()
		condition = &ast.MatchExpr{
			Position: pattern.Pos(),
			Value:    &ast.Ident{Position: pattern.Pos(), Name: "__where_item"},
			Arms: []ast.MatchArm{
				{Position: pattern.Pos(), Pattern: pattern, Body: []ast.Stmt{&ast.ExprStmt{Position: filter.Pos(), Expr: filter}}},
				{Position: pattern.Pos(), Pattern: &ast.MatchWildcardPattern{Position: pattern.Pos()}, Body: []ast.Stmt{&ast.ExprStmt{Position: pattern.Pos(), Expr: &ast.BoolLit{Position: pattern.Pos(), Value: false}}}},
			},
		}
	}
	predicate := &ast.LambdaExpr{
		Position:            pattern.Pos(),
		Keyword:             "lambda",
		UsesShorthandParams: true,
		Params:              []ast.ParamDecl{{Position: pattern.Pos(), Name: "__where_item"}},
		BodyExpr:            condition,
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "where"},
		Args:     []ast.Expr{source, predicate},
	}
}

func (p *Parser) peekExplicitWhereSubjectPattern(name string) bool {
	return name != "" &&
		p.peek() == lexer.TOKEN_IDENT &&
		p.cur().Text == name && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IS
}

func (p *Parser) parseQueryBindPattern() (string, ast.MoveBindPattern) {
	pos := p.cur().Pos
	first := p.expect(lexer.TOKEN_IDENT).Text
	if !p.match(lexer.TOKEN_COMMA) {
		return first, nil
	}
	args := []ast.MoveBindArg{{Position: pos, Name: first}}
	for {
		tok := p.expect(lexer.TOKEN_IDENT)
		args = append(args, ast.MoveBindArg{Position: tok.Pos, Name: tok.Text})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return first, &ast.MoveBindTuplePattern{Position: pos, Args: args}
}

func (p *Parser) peekQueryWhereSubjectPattern(name string, pattern ast.MoveBindPattern) (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_IS {
		return "", false
	}
	subject := p.cur().Text
	if pattern == nil {
		return subject, name != "" && name == subject
	}
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

func (p *Parser) peekWhereTupleSubjectPattern() bool {
	if p.peek() != lexer.TOKEN_IDENT || !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_COMMA {
		return false
	}
	index := p.pos + 2
	for p.hasToken(index) {
		if p.tokenAt(index).Kind != lexer.TOKEN_IDENT {
			return false
		}
		index++
		if !p.hasToken(index) {
			return false
		}
		switch p.tokenAt(index).Kind {
		case lexer.TOKEN_IS:
			return true
		case lexer.TOKEN_COMMA:
			index++
		default:
			return false
		}
	}
	return false
}

func (p *Parser) peekWhereViewPatternFilter() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	if !p.hasToken(p.pos + 1) {
		return false
	}
	next := p.tokenAt(p.pos + 1).Kind
	if next == lexer.TOKEN_LPAREN || next == lexer.TOKEN_LBRACE {
		return forWhereIdentLooksLikePatternType(p.cur().Text)
	}
	if next != lexer.TOKEN_DOT && next != lexer.TOKEN_SCOPE {
		return false
	}
	index := p.pos + 1
	for p.hasToken(index+1) && (p.tokenAt(index).Kind == lexer.TOKEN_DOT || p.tokenAt(index).Kind == lexer.TOKEN_SCOPE) && p.tokenAt(index+1).Kind == lexer.TOKEN_IDENT {
		index += 2
	}
	if !p.hasToken(index) || !forWhereIdentLooksLikePatternType(p.cur().Text) {
		return false
	}
	if !p.whereChainLooksLikePattern(p.pos, index) {
		return false
	}
	switch p.tokenAt(index).Kind {
	case lexer.TOKEN_LPAREN, lexer.TOKEN_COLON, lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_COMMA, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
		return true
	default:
		return false
	}
}

func matchPatternAsIsTargetExpr(pattern ast.MatchPattern) ast.Expr {
	switch n := pattern.(type) {
	case *ast.MatchVariantPattern:
		return &ast.VariantTestExpr{Position: n.Position, Pattern: n}
	case *ast.MatchStructPattern:
		return &ast.StructTestExpr{Position: n.Position, Pattern: n}
	default:
		return nil
	}
}

// elseIntroducesRecoveryClause reports whether the token sequence immediately
// after an `else` is a recovery clause (`return`, `raise`, `void`, or a
// `binding:` block) rather than a plain value fallback. The caller is positioned
// on the `else` token.
func (p *Parser) elseIntroducesRecoveryClause() bool {
	if p.peek() != lexer.TOKEN_ELSE {
		return false
	}
	next := p.pos + 1
	if !p.hasToken(next) {
		return false
	}
	switch p.tokenAt(next).Kind {
	case lexer.TOKEN_RETURN, lexer.TOKEN_RAISE:
		return true
	case lexer.TOKEN_IDENT:
		if p.tokenAt(next).Text == "void" {
			return true
		}
		// `else binding:` block recovery.
		return p.hasToken(next+1) && p.tokenAt(next+1).Kind == lexer.TOKEN_COLON
	}
	return false
}

func (p *Parser) parseRecoveryClause(pos lexer.Pos) *ast.RecoveryClause {
	switch p.peek() {
	case lexer.TOKEN_RETURN:
		p.advance()
		return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryReturn, Value: p.parseOptionalRecoveryExpr()}
	case lexer.TOKEN_RAISE:
		p.advance()
		return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryRaise, Value: p.parseOr()}
	case lexer.TOKEN_IDENT:
		if p.cur().Text == "void" {
			p.advance()
			return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryVoid}
		}
		if p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON {
			binding := p.cur().Text
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryBlock, Binding: binding, Body: p.parseBlock()}
		}
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: p.parseExpr()}
}

func (p *Parser) parseOptionalRecoveryExpr() ast.Expr {
	switch p.peek() {
	case lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF, lexer.TOKEN_DEDENT, lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_COMMA:
		return nil
	default:
		return p.parseExpr()
	}
}

func recoveryFallbackExpr(recovery *ast.RecoveryClause) ast.Expr {
	if recovery == nil {
		return nil
	}
	switch recovery.Kind {
	case ast.RecoveryValue:
		return recovery.Value
	case ast.RecoveryRaise:
		return &ast.RaiseExpr{Position: recovery.Position, Error: recovery.Value}
	default:
		return nil
	}
}
func (p *Parser) parseOr() ast.Expr {
	left := p.parseAnd()
	chain := 0
	for p.peek() == lexer.TOKEN_OR {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		p.advance()
		right := p.parseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_OR, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAnd() ast.Expr {
	left := p.parseNot()
	chain := 0
	for p.peek() == lexer.TOKEN_AND {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		p.advance()
		right := p.parseNot()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AND, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseNot() ast.Expr {
	if p.peek() == lexer.TOKEN_NOT {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNestedNot()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: operand}
	}
	if p.matchIdentText("let") {
		pos := p.tokenAt(p.pos - 1).Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseNestedNot()
		return &ast.OptionalBindExpr{Position: pos, Name: name, Value: value}
	}
	return p.parseComparison()
}
func (p *Parser) parseComparison() ast.Expr {
	left := p.parseAs()
	chain := 0
	for p.peek() == lexer.TOKEN_EQEQ || p.peek() == lexer.TOKEN_BANGEQ ||
		p.peek() == lexer.TOKEN_LT || p.peek() == lexer.TOKEN_GT ||
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ ||
		(!p.disallowIsComparison && p.peek() == lexer.TOKEN_IS) || p.membershipLiteralAhead() || p.notInMembershipAhead() {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		if p.notInMembershipAhead() {
			p.advance()
			inToken := p.advance()
			right := p.parseMembershipRight()
			membership := &ast.BinaryExpr{Position: inToken.Pos, Op: lexer.TOKEN_IN, Left: left, Right: right}
			left = &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: membership}
			continue
		}
		op := p.advance()
		var right ast.Expr
		if op.Kind == lexer.TOKEN_IS {
			if p.peek() == lexer.TOKEN_NOT {
				notToken := p.advance()
				right = p.parseIsTestExpr()
				test := &ast.BinaryExpr{Position: op.Pos, Op: op.Kind, Left: left, Right: right}
				left = &ast.UnaryExpr{Position: notToken.Pos, Op: lexer.TOKEN_NOT, Operand: test}
				continue
			}
			if name, ok := p.peekIsBareBindingTarget(); ok {
				// docs/80: `x is value` with a bare, unqualified, lowercase
				// identifier that is not a builtin type name is a refutable
				// binding, not a type test. It reuses the `let value = x`
				// optional-bind node so the whole optional-binding pipeline
				// (analysis + scoping + lowering) applies unchanged.
				p.advance()
				left = &ast.OptionalBindExpr{Position: op.Pos, Name: name, Value: left, FromIs: true}
				continue
			}
			right = p.parseIsTestExpr()
		} else if op.Kind == lexer.TOKEN_IN {
			right = p.parseMembershipRight()
		} else {
			right = p.parseAs()
		}
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) membershipLiteralAhead() bool {
	return p.allowInMembership && p.peek() == lexer.TOKEN_IN
}
func (p *Parser) notInMembershipAhead() bool {
	return p.allowInMembership && p.peek() == lexer.TOKEN_NOT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IN
}
func (p *Parser) parseMembershipRight() ast.Expr {
	start := p.parseAs()
	if !p.membershipRangeOpAhead() {
		return start
	}
	opKind, opPos := p.consumeMembershipRangeOp()
	end := p.parseAs()
	return &ast.MembershipRangeExpr{Position: opPos, Start: start, End: end, Op: opKind}
}
func (p *Parser) membershipRangeOpAhead() bool {
	switch p.peek() {
	case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE:
		return true
	default:
		return false
	}
}
