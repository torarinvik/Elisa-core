//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersTreeRewriteDefaultExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ", "tree.default.src.row", "tree.default.store.state", "load %Lua_Expr_Binary__TreeRow", "insertvalue %Lua_Expr_Binary__TreeRow", "store %Lua_Expr_Binary__TreeRow"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree rewrite default lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersCategoryUnionTreeRewriteDefaultExpr(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_rewrite_default.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ",
		"tree.default.kind.ptr",
		"tree.default.src.payload.value",
		"tree.default.payload.memcpy",
		"tree.default.count.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union tree rewrite default lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua_Expr_Binary__TreeTable") || strings.Contains(output, "tree.default.src.row") {
		t.Fatalf("expected category_union rewrite default lowering to avoid exact per-variant rows, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersTreeRewriteDefaultExprWithChildren(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		items: darray[Expr]

def simplify(block: Lua.Block) -> Lua.Block:
	in perm:
		return rewrite block as Lua.Node:
			Lua.Expr.Int(expr):
				default
			Lua.Block(block, items: items):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_default_children.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"tree.default.children", "tree.default.children.memcpy", "@alloc_perm"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree rewrite default children lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeRewriteImplicitDefaultRecordUpdate(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr default:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_implicit_default_update.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ", "tree.default.src", "tree.update.src", "category.default.arm"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected implicit rewrite default lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersSequenceRewriteExpr(t *testing.T) {
	src := `def keep_non_zero(owner: mutable Arena&, items: dview[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[u32]:
				item when item != 0u32:
					emit item
`
	result := parseAndAnalyzeBackendTest(t, "backend_sequence_rewrite_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %DynArray__u32 @keep_non_zero", "sequence.rewrite.loop", "sequence.emit.slot"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected sequence rewrite lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeTargetSequenceRewriteExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Name(name: u32)

def keep_positive_int_values(owner: mutable Arena&, items: dview[Lua.Expr]) -> darray[i64]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[i64]:
				Lua.Expr.Int(expr) when expr.value > 0:
					emit expr.value
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_target_sequence_rewrite_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %DynArray__i64 @keep_positive_int_values", "sequence.rewrite.arm.tree.tag", "sequence.emit.slot"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree-target sequence rewrite lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersSequenceRewriteEmitAllExpr(t *testing.T) {
	src := `def concat(owner: mutable Arena&, left: dview[u32], right: dview[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			segments: darray[dview[u32]] = [left, right]
			return rewrite segments as sequence[u32]:
				segment:
					emit all segment
`
	result := parseAndAnalyzeBackendTest(t, "backend_sequence_rewrite_emit_all_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %DynArray__u32 @concat", "sequence.emit.all.loop", "sequence.emit.all.elem"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected emit-all lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersHeterogeneousTreeRewriteExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Function(body: Block)
	block Block:
		items: darray[Expr]

def clone_expr(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Node:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Function(expr, body: body):
				default
			Lua.Block(block, items: items):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_heterogeneous_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @clone_expr(%Lua__TreeHandle ", "define private %Lua__TreeHandle @tree_fold_", "call %Lua__TreeHandle @tree_fold_", "fold.arm.named.body.value", "fold.arm.named.items.sub.view.len"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected heterogeneous tree rewrite lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersGuardedTreeFoldExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def score(node: Lua.Expr) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Int(expr, children) when expr.value > 0:
			expr.value + children.len.i64()
		Lua.Expr.Int(expr, children):
			0
		Lua.Expr.Binary(expr, left, right):
			left + right + expr.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_fold_guard.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(%Lua__TreeHandle ", "define private i64 @tree_fold_", "guard.body"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded fold lowering to include %q, got:\n%s", check, output)
		}
	}
}
