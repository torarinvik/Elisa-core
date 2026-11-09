package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

func (p *Parser) parseAs() ast.Expr {
	return p.parseBitwiseOr()
}

// isBuiltinBareTypeName mirrors the lowercase builtin type names registered in
// semantic/analyzer_builtins.go (plus the string views cstr/dstr). A bare
// `is`-target naming one of these is a TYPE test; any other bare unqualified
// lowercase identifier is a refutable BINDING (docs/80). Keep in sync with the
// builtin registration; a missing entry turns an `is <builtin>` type test into a
// binding and the suite's many `is`-type-test fixtures catch it immediately.
var isBuiltinBareTypeName = map[string]bool{
	"void": true, "bool": true, "char": true, "int": true,
	"i8": true, "i16": true, "i32": true, "i64": true, "isize": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "usize": true, "uintptr": true,
	"f32": true, "f64": true, "cstr": true, "dstr": true,
}

// peekIsBareBindingTarget reports whether the upcoming `is`-target is a bare,
// unqualified, lowercase, non-builtin identifier — i.e. a refutable binding name
// (docs/80) rather than a type/variant/struct test. It must NOT be continued by a
// token that would make it a qualified name, a variant/struct payload, a union
// alternative, or an alias.
func (p *Parser) peekIsBareBindingTarget() (string, bool) {
	if p.peek() != lexer.TOKEN_IDENT {
		return "", false
	}
	name := p.cur().Text
	if name == "" || name[0] < 'a' || name[0] > 'z' || isBuiltinBareTypeName[name] {
		return "", false
	}
	if p.hasToken(p.pos + 1) {
		switch p.tokenAt(p.pos + 1).Kind {
		case lexer.TOKEN_DOT, lexer.TOKEN_SCOPE, lexer.TOKEN_LPAREN,
			lexer.TOKEN_LBRACE, lexer.TOKEN_LBRACKET, lexer.TOKEN_PIPE, lexer.TOKEN_AS:
			return "", false
		}
	}
	return name, true
}
func (p *Parser) parseIsTestExpr() ast.Expr {
	pos := p.cur().Pos
	brackets := false
	if p.peek() == lexer.TOKEN_LBRACKET {
		brackets = true
		p.advance()
	}
	targets := make([]ast.Expr, 0, 1)
	targets = append(targets, p.parseSingleIsTestTargetExpr())
	for p.peek() == lexer.TOKEN_PIPE {
		p.advance()
		targets = append(targets, p.parseSingleIsTestTargetExpr())
	}
	if brackets {
		p.expect(lexer.TOKEN_RBRACKET)
	}
	if len(targets) == 1 && !brackets {
		return targets[0]
	}
	return &ast.IsPatternExpr{Position: pos, Targets: targets, Brackets: brackets}
}
func (p *Parser) parseSingleIsTestTargetExpr() ast.Expr {
	target := p.parseSingleIsTestTargetExprWithoutAlias()
	if p.peek() == lexer.TOKEN_AS && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		pos := p.cur().Pos
		p.advance()
		alias := p.expect(lexer.TOKEN_IDENT).Text
		return &ast.IsAliasExpr{Position: pos, Target: target, Alias: alias}
	}
	// docs/77 §2 binder sugar: `e is Statement s` ≡ `e is Statement as s` — a bare
	// lowercase identifier directly after the target binds the matched value (for a
	// category target, at the narrowed type). No valid continuation starts with a
	// bare lowercase ident here, so this is unambiguous.
	if p.peek() == lexer.TOKEN_IDENT {
		name := p.cur().Text
		if name != "" && name != "_" && name[0] >= 'a' && name[0] <= 'z' {
			pos := p.cur().Pos
			alias := p.advance().Text
			return &ast.IsAliasExpr{Position: pos, Target: target, Alias: alias}
		}
	}
	return target
}
func (p *Parser) parseSingleIsTestTargetExprWithoutAlias() ast.Expr {
	if p.peek() == lexer.TOKEN_NULL {
		return p.parsePrimary()
	}
	if p.peekQualifiedVariantTargetWithPayload() {
		return p.parseVariantIsTestExpr()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) &&
		(p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LPAREN || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_LBRACE) {
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
	// docs/77 §2 binder form: `is Statement s` — a bare type name followed by a lowercase
	// binder identifier. Intercepted BEFORE the type parser, whose legacy region-prefix
	// grammar (`r T&`) would otherwise swallow both identifiers. The binder form never
	// continues with `&` / `.` / `[` after the second identifier.
	if p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		second := p.tokenAt(p.pos + 1).Text
		cont := lexer.TOKEN_EOF
		if p.hasToken(p.pos + 2) {
			cont = p.tokenAt(p.pos + 2).Kind
		}
		if second != "" && second != "_" && second[0] >= 'a' && second[0] <= 'z' &&
			cont != lexer.TOKEN_AMPERSAND && cont != lexer.TOKEN_DOT && cont != lexer.TOKEN_LBRACKET {
			pos := p.cur().Pos
			name := p.advance().Text
			return &ast.TypeExprExpr{Position: pos, Type: &ast.NamedType{Position: pos, Name: name}}
		}
	}
	target := p.parseTypeExprWithoutErrorUnionSuffix()
	return &ast.TypeExprExpr{Position: target.Pos(), Type: target}
}
func (p *Parser) peekQualifiedVariantTargetWithPayload() bool {
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	// `M::Enum.Variant(...)`: the module steps are `::`, the variant step `.`; both
	// separators are accepted here so a module-qualified payload target parses as the
	// variant test it is (it used to fall through to a parse error).
	i := p.pos
	sawDot := false
	for p.hasToken(i+1) && (p.tokenAt(i+1).Kind == lexer.TOKEN_DOT || p.tokenAt(i+1).Kind == lexer.TOKEN_SCOPE) {
		if !p.hasToken(i+2) || p.tokenAt(i+2).Kind != lexer.TOKEN_IDENT {
			return false
		}
		sawDot = true
		i += 2
	}
	return sawDot && p.hasToken(i+1) && p.tokenAt(i+1).Kind == lexer.TOKEN_LPAREN
}
func (p *Parser) parseQualifiedVariantTarget() (string, string, lexer.Pos) {
	pos := p.cur().Pos
	// `M::Enum.Variant(...)`: module segments join with `::`, the variant with `.`; a
	// `.` before the last separator is a mis-spelled module path (parseVariantPathName).
	parts := p.parseVariantPathName(pos, p.expect(lexer.TOKEN_IDENT).Text)
	if len(parts) < 2 {
		return "", "", pos
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1], pos
}
func (p *Parser) parseVariantIsTestExpr() ast.Expr {
	enumName, variant, pos := p.parseQualifiedVariantTarget()
	p.expect(lexer.TOKEN_LPAREN)
	args := make([]ast.MatchPatternArg, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
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
	chain := 0
	for p.peek() == lexer.TOKEN_PIPE {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseXor()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PIPE, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseXor() ast.Expr {
	left := p.parseBitwiseAnd()
	chain := 0
	for p.peek() == lexer.TOKEN_CARET {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		p.advance()
		right := p.parseBitwiseAnd()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_CARET, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseBitwiseAnd() ast.Expr {
	left := p.parseShift()
	chain := 0
	for p.peek() == lexer.TOKEN_AMPERSAND {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		p.advance()
		right := p.parseShift()
		left = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_AMPERSAND, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseShift() ast.Expr {
	left := p.parseAddSub()
	chain := 0
	for p.peek() == lexer.TOKEN_LSHIFT || p.peek() == lexer.TOKEN_RSHIFT {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseAddSub()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseAddSub() ast.Expr {
	left := p.parseMulDiv()
	chain := 0
	for p.peek() == lexer.TOKEN_PLUS || p.peek() == lexer.TOKEN_MINUS {
		chain++
		p.checkChainDepth(chain)
		pos := p.cur().Pos
		op := p.advance()
		right := p.parseMulDiv()
		left = &ast.BinaryExpr{Position: pos, Op: op.Kind, Left: left, Right: right}
	}
	return left
}
func (p *Parser) parseMulDiv() ast.Expr {
	left := p.parseUnary()
	chain := 0
	for p.peek() == lexer.TOKEN_STAR || p.peek() == lexer.TOKEN_SLASH || p.peek() == lexer.TOKEN_PERCENT {
		chain++
		p.checkChainDepth(chain)
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
		pos := p.cur().Pos
		p.errorAt(pos, "`node[...] Tree.Member(...)` has been removed; use `new[owner] Enum.Variant(...)` or an ordinary enum constructor")
		p.advance()
		return &ast.Ident{Position: pos, Name: "__invalid_removed_node_sugar"}
	}
	if p.matchIdentText("submit") {
		pos := p.tokenAt(p.pos - 1).Pos
		var explicitPool ast.Expr
		hasExplicitPool := false
		if p.match(lexer.TOKEN_LBRACKET) {
			hasExplicitPool = true
			explicitPool = p.parseExpr()
			p.expect(lexer.TOKEN_RBRACKET)
		}
		callExpr := p.parseNestedUnary()
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
				p.errorf("submit requires a concurrency scope like \"nursery:\", \"nursery on(pool):\", \"pool workers(...):\", or an explicit pool like \"submit[pool] work(arg)\"")
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
		submitCall := &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_submit1"},
			Args: []ast.Expr{
				poolArg,
				call.Func,
				call.Args[0],
			},
			// Synthesized from the safe `submit` / `pool` sugar, not a hand-written
			// raw pool_submit1 call — exempt from the raw-surface-removed diagnostic.
			SafeConcurrencySugar: true,
		}
		// Inside a nursery (implicit pool), auto-collect the task into the nursery's group so
		// it is joined at scope exit. The submit then yields void (fire-into-nursery): for an
		// individually awaitable handle, use a `pool workers(...)` scope instead.
		if !hasExplicitPool {
			if grpName, ok := p.nurseryGroupForActivePool(); ok {
				return &ast.CallExpr{
					Position: pos,
					Func:     &ast.Ident{Position: pos, Name: "task_group_add"},
					Args:     []ast.Expr{nurseryGroupRef(pos, grpName), submitCall},
				}
			}
		}
		return submitCall
	}
	if p.matchIdentText("await") {
		pos := p.tokenAt(p.pos - 1).Pos
		operand := p.parseNestedUnary()
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "pool_await"},
			Args:     []ast.Expr{&ast.MoveExpr{Position: pos, Operand: operand}},
		}
	}
	if p.matchIdentText("move") {
		pos := p.tokenAt(p.pos - 1).Pos
		operand := p.parseNestedUnary()
		return &ast.MoveExpr{Position: pos, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_MINUS {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNestedUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_MINUS, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_TILDE {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNestedUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_TILDE, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_BANG {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNestedUnary()
		return &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_BANG, Operand: operand}
	}
	if p.peek() == lexer.TOKEN_AMPERSAND {
		pos := p.cur().Pos
		p.advance()
		operand := p.parseNestedUnary()
		return &ast.AddrOfExpr{Position: pos, Operand: operand}
	}
	return p.parsePostfix()
}
func (p *Parser) parseAllocExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_IDENT)
	var owner ast.Expr
	autoRegion := false
	explicitAutoRegion := false
	if p.match(lexer.TOKEN_LBRACKET) {
		owner = p.parseExpr()
		p.expect(lexer.TOKEN_RBRACKET)
		// `new[auto]`: infer the region (native stack arena) like a container's backing. Represent
		// it with the AutoRegion flag and a nil Owner so no pass tries to resolve `auto` as an
		// identifier; the flag disambiguates the nil Owner from a bracket-less packed `new`.
		if ident, ok := owner.(*ast.Ident); ok && ident != nil && ident.Name == "auto" {
			owner = nil
			autoRegion = true
			explicitAutoRegion = true
		}
	}
	value := p.parseExpr()
	return &ast.AllocExpr{Position: pos, Owner: owner, Value: value, AutoRegion: autoRegion, ExplicitAutoRegion: explicitAutoRegion}
}
func (p *Parser) looksLikeNodeAllocExpr() bool {
	if !p.hasToken(p.pos + 1) {
		return false
	}
	if p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT {
		return true
	}
	if p.tokenAt(p.pos+1).Kind != lexer.TOKEN_LBRACKET {
		return false
	}
	depth := 0
	for i := p.pos + 1; p.hasToken(i); i++ {
		switch p.tokenAt(i).Kind {
		case lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RBRACKET:
			depth--
			if depth == 0 {
				return p.hasToken(i+1) && p.tokenAt(i+1).Kind == lexer.TOKEN_IDENT
			}
		case lexer.TOKEN_EOF, lexer.TOKEN_NEWLINE:
			return false
		}
	}
	return false
}
func (p *Parser) parseCatchExpr() ast.Expr {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_CATCH)
	value := p.withInMembershipDisabled(p.parseExpr)
	success, arms := p.parseCatchArms()
	return &ast.CatchExpr{Position: pos, Value: value, Success: success, Arms: arms}
}

// prevTokenIsDedent reports whether the token consumed immediately before the current
// position was a DEDENT — the boundary a multi-line block expression (match arms, an
// indented value block) leaves behind. A postfix-ternary `if` directly after that
// boundary belongs to the NEXT statement.
func (p *Parser) prevTokenIsDedent() bool {
	return p.pos > 0 && p.tokenAt(p.pos-1).Kind == lexer.TOKEN_DEDENT
}

// parseNestedUnary parses the operand of a prefix operator; the extra level counts toward
// MaxNestingDepth (`- - - … x`), while a plain primary costs nothing beyond its parseExpr.
func (p *Parser) parseNestedUnary() ast.Expr {
	p.enterNesting()
	defer p.leaveNesting()
	return p.parseUnary()
}

// parseNestedNot parses the operand of `not` / `let` the same way.
func (p *Parser) parseNestedNot() ast.Expr {
	p.enterNesting()
	defer p.leaveNesting()
	return p.parseNot()
}

// A runtime range evaluates its endpoints once and exits at the first witness
// (exists) or counterexample (forall). Empty ranges have the logical identity.
func (p *Parser) runtimeRangeQuantifier(pos lexer.Pos, exists bool, binder string, lo, hi, predicate ast.Expr) ast.Expr {
	name := fmt.Sprintf("__quantifier_%d", pos.Offset)
	result := &ast.Ident{Position: pos, Name: name}
	condition := predicate
	if !exists {
		condition = &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: predicate}
	}
	loop := &ast.ForStmt{Position: pos, Name: binder, Start: lo, End: hi, Op: lexer.TOKEN_RANGE_LT, Body: []ast.Stmt{
		&ast.IfStmt{Position: pos, Cond: condition, Then: []ast.Stmt{
			&ast.AssignStmt{Position: pos, Target: result, Value: &ast.BoolLit{Position: pos, Value: exists}},
			&ast.BreakStmt{Position: pos},
		}},
	}}
	return &ast.ExprBlock{Position: pos, Stmts: []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: name, Mutable: true, Value: &ast.BoolLit{Position: pos, Value: !exists}}, loop,
	}, Value: result}
}
