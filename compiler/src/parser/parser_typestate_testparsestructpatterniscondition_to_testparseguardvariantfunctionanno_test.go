package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/unparse"
	"strings"
	"testing"
)

func TestParseStructPatternIsCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n\ndef is_integer(tok: Token) -> bool:\n    return tok is Token(kind: .INTEGER, span: Span(start: 1))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is-expression return, got %T %#v", ret.Value, ret.Value)
	}
	target, ok := cond.Right.(*ast.StructTestExpr)
	if !ok {
		t.Fatalf("expected struct test target, got %T", cond.Right)
	}
	if target.Pattern == nil || target.Pattern.TypeName != "Token" || len(target.Pattern.Args) != 2 {
		t.Fatalf("unexpected struct is target %#v", target.Pattern)
	}
	if _, ok := target.Pattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected literal kind field pattern, got %T", target.Pattern.Args[0].Pattern)
	}
	spanPattern, ok := target.Pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok || spanPattern.TypeName != "Span" {
		t.Fatalf("expected nested span struct is-pattern, got %T %#v", target.Pattern.Args[1].Pattern, target.Pattern.Args[1].Pattern)
	}
	if len(spanPattern.Args) != 1 || spanPattern.Args[0].Name != "start" {
		t.Fatalf("unexpected span field args %#v", spanPattern.Args)
	}
	if _, ok := spanPattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected literal nested field pattern, got %T", spanPattern.Args[0].Pattern)
	}
}
func TestParseStructPatternIsConditionWithBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n    value: int\n\ndef score(tok: Token) -> int:\n    if tok is Token(kind: .INTEGER, span: Span(start: start), value: value):\n        return start + value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	target, ok := cond.Right.(*ast.StructTestExpr)
	if !ok || target.Pattern == nil {
		t.Fatalf("expected struct test condition, got %T %#v", cond.Right, cond.Right)
	}
	spanPattern, ok := target.Pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected nested span pattern, got %T", target.Pattern.Args[1].Pattern)
	}
	startBind, ok := spanPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || startBind.Name != "start" {
		t.Fatalf("expected nested start bind, got %T %#v", spanPattern.Args[0].Pattern, spanPattern.Args[0].Pattern)
	}
	valueBind, ok := target.Pattern.Args[2].Pattern.(*ast.MatchBindPattern)
	if !ok || valueBind.Name != "value" {
		t.Fatalf("expected value bind, got %T %#v", target.Pattern.Args[2].Pattern, target.Pattern.Args[2].Pattern)
	}
}
func TestParseVariantPatternIsConditionWithBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Pair(left: i64, right: i64)\n\ndef score(node: Expr) -> i64:\n    if node is Expr.Pair(left, right):\n        return left + right\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	target, ok := cond.Right.(*ast.VariantTestExpr)
	if !ok || target.Pattern == nil {
		t.Fatalf("expected variant test condition, got %T %#v", cond.Right, cond.Right)
	}
	leftBind, ok := target.Pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "left" {
		t.Fatalf("expected left bind, got %T %#v", target.Pattern.Args[0].Pattern, target.Pattern.Args[0].Pattern)
	}
	rightBind, ok := target.Pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "right" {
		t.Fatalf("expected right bind, got %T %#v", target.Pattern.Args[1].Pattern, target.Pattern.Args[1].Pattern)
	}
}
func TestParseIfLetCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(maybe: i64?) -> i64:\n    if let value = maybe and value > 0:\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	letExpr, ok := ifStmt.Cond.(*ast.OptionalBindExpr)
	if !ok || letExpr.Name != "value" {
		t.Fatalf("expected let-bind condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	guard, ok := ifStmt.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected let tail condition to lower to nested if, got %T", ifStmt.Then[0])
	}
	if cond, ok := guard.Cond.(*ast.BinaryExpr); !ok || cond.Op != lexer.TOKEN_GT {
		t.Fatalf("expected tail condition, got %T %#v", guard.Cond, guard.Cond)
	}
}
func TestParseIfLetMultipleBindingsLowersToNestedIfs(t *testing.T) {
	file, errs := parseSourceFile(t, `def keep(lower: i64?, upper: i64?) -> void:
    if let actual_lower = lower, actual_upper = upper:
        actual_lower > actual_upper then:
            return
        return
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	outer, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected outer optional bind if, got %T", decl.Body[0])
	}
	outerBind, ok := outer.Cond.(*ast.OptionalBindExpr)
	if !ok || outerBind.Name != "actual_lower" {
		t.Fatalf("expected actual_lower optional bind, got %T %#v", outer.Cond, outer.Cond)
	}
	inner, ok := outer.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected nested optional bind if, got %T", outer.Then[0])
	}
	innerBind, ok := inner.Cond.(*ast.OptionalBindExpr)
	if !ok || innerBind.Name != "actual_upper" {
		t.Fatalf("expected actual_upper optional bind, got %T %#v", inner.Cond, inner.Cond)
	}
	bodyWrapper, ok := inner.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected multi-statement body wrapper, got %T", inner.Then[0])
	}
	guard, ok := bodyWrapper.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected condition then block to lower to if, got %T", bodyWrapper.Then[0])
	}
	if _, ok := guard.Cond.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected condition then binary condition, got %T %#v", guard.Cond, guard.Cond)
	}
}
func TestParseReturnQuestionLowersToIfLetReturn(t *testing.T) {
	file, errs := parseSourceFile(t, "def first(found: i64?) -> i64?:\n    return? found\n    return null\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected return? to lower to if statement, got %T", decl.Body[0])
	}
	letExpr, ok := ifStmt.Cond.(*ast.OptionalBindExpr)
	if !ok || letExpr.Name != "__return_optional" {
		t.Fatalf("expected optional return condition binding, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	ret, ok := ifStmt.Then[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected optional return then-body, got %T", ifStmt.Then[0])
	}
	ident, ok := ret.Value.(*ast.Ident)
	if !ok || ident.Name != "__return_optional" {
		t.Fatalf("expected optional return payload ident, got %T %#v", ret.Value, ret.Value)
	}
}
func TestParsePostfixReturnIfLowersToGuardReturn(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64, stop: bool) -> i64:\n    value return if stop\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected postfix return-if to lower to if statement, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.Ident)
	if !ok || cond.Name != "stop" {
		t.Fatalf("expected stop condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	ret, ok := ifStmt.Then[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected guard return then-body, got %T", ifStmt.Then[0])
	}
	value, ok := ret.Value.(*ast.Ident)
	if !ok || value.Name != "value" {
		t.Fatalf("expected returned value ident, got %T %#v", ret.Value, ret.Value)
	}
}
func TestParseReturnQuestionWithOptionalBindingsLowersToNestedIfs(t *testing.T) {
	file, errs := parseSourceFile(t, `def in_range(lower: i64?, upper: i64?, value: i64?) -> bool?:
    return? with lower_value = lower,
                 upper_value = upper,
                 value_int = value:
        value_int >= lower_value and value_int <= upper_value
    return null
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	outer, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected return? with to lower to if statement, got %T", decl.Body[0])
	}
	outerBind, ok := outer.Cond.(*ast.OptionalBindExpr)
	if !ok || outerBind.Name != "lower_value" {
		t.Fatalf("expected lower_value optional bind, got %T %#v", outer.Cond, outer.Cond)
	}
	middle, ok := outer.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected nested optional bind, got %T", outer.Then[0])
	}
	middleBind, ok := middle.Cond.(*ast.OptionalBindExpr)
	if !ok || middleBind.Name != "upper_value" {
		t.Fatalf("expected upper_value optional bind, got %T %#v", middle.Cond, middle.Cond)
	}
	inner, ok := middle.Then[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected innermost optional bind, got %T", middle.Then[0])
	}
	innerBind, ok := inner.Cond.(*ast.OptionalBindExpr)
	if !ok || innerBind.Name != "value_int" {
		t.Fatalf("expected value_int optional bind, got %T %#v", inner.Cond, inner.Cond)
	}
	ret, ok := inner.Then[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected final return, got %T", inner.Then[0])
	}
	if _, ok := ret.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected final return expression, got %T %#v", ret.Value, ret.Value)
	}
}
func TestParseGuardElseStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(maybe: i64?) -> i64:\n    guard maybe != null else return 0\n    return 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected lowered if stmt, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.UnaryExpr)
	if !ok || cond.Op != lexer.TOKEN_NOT {
		t.Fatalf("expected inverted guard condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	if _, ok := ifStmt.Then[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected guard else branch to lower to return, got %T", ifStmt.Then[0])
	}
}
func TestParseWithBundleSpread(t *testing.T) {
	file, errs := parseSourceFile(t, "bundle ParseCtx implicit:\n    offset: i64\n\ndef inner() with ParseCtx -> i64:\n    return offset\n\ndef keep() -> i64:\n    offset: i64 = 7\n    return inner() with ParseCtx(.., offset = offset)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	ret := decl.Body[1].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok || len(call.WithBundles) != 1 {
		t.Fatalf("expected call with one bundle, got %T %#v", ret.Value, ret.Value)
	}
	bundle := call.WithBundles[0]
	if !bundle.Spread {
		t.Fatalf("expected ParseCtx bundle to record spread marker, got %#v", bundle)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "ParseCtx(.., offset = offset)") {
		t.Fatalf("expected unparse output to preserve bundle spread, got:\n%s", formatted)
	}
}
func TestParseVisitArmAlternativesAndGuard(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Float(value: f64)\n\ndef score(node: Lua.Expr) -> i64:\n    return visit node:\n        Lua.Expr.Int(expr) | Lua.Expr.Float(expr) when expr.span > 0:\n            expr.span\n        _:\n            0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	visitExpr, ok := ret.Value.(*ast.VisitExpr)
	if !ok {
		t.Fatalf("expected visit expr, got %T", ret.Value)
	}
	if len(visitExpr.Arms) != 3 {
		t.Fatalf("expected visit arm alternatives to expand into three arms, got %d", len(visitExpr.Arms))
	}
	for i := 0; i < 2; i++ {
		if visitExpr.Arms[i].Guard == nil {
			t.Fatalf("expected expanded visit arm %d to keep guard", i)
		}
		if visitExpr.Arms[i].BindName != "expr" {
			t.Fatalf("expected expanded visit arm %d bind name expr, got %#v", i, visitExpr.Arms[i])
		}
	}
	if !visitExpr.Arms[2].Wildcard {
		t.Fatalf("expected final wildcard arm, got %#v", visitExpr.Arms[2])
	}
}
func TestParseStructDeclWithNamedStateCasesAndDeriveBlock(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Player[state Alive | Dead]:\n    health: int\n\n    derive state:\n        Alive when self.health > 0\n        Dead when self.health <= 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.NamedStateCases) != 2 || decl.NamedStateCases[0] != "Alive" || decl.NamedStateCases[1] != "Dead" {
		t.Fatalf("expected named state cases [Alive Dead], got %#v", decl.NamedStateCases)
	}
	if len(decl.GenericParams) != 1 || decl.GenericParams[0].Kind != ast.GenericParamState {
		t.Fatalf("expected a single struct state generic param, got %#v", decl.GenericParams)
	}
	if len(decl.DerivedStates) != 2 {
		t.Fatalf("expected two derived-state clauses, got %d", len(decl.DerivedStates))
	}
	if decl.DerivedStates[0].StateName != "Alive" || decl.DerivedStates[1].StateName != "Dead" {
		t.Fatalf("unexpected derived-state clause names: %#v", decl.DerivedStates)
	}
	if _, ok := decl.DerivedStates[0].Condition.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected first derived-state condition to be binary, got %T", decl.DerivedStates[0].Condition)
	}
}
func TestParseNamedStateUnionTypeExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Player[Alive | Dead]) -> Player[Alive | Dead]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	paramType, ok := decl.Params[0].Type.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected generic type, got %T", decl.Params[0].Type)
	}
	if len(paramType.Args) != 1 {
		t.Fatalf("expected one state arg, got %d", len(paramType.Args))
	}
	stateSet, ok := paramType.Args[0].(*ast.StateSetTypeExpr)
	if !ok {
		t.Fatalf("expected state-set type arg, got %T", paramType.Args[0])
	}
	if len(stateSet.Cases) != 2 || stateSet.Cases[0] != "Alive" || stateSet.Cases[1] != "Dead" {
		t.Fatalf("expected Alive | Dead state-set, got %#v", stateSet.Cases)
	}
}
func TestParseTypedStateIsConditionAndExplicitStatefulStructLiteral(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(node: Player[Alive | Dead]) -> Player[Alive]:\n    if node is Player[Alive]:\n        return Player[Alive](1)\n    return Player[Alive](2)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", decl.Body[0])
	}
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	typed, ok := cond.Right.(*ast.TypeExprExpr)
	if !ok {
		t.Fatalf("expected typed is target, got %T", cond.Right)
	}
	target, ok := typed.Type.(*ast.GenericType)
	if !ok || target.Name != "Player" || len(target.Args) != 1 {
		t.Fatalf("expected Player[Alive] is target, got %#v", typed.Type)
	}
	ret := ifStmt.Then[0].(*ast.ReturnStmt)
	lit, ok := ret.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected explicit stateful struct literal, got %T", ret.Value)
	}
	if lit.Name != "Player" || len(lit.TypeArgs) != 1 {
		t.Fatalf("expected Player[Alive](...) literal, got %#v", lit)
	}
	if _, ok := lit.TypeArgs[0].(*ast.NamedType); !ok {
		t.Fatalf("expected named state type arg, got %T", lit.TypeArgs[0])
	}
}
func TestParsePackedIfPayloadDestructureStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Add(left: int, right: int)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store as Expr.Add(left: lhs, right: rhs):\n        return lhs + rhs + node.span\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected variant pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if len(pattern.Args) != 2 {
		t.Fatalf("expected two destructured payload args, got %d", len(pattern.Args))
	}
	if pattern.Args[0].Name != "left" || pattern.Args[1].Name != "right" {
		t.Fatalf("expected named payload bindings left/right, got %#v", pattern.Args)
	}
	leftBind, ok := pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "lhs" {
		t.Fatalf("expected lhs bind pattern, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	rightBind, ok := pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "rhs" {
		t.Fatalf("expected rhs bind pattern, got %T %#v", pattern.Args[1].Pattern, pattern.Args[1].Pattern)
	}
}
func TestParseAlignedStructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@align(32)\nstruct Vec4:\n    x: f32\n    y: f32\n    z: f32\n    w: f32\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "align" {
		t.Fatalf("expected align annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "32" {
		t.Fatalf("expected align(32), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseCachelineAlignedStructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@cacheline_aligned\nstruct Counter:\n    value: i64\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "cacheline_aligned" {
		t.Fatalf("expected cacheline_aligned annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected cacheline_aligned to take no args, got %#v", decl.Annotations[0].Args)
	}
}
func TestParsePackedEnumProfileAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_profile(build_heavy)\npacked enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one enum annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "packed_profile" {
		t.Fatalf("expected packed_profile annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "build_heavy" {
		t.Fatalf("expected packed_profile(build_heavy), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseInlineFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@inline(always)\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "inline" {
		t.Fatalf("expected inline annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "always" {
		t.Fatalf("expected inline(always), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseNoRecurseFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@norecurse\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "norecurse" {
		t.Fatalf("expected norecurse annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected norecurse to take no args, got %#v", decl.Annotations[0].Args)
	}
}
func TestParseHotFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@hot\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "hot" {
		t.Fatalf("expected hot annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected hot to take no args, got %#v", decl.Annotations[0].Args)
	}
}
func TestParseGuardNonNullFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_nonnull(box)\ndef has_box(box: Box&?) -> bool:\n    return box != null\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_nonnull" {
		t.Fatalf("expected guard_nonnull annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "box" {
		t.Fatalf("expected guard_nonnull(box), got %#v", decl.Annotations[0].Args)
	}
}
func TestParseGuardVariantFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_variant(node, Expr.Int)\ndef is_int(node: Expr) -> bool:\n    return node is Expr.Int\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_variant" {
		t.Fatalf("expected guard_variant annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 2 || decl.Annotations[0].Args[0] != "node" || decl.Annotations[0].Args[1] != "Expr.Int" {
		t.Fatalf("expected guard_variant(node, Expr.Int), got %#v", decl.Annotations[0].Args)
	}
}
