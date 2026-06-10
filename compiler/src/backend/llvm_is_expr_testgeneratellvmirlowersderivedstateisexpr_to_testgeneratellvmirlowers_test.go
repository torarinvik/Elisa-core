//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersDerivedStateIsExpr(t *testing.T) {
	src := `struct Player[state Alive | Dead]:
    health: int

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def score(player: Player) -> int:
    if player is Player[Alive]:
        return player.health
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_derived_state_is_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"isstate.field.health", "isstate.icmp"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected derived-state is lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersEnumIsExprWithLiteralPayloadPattern(t *testing.T) {
	src := `enum Expr:
	Float(PI: f64)
	Int(value: int)

def is_pi(node: Expr) -> bool:
	return node is Expr.Float(3.14)
`
	result := parseAndAnalyzeBackendTest(t, "backend_enum_is_expr_literal_payload.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @is_pi(", "fcmp oeq double", "is.variant.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected literal-payload is lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersEnumVariantViewAfterIsExpr(t *testing.T) {
	src := `enum Expr:
	Pair(left: i64, right: i64)
	Int(value: i64)

def score(node: Expr) -> i64:
	if node is Expr.Pair:
		return node.left + node.right
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_enum_variant_view_after_is.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "enum.payload.ptr", "match.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enum variant view lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersAliasedEnumVariantViewAfterIsExpr(t *testing.T) {
	src := `enum Expr:
	Pair(left: i64, right: i64)
	Int(value: i64)

def score(node: Expr) -> i64:
	if node is Expr.Pair as pair:
		return pair.left + pair.right
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_enum_variant_view_alias_after_is.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "enum.payload.ptr", "match.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected aliased enum variant view lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersPatternTernaryBindings(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return value if node is Expr.Int(value) else 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_pattern_ternary_bindings.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @unwrap(", "ternary.then", "match.pattern.ok", "store i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected pattern ternary lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersRefinedVariantValueInContextualTernary(t *testing.T) {
	src := `enum Expr:
    Pair(left: i64, right: i64)
    Int(value: i64)

def choose(node: Expr, fallback: Expr) -> Expr:
    return node if node is Expr.Pair else fallback
`
	result := parseAndAnalyzeBackendTest(t, "backend_refined_variant_contextual_ternary.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define", "@choose", "ternary.then", "termp"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected contextual ternary variant lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersPatternGuardReturn(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return value if node is Expr.Int(value) else 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_pattern_guard_return.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @unwrap(", "ternary.then", "match.pattern.ok", "store i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected pattern guard return lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersIsExprWithAlternativeValueTargets(t *testing.T) {
	src := `const enum Tok of i32:
	LT = 1
	LTEQ = 2
	GT = 3
	GTEQ = 4

def is_rel(kind: Tok) -> bool:
	return kind is [.LT | .LTEQ | .GT | .GTEQ]
`
	result := parseAndAnalyzeBackendTest(t, "backend_is_expr_alternatives.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @is_rel(i32 ", "isvalue.eq", "istest.or"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected alternative is lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBracketedAndGroupedQualifiedAlternativesEquivalently(t *testing.T) {
	src := `enum Expr:
	Int
	Bool
	Char
	Missing

def grouped_scalar(value: Expr) -> bool:
	return value is (
		Expr.Int
		| Expr.Bool
		| Expr.Char
	)

def new_scalar(value: Expr) -> bool:
	return value is [Expr.Int | Expr.Bool | Expr.Char]
`
	result := parseAndAnalyzeBackendTest(t, "backend_is_expr_qualified_alternative_equivalence.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @grouped_scalar(", "define i1 @new_scalar(", "icmp eq i32", "istest.or"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected qualified alternative is lowering to include %q, got:\n%s", check, output)
		}
	}
	if count := strings.Count(output, "istest.or"); count < 2 {
		t.Fatalf("expected both bracketed and grouped alternatives to lower through istest.or, saw %d occurrences in:\n%s", count, output)
	}
}

func TestGenerateLLVMIRLowersIsNotExpr(t *testing.T) {
	src := `const enum Tok of i32:
	IDENT = 1
	NUMBER = 2

def keep(kind: Tok) -> bool:
	return kind is not .IDENT
`
	result := parseAndAnalyzeBackendTest(t, "backend_is_not_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i32 ", "isvalue.eq", "nottmp"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected is-not lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExpectFieldShapeListRestPattern(t *testing.T) {
	src := `struct Block:
    stmts: int[3]

def check(block: Block) -> void:
    can Abort.Panic:
        expect block as {stmts: [1, 2, ...]}
`
	result := parseAndAnalyzeBackendTest(t, "backend_expect_field_shape_list_rest.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"expect.ok", "expect.fail", "match.list.items", "match.literal.eq"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected field-shape/list-rest expect lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersExpectVariantPayloadFieldShapeListRestPattern(t *testing.T) {
	src := `struct Block:
    stmts: int[3]

enum Stmt:
    While(condition: int, body: Block)

def check(stmt: Stmt) -> void:
    can Abort.Panic:
        expect stmt as Stmt.While(_, {stmts: [1, 2, ...]})
`
	result := parseAndAnalyzeBackendTest(t, "backend_expect_variant_payload_field_shape_list_rest.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"expect.ok", "expect.fail", "match.list.items", "match.literal.eq"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected variant-payload field-shape/list-rest expect lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersStructFieldPatternsInIsAndMatch(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Span:
	start: i64
	finish: i64

struct Token:
	kind: Tok
	span: Span
	value: i64

def is_integer(tok: Token) -> bool:
	return tok is Token(kind: .INTEGER)

def score(tok: Token) -> i64:
	match tok:
		Token(kind: .INTEGER, span: Span(start: start), value: value):
			return start + value
		_:
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_pattern_match_is.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @is_integer(", "define i64 @score(", "is.struct.result", "match.struct.field", "extractvalue"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected struct-pattern lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersConstEnumMatchStatementsAndExpressions(t *testing.T) {
	src := `const enum Op of i32:
	ADD = 1
	SUB = 2
	MUL = 3

def apply_stmt(op: Op) -> i64:
	match op:
		Op.ADD:
			return 10
		Op.SUB:
			return 20
		_:
			return 30

def apply_expr(op: Op) -> i64:
	match op:
		Op.ADD:
			return 10
		Op.SUB:
			return 20
		Op.MUL:
			return 30
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_const_enum_match.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @apply_stmt(i32 ", "define i64 @apply_expr(i32 ", "match.tag"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected const-enum match lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersStructIsConditionBindingsInIfAndWhile(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Span:
	start: i64
	finish: i64

struct Token:
	kind: Tok
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, span: Span(start: start), value: value):
		return start + value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value):
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "define i64 @loop_value(", "cond.struct.field", "store i64", "match.literal.eq"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected struct condition binding lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersStructIsConditionBindingsThroughAnd(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Token:
	kind: Tok
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) and value > 0:
		return value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value) and value > 0:
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings_and.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "define i64 @loop_value(", "cond.and.rhs", "cond.struct.field", "load i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected short-circuit struct condition binding lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersStructIsConditionBindingsThroughTruthyOr(t *testing.T) {
	src := `const enum Tok of i32:
	INTEGER = 1
	FLOAT = 2

struct Token:
	kind: Tok
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT, value: value):
		return value
	return 0

def loop_value(tok: Token) -> i64:
	while tok is Token(kind: .INTEGER, value: value) or tok is Token(kind: .FLOAT, value: value):
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings_or.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "define i64 @loop_value(", "cond.or.rhs", "cond.struct.field", "load i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected truthy-or struct condition binding lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersVariantAndLetConditionBindings(t *testing.T) {
	src := `enum Expr:
	Int(value: i64)
	Pair(left: i64, right: i64)

def score(node: Expr, maybe: i64?, enabled: bool) -> i64:
	guard enabled else return 0
	if maybe is value and node is Expr.Pair(left, right):
		return value + left + right
	return 0

def fallback(maybe: i64?) -> i64:
	if maybe is value:
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_variant_let_bindings.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "define i64 @fallback(", "cond.let.bind", "match.pattern.ok", "store i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected variant/let condition lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRResolvesBarePackedVariantWitnessType(t *testing.T) {
	src := "packed enum Expr:\n\tcommon:\n\t\tspan: int\n\tLit(value: int)\n\tAdd(left: Expr, right: Expr)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n\tif node in store is Expr.Lit(value: value):\n\t\tlit: Expr.Lit = node\n\t\treturn value + lit.span\n\treturn 0\n"
	result := parseAndAnalyzeBackendTest(t, "backend_bare_packed_variant.elisa", src)
	if _, err := generateLLVMIRWithDefaultPackedLoweringForTest(result); err != nil {
		t.Fatalf("expected bare packed-variant witness type to lower, got: %v", err)
	}
}
