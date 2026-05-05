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
func TestGenerateLLVMIRLowersReturnQuestionPatternGuard(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return? value if node is Expr.Int(value)
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_return_question_pattern_guard.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @unwrap(", "if.then", "match.pattern.ok", "store i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected return? pattern guard lowering to include %q, got:\n%s", check, output)
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
	return kind is .LT | .LTEQ | .GT | .GTEQ
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
func TestGenerateLLVMIRLowersExpectTreeBlockFieldShapeListPattern(t *testing.T) {
	src := `tree Perl:
    block Block:
        stmts: darray[Stmt]

    node Stmt:
        While(condition: int, body: Block)
        Last
        Next

def check(stmt: Perl.Stmt) -> void:
    can Abort.Panic:
        expect stmt as Perl.Stmt.While(_, {stmts: [Perl.Stmt.Next, Perl.Stmt.Last]})
`
	result := parseAndAnalyzeBackendTest(t, "backend_expect_tree_block_field_shape_list.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"expect.ok", "expect.fail", "match.struct.field", "match.list.items"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree-block field-shape/list expect lowering to include %q, got:\n%s", check, output)
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
	if let value = maybe and node is Expr.Pair(left, right):
		return value + left + right
	return 0

def fallback(maybe: i64?) -> i64:
	if let value = maybe:
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
func TestGenerateLLVMIRLowersNestedLetOverTreeConditionBoundOptional(t *testing.T) {
	src := `tree Tiny:
	common:
		span: i64
	@role(expr)
	node Expr:
		IntegerLit(value: i64)
	@role(stmt)
	node Stmt:
		MaybeStep(step?: Expr)

def score(stmt: Tiny.Stmt) -> i64:
	if stmt is Tiny.Stmt.MaybeStep(step):
		if let step_expr = step:
			if step_expr is Tiny.Expr.IntegerLit(value):
				return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_nested_let_optional.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	body := output
	for _, check := range []string{"load %Optional__Tiny_Expr", "optional.present", "cond.let.bind"} {
		if !strings.Contains(body, check) {
			t.Fatalf("expected nested tree let lowering to include %q, got:\n%s", check, body)
		}
	}
	for _, bad := range []string{"br i1 false, label %cond.let.bind", "store %Tiny__TreeHandle zeroinitializer, ptr %step_expr.cond"} {
		if strings.Contains(body, bad) {
			t.Fatalf("expected nested tree let lowering to avoid %q, got:\n%s", bad, body)
		}
	}
}
func TestGenerateLLVMIRLowersTreeConstructorsAndIsExprPatterns(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	in perm:
		return Lua.Expr.Nil(span: 1)

def make_binary(span: i64, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in perm:
		return Lua.Expr.Binary(span: span, left: left, right: right)

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return node.left.span
	return node.span

def starts_with_nil(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary(Lua.Expr.Nil, _)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_is_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"%Lua__TreeHandle = type { ptr, i64 }", "%Lua__TreeStoreState = type {", "@Lua__perm_tree_store", "define %Lua__TreeHandle @make_binary(i64 ", "is.tree.variant.result", "match.tree.tag", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree is lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeConstructorsWithArenaOwners(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def build(owner: Arena) -> Lua.Expr:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	in alloc:
		left: Lua.Expr = Lua.Expr.Nil(span: 1)
		right: Lua.Expr = new[alloc] Lua.Expr.Nil(span: 2)
		return Lua.Expr.Binary(span: 3, left: left, right: right)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_owner_scope.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@arena_alloc") {
		t.Fatalf("expected tree arena owner lowering to call arena_alloc, got:\n%s", output)
	}
	if strings.Contains(output, "@alloc_perm") {
		t.Fatalf("expected tree arena owner lowering to avoid alloc_perm, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersFirstClassTreeViewWithoutExtraCarrier(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def score_binary(view_node: treeview[Lua.Expr.Binary]) -> i64:
	return view_node.left.span + view_node.right.span + view_node.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_treeview_surface.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_binary(%Lua__TreeHandle ", "call i64 @score_binary(%Lua__TreeHandle ", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected first-class treeview lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected treeview surface type to lower through the existing tree handle carrier, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersBareTreeVariantSurfaceTypeWithoutExtraCarrier(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def score_binary(view_node: Lua.Expr.Binary) -> i64:
	return view_node.left.span + view_node.right.span + view_node.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_bare_variant_surface.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_binary(%Lua__TreeHandle ", "call i64 @score_binary(%Lua__TreeHandle ", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected bare tree variant surface lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected bare tree variant surface type to lower through the existing tree handle carrier, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersTreeMatchStatementsAndExpressions(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def child_span(node: Lua.Expr) -> i64:
	match node:
		Lua.Expr.Binary(left: left, right: _):
			return node.left.span + left.span
		_:
			return node.span

def eval(node: Lua.Expr) -> i64:
	match node:
		Lua.Expr.Nil:
			return 0
		Lua.Expr.Int(value: value):
			return value
		Lua.Expr.Binary(left: Lua.Expr.Int(value: lhs), right: right):
			return lhs + eval(right)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_match.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @child_span(%Lua__TreeHandle ", "define i64 @eval(%Lua__TreeHandle ", "match.tree.tag", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree match lowering to include %q, got:\n%s", check, output)
		}
	}
}
