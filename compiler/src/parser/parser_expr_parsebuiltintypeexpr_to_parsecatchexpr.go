package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (p *Parser) parseBuiltinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	switch name {
	case "id", "Id", "ID", "RowId":
		p.advance()
		tag := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		if name == "RowId" {
			return &ast.BuiltinTypeExpr{Position: pos, Name: "RowId", TypeArgs: []ast.TypeExpr{tag}}
		}
		return &ast.BuiltinTypeExpr{Position: pos, Name: "id", TypeArgs: []ast.TypeExpr{tag}}
	case "array", "darray":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			if name == "darray" {
				return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
			}
			p.errorf("array expects 2 arguments, got 1")
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		p.expect(lexer.TOKEN_COMMA)
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}, ValueArgs: []ast.Expr{size}}
	case "str":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "str", ValueArgs: []ast.Expr{size}}
	case "string":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "string", ValueArgs: []ast.Expr{size}}
	case "dict":
		p.advance()
		key := p.parseTypeExpr()
		p.expect(lexer.TOKEN_COMMA)
		value := p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{key, value}}
	case "cstr":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "cstr", ValueArgs: []ast.Expr{size}}
	case "cstring":
		p.advance()
		size := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: "cstring", ValueArgs: []ast.Expr{size}}
	case "view", "dview", "packedview", "treeview":
		p.advance()
		elem := p.parseTypeExpr()
		if p.match(lexer.TOKEN_RBRACKET) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		if name == "dview" || name == "packedview" || name == "treeview" {
			p.errorf("%s expects 1 argument, got 3", name)
			for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
				p.advance()
			}
			p.expect(lexer.TOKEN_RBRACKET)
			return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}}
		}
		p.expect(lexer.TOKEN_COMMA)
		begin := p.parseExpr()
		p.expect(lexer.TOKEN_COMMA)
		end := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, TypeArgs: []ast.TypeExpr{elem}, ValueArgs: []ast.Expr{begin, end}}
	case "sview":
		p.advance()
		begin := p.parseExpr()
		p.expect(lexer.TOKEN_COMMA)
		end := p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		return &ast.BuiltinTypeExpr{Position: pos, Name: name, ValueArgs: []ast.Expr{begin, end}}
	default:
		return nil
	}
}
func (p *Parser) parseExpr() ast.Expr {
	expr := p.parseOr()

	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && (p.tokens[p.pos+1].Text == "first" || p.tokens[p.pos+1].Text == "each") {
		return p.parseProjectionQueryExpr(expr)
	}
	if p.allowWhereExpr && p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "where" {
		return p.parseWhereViewExpr(expr)
	}
	if p.allowTernary && p.peek() == lexer.TOKEN_IF {
		pos := p.cur().Pos
		p.advance()
		cond := p.parseOr()
		p.expect(lexer.TOKEN_ELSE)
		alt := p.parseExpr()
		return &ast.TernaryExpr{Position: pos, Value: expr, Cond: cond, Alt: alt}
	}
	if p.peek() == lexer.TOKEN_ELSE {
		pos := p.cur().Pos
		p.advance()
		recovery := p.parseRecoveryClause(pos)
		fallback := recoveryFallbackExpr(recovery)
		if tryExpr, ok := expr.(*ast.TryExpr); ok && tryExpr.Recovery == nil && tryExpr.Fallback == nil {
			tryExpr.Recovery = recovery
			tryExpr.Fallback = fallback
			return tryExpr
		}
		return &ast.UnwrapElseExpr{Position: pos, Value: expr, Fallback: fallback, Recovery: recovery}
	}
	if p.matchIdentText("can") {
		permissions := p.parsePermissionRefs(false)
		return &ast.CanExpr{Position: expr.Pos(), Expr: expr, Permissions: permissions}
	}

	return expr
}

func (p *Parser) parseProjectionQueryExpr(projection ast.Expr) ast.Expr {
	pos := projection.Pos()
	p.expectIdentText("for")
	kindText := p.expect(lexer.TOKEN_IDENT).Text
	kind := ast.QueryExprFirst
	switch kindText {
	case "first":
		kind = ast.QueryExprFirst
	case "each":
		kind = ast.QueryExprEach
	default:
		p.errorAt(pos, "projection query expression expects first or each, got %q", kindText)
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_IN)
	source := p.withInMembershipDisabled(func() ast.Expr {
		return p.withWhereExprDisabled(func() ast.Expr { return p.withTernaryDisabled(p.parseExpr) })
	})
	var patternFilter ast.MatchPattern
	var filter ast.Expr
	if p.matchIdentText("where") {
		if p.peekWhereViewPatternFilter() {
			patternFilter = p.parseMatchPattern()
			if p.match(lexer.TOKEN_COLON) {
				filter = p.parseExpr()
			}
		} else {
			filter = p.parseExpr()
		}
	}
	var owner ast.Expr
	if p.match(lexer.TOKEN_WITH) {
		owner = p.withInMembershipDisabled(p.parseExpr)
	}
	return &ast.QueryExpr{Position: pos, Kind: kind, Name: name, Source: source, Filter: filter, PatternFilter: patternFilter, Projection: projection, Owner: owner}
}

func (p *Parser) parseWhereViewExpr(source ast.Expr) ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("where")
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

func (p *Parser) peekWhereViewPatternFilter() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1].Kind
	if next == lexer.TOKEN_LPAREN || next == lexer.TOKEN_LBRACE {
		return forWhereIdentLooksLikePatternType(p.cur().Text)
	}
	if next != lexer.TOKEN_DOT {
		return false
	}
	index := p.pos + 1
	for index+1 < len(p.tokens) && p.tokens[index].Kind == lexer.TOKEN_DOT && p.tokens[index+1].Kind == lexer.TOKEN_IDENT {
		index += 2
	}
	if index >= len(p.tokens) || !forWhereIdentLooksLikePatternType(p.cur().Text) {
		return false
	}
	if index >= len(p.tokens) {
		return true
	}
	switch p.tokens[index].Kind {
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
		if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
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
func (p *Parser) parseChildrenCallArgs() ([]ast.Expr, []string) {
	if p.peek() == lexer.TOKEN_RPAREN {
		return nil, nil
	}
	args := make([]ast.Expr, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	for {
		arg := p.withAsCastEnabled(p.parseExpr)
		if p.peek() == lexer.TOKEN_TO {
			pos := p.cur().Pos
			p.advance()
			target := p.parseTypeExpr()
			p.errorAt(pos, "legacy children cast syntax `expr to T` is deprecated; use `expr as T` instead")
			arg = &ast.CastExpr{Position: arg.Pos(), Operand: arg, Target: target, Origin: ast.CastExprOriginToSyntax}
		}
		args = append(args, arg)
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	return args, nil
}
func (p *Parser) parseOr() ast.Expr {
	left := p.parseAnd()
	for p.peek() == lexer.TOKEN_OR {
		pos := p.cur().Pos
		p.advance()
		right := p.parseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_OR, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAnd() ast.Expr {
	left := p.parseNot()
	for p.peek() == lexer.TOKEN_AND {
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
		operand := p.parseNot()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: operand}
	}
	if p.matchIdentText("let") {
		pos := p.tokens[p.pos-1].Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		value := p.parseNot()
		return &ast.OptionalBindExpr{Position: pos, Name: name, Value: value}
	}
	return p.parseComparison()
}
func (p *Parser) parseComparison() ast.Expr {
	left := p.parseAs()
	for p.peek() == lexer.TOKEN_EQEQ || p.peek() == lexer.TOKEN_BANGEQ ||
		p.peek() == lexer.TOKEN_LT || p.peek() == lexer.TOKEN_GT ||
		p.peek() == lexer.TOKEN_LTEQ || p.peek() == lexer.TOKEN_GTEQ ||
		p.peek() == lexer.TOKEN_IS || p.membershipLiteralAhead() || p.notInMembershipAhead() {
		pos := p.cur().Pos
		if p.notInMembershipAhead() {
			p.advance()
			inToken := p.advance()
			right := p.parseAs()
			membership := &ast.BinaryExpr{Position: inToken.Pos, Op: lexer.TOKEN_IN, Left: left, Right: right}
			left = &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: membership}
			continue
		}
		op := p.advance()
		var right ast.Expr
		if op.Kind == lexer.TOKEN_IS {
			right = p.parseIsTestExpr()
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
	return p.allowInMembership && p.peek() == lexer.TOKEN_NOT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IN
}
func (p *Parser) parseAs() ast.Expr {
	left := p.parseBitwiseOr()
	for p.allowAsCast && p.peek() == lexer.TOKEN_AS {
		if p.pos+1 >= len(p.tokens) || !tokenCanStartTypeExpr(p.tokens[p.pos+1]) {
			return left
		}
		pos := p.cur().Pos
		p.advance()
		target := p.parseTypeExpr()
		left = &ast.CastExpr{Position: pos, Operand: left, Target: target, Origin: ast.CastExprOriginAsSyntax}
	}
	return left
}
func (p *Parser) parseIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	targets := make([]ast.Expr, 0, 1)
	targets = append(targets, p.parseSingleIsTestTargetExpr())
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		targets = append(targets, p.parseSingleIsTestTargetExpr())
	}
	if len(targets) == 1 {
		return targets[0]
	}
	return &ast.IsPatternExpr{Position: pos, Targets: targets}
}
func (p *Parser) parseSingleIsTestTargetExpr() ast.Expr {
	target := p.parseSingleIsTestTargetExprWithoutAlias()
	if p.peek() == lexer.TOKEN_AS && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		pos := p.cur().Pos
		p.advance()
		alias := p.expect(lexer.TOKEN_IDENT).Text
		return &ast.IsAliasExpr{Position: pos, Target: target, Alias: alias}
	}
	return target
}
func (p *Parser) parseSingleIsTestTargetExprWithoutAlias() ast.Expr {
	if p.peekQualifiedVariantTargetWithPayload() {
		return p.parseVariantIsTestExpr()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) &&
		(p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN || p.tokens[p.pos+1].Kind == lexer.TOKEN_LBRACE) {
		return p.parseStructIsTestExpr()
	}
	if p.peek() == lexer.TOKEN_DOT {
		return p.parsePrimary()
	}
	if p.peek() == lexer.TOKEN_LPAREN {
		pos := p.cur().Pos
		p.advance()
		inner := p.parseIsTestExpr()
		p.expect(lexer.TOKEN_RPAREN)
		return &ast.ParenExpr{Position: pos, Inner: inner}
	}
	target := p.parseTypeExprWithoutErrorUnionSuffix()
	return &ast.TypeExprExpr{Position: target.Pos(), Type: target}
}
func (p *Parser) peekQualifiedVariantTargetWithPayload() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	i := p.pos
	sawDot := false
	for i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_DOT {
		if i+2 >= len(p.tokens) || p.tokens[i+2].Kind != lexer.TOKEN_IDENT {
			return false
		}
		sawDot = true
		i += 2
	}
	return sawDot && i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_LPAREN
}
func (p *Parser) parseQualifiedVariantTarget() (string, string, lexer.Pos) {
	pos := p.cur().Pos
	parts := []string{p.expect(lexer.TOKEN_IDENT).Text}
	for p.match(lexer.TOKEN_DOT) {
		parts = append(parts, p.expect(lexer.TOKEN_IDENT).Text)
	}
	if len(parts) < 2 {
		return "", "", pos
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], pos
}
func (p *Parser) parseVariantIsTestExpr() ast.Expr {
	enumName, variant, pos := p.parseQualifiedVariantTarget()
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	p.expect(lexer.TOKEN_LPAREN)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			args = append(args, p.parseMatchPatternArg())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.VariantTestExpr{Position: pos, Pattern: &ast.MatchVariantPattern{Position: pos, EnumName: enumName, Variant: variant, Args: args}}
}
func (p *Parser) parseStructIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	name := p.expect(lexer.TOKEN_IDENT).Text
	pattern, ok := p.parseMatchStructPatternAfterName(pos, name).(*ast.MatchStructPattern)
	if !ok {
		return &ast.StructTestExpr{Position: pos}
	}
	return &ast.StructTestExpr{Position: pos, Pattern: pattern}
}
func (p *Parser) parseBitwiseOr() ast.Expr {
	left := p.parseBitwiseXor()
	for p.peek() == lexer.TOKEN_PIPE {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseXor()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PIPE, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseXor() ast.Expr {
	left := p.parseBitwiseAnd()
	for p.peek() == lexer.TOKEN_CARET {
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_CARET, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseAnd() ast.Expr {
	left := p.parseShift()
	for p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		right := p.parseShift()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AMPERSAND, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseShift() ast.Expr {
	left := p.parseAddSub()
	for p.peek() == lexer.TOKEN_LSHIFT || p.peek() == lexer.TOKEN_RSHIFT {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseAddSub()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAddSub() ast.Expr {
	left := p.parseMulDiv()
	for p.peek() == lexer.TOKEN_PLUS || p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseMulDiv()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseMulDiv() ast.Expr {
	left := p.parseUnary()
	for p.peek() == lexer.TOKEN_STAR || p.peek() == lexer.TOKEN_SLASH || p.peek() == lexer.TOKEN_PERCENT {
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseUnary()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseUnary() ast.Expr {
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "new" {
		return p.parseAllocExpr()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "node" && p.looksLikeNodeAllocExpr() {
		return p.parseNodeAllocExpr()
	}
	if p.matchIdentText("submit") {
		pos := p.tokens[p.pos-1].Pos
		var explicitPool ast.Expr
		hasExplicitPool := false
		if p.match(lexer.TOKEN_LBRACKET) {
			hasExplicitPool = true
			explicitPool = p.parseExpr()
			p.expect(lexer.TOKEN_RBRACKET)
		}
		callExpr := p.parseUnary()
		call, ok := callExpr.(*ast.CallExpr)
		if !ok {
			p.errorf("submit expects a call like submit work(arg) or submit[pool] work(arg)")
			return callExpr
		}
		if len(call.Args) != 1 || call.NamedArgCount() != 0 {
			p.errorf("submit currently expects exactly one positional argument")
			return callExpr
		}
		poolArg := explicitPool
		if !hasExplicitPool {
			activePool := p.activePoolName()
			if activePool == "" {
				p.errorf("submit requires an active pool scope like \"pool workers(...):\" or an explicit pool like \"submit[pool] work(arg)\"")
				return callExpr
			}
			poolPos := call.Func.Pos()
			poolArg = &ast.CastExpr{
				Position: poolPos,
				Operand:  &ast.AddrOfExpr{Position: poolPos, Operand: &ast.Ident{Position: poolPos, Name: activePool}},
				Target: &ast.RefType{
					Position: poolPos,
					Elem:     &ast.NamedType{Position: poolPos, Name: "ThreadPool"},
					State:    ast.RefStateNonNull,
					Storage:  ast.RefStorageAny,
					Explicit: true,
				},
			}
		}
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_submit1"},
			Args: []ast.Expr{
				poolArg,
				call.Func,
				call.Args[0],
			},
		}
	}
	if p.matchIdentText("await") {
		pos := p.tokens[p.pos-1].Pos
		operand := p.parseUnary()
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_await"},
			Args:     []ast.Expr{&ast.MoveExpr{Position: pos, Operand: operand}},
		}
	}
	if p.matchIdentText("move") {
		pos := p.tokens[p.pos-1].Pos
		operand := p.parseUnary()
		return &ast.MoveExpr{Position: pos, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_TILDE {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_TILDE, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_BANG {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_BANG, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseUnary()
		return &ast.AddrOfExpr{Position: pos, Operand: operand}
	}
	return p.parsePostfix()
}
func (p *Parser) parseAllocExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	var owner ast.Expr
	if p.match(lexer.TOKEN_LBRACKET) {
		owner = p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	value := p.parseExpr()
	return &ast.AllocExpr{Position: pos, Owner: owner, Value: value}
}
func (p *Parser) looksLikeNodeAllocExpr() bool {
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	if p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT {
		return true
	}
	if p.tokens[p.pos+1].Kind != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for i := p.pos + 1; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
			if depth == 0 {
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.TOKEN_IDENT
			}
		case lexer.TOKEN_EOF, lexer.TOKEN_NEWLINE:
			return false
		}
	}
	return false
}
func (p *Parser) parseNodeAllocExpr() ast.Expr {
	pos := p.cur().Pos
	p.expectIdentText("node")
	owner := ast.Expr(&ast.Ident{Position: pos, Name: "alloc"})
	var span ast.Expr
	if p.match(lexer.TOKEN_LBRACKET) {
		for p.peek() != lexer.TOKEN_RBRACKET && p.peek() != lexer.TOKEN_EOF {
			if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
				name := p.advance().Text
				p.expect(lexer.TOKEN_ASSIGN)
				value := p.parseExpr()
				switch name {
				case "alloc":
					owner = value
				case "span":
					span = value
				default:
					p.errorf("unknown node construction option %q", name)
				}
			} else {
				owner = p.parseExpr()
			}
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RBRACKET)
	}
	value := p.parseExpr()
	if span != nil {
		value = p.injectNodeSpanArg(value, span)
	}
	return &ast.AllocExpr{Position: pos, Owner: owner, Value: value, NodeSugar: true, NodeSpan: span}
}
func (p *Parser) injectNodeSpanArg(value ast.Expr, span ast.Expr) ast.Expr {
	call, ok := value.(*ast.CallExpr)
	if !ok || call == nil {
		return &ast.CallExpr{Position: value.Pos(), Func: value, Args: []ast.Expr{span}, ArgNames: []string{"span"}, ArgShorthand: []bool{false}}
	}
	for _, name := range call.ArgNames {
		if name == "span" {
			p.errorf("node constructor span supplied both in node[...] and constructor arguments")
			return value
		}
	}
	index := len(call.Args)
	call.Args = append(call.Args, span)
	call.ArgNames = append(call.ArgNames, "span")
	call.ArgShorthand = append(call.ArgShorthand, false)
	if len(call.ArgItemOrder) != 0 {
		call.ArgItemOrder = append(call.ArgItemOrder, ast.CallArgItem{Position: span.Pos(), ArgIndex: index})
	}
	return value
}
func (p *Parser) parseCatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CATCH)
	value := p.withInMembershipDisabled(p.parseExpr)
	success, arms := p.parseCatchArms()
	return &ast.CatchExpr{Position: pos, Value: value, Success: success, Arms: arms}
}
