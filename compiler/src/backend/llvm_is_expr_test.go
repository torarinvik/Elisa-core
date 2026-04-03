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
	result := parseAndAnalyzeBackendTest(t, "backend_derived_state_is_expr.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_enum_is_expr_literal_payload.llcontext", src)
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

func TestGenerateLLVMIRLowersTreeConstructorsAndIsExprPatterns(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	expr Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	return Lua.Expr.Nil(span: 1)

def make_binary(span: i64, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	return Lua.Expr.Binary(span: span, left: left, right: right)

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return node.left.span
	return node.span

def starts_with_nil(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary(Lua.Expr.Nil, _)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_is_expr.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"%Lua_Expr__Node = type { i32, i64, [2 x i64] }", "declare ptr @alloc_perm(i64)", "define ptr @make_binary(i64 ", "is.tree.variant.result", "match.tree.tag", "tree.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree is lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFirstClassTreeViewWithoutExtraCarrier(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	expr Expr:
		Nil
		Binary(left: Expr, right: Expr)

def score_binary(view_node: treeview[Lua.Expr.Binary]) -> i64:
	return view_node.left.span + view_node.right.span + view_node.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_treeview_surface.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_binary(ptr ", "call i64 @score_binary(ptr ", "tree.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected first-class treeview lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected treeview surface type to lower through the existing tree pointer carrier, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersBareTreeVariantSurfaceTypeWithoutExtraCarrier(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	expr Expr:
		Nil
		Binary(left: Expr, right: Expr)

def score_binary(view_node: Lua.Expr.Binary) -> i64:
	return view_node.left.span + view_node.right.span + view_node.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_bare_variant_surface.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_binary(ptr ", "call i64 @score_binary(ptr ", "tree.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected bare tree variant surface lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected bare tree variant surface type to lower through the existing tree pointer carrier, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersTreeMatchStatementsAndExpressions(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	expr Expr:
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
	return match node:
		Lua.Expr.Nil:
			0
		Lua.Expr.Int(value: value):
			value
		Lua.Expr.Binary(left: Lua.Expr.Int(value: lhs), right: right):
			lhs + eval(right)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_match.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @child_span(ptr ", "define i64 @eval(ptr ", "match.tree.tag", "tree.payload.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree match lowering to include %q, got:\n%s", check, output)
		}
	}
}
