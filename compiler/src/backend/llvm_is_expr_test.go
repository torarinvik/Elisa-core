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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_is_expr.llcontext", src)
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
	alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
	in alloc:
		left: Lua.Expr = Lua.Expr.Nil(span: 1)
		right: Lua.Expr = new[alloc] Lua.Expr.Nil(span: 2)
		return Lua.Expr.Binary(span: 3, left: left, right: right)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_owner_scope.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_treeview_surface.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_bare_variant_surface.llcontext", src)
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
	for _, check := range []string{"define i64 @child_span(%Lua__TreeHandle ", "define i64 @eval(%Lua__TreeHandle ", "match.tree.tag", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree match lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTreeOpenAndViewStatements(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node

def child_span(node: Lua.Expr) -> i64:
	view node as Lua.Expr.Binary(binary):
		kept: treeview[Lua.Expr.Binary] = keep_binary(binary)
		return kept.left.span + binary.right.span + node.left.span
	return node.span

def left_value(node: Lua.Expr) -> i64:
	open node as Lua.Expr.Binary(Lua.Expr.Int(value), rhs):
		return value + rhs.span
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_open_view.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @child_span(%Lua__TreeHandle ", "define i64 @left_value(%Lua__TreeHandle ", "call %Lua__TreeHandle @keep_binary(%Lua__TreeHandle ", "match.tree.tag", "tree.field.column.ptr", "call void @llvm.trap()"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree open/view lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected tree open/view lowering to keep treeview as the existing tree handle carrier, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersTreeChildrenLoops(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Unary(op: i32, child expr: Expr)
		Binary(op: i32, child left: Expr, child right: Expr)
		Call(child callee: Expr, children args: darray[Expr], link source_expr: Expr)

def count_nodes(node: Lua.Expr) -> i64:
	total: mutable i64 = 1
	for child in children(node):
		total <- total + count_nodes(child)
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_children.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"TreeChildren", "tree.children.node.insert", "tree.children.count.phi", "tree.children.value.phi"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree children lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMixedTreeChildrenToRootLoops(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		IfStmt(child condition: Lua.Expr, child body: Lua.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Lua.Stmt]

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt to Lua.Node):
		total <- total + child.kind.i64()
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_children_root.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @count_children(%Lua__TreeHandle ", "TreeChildren", "tree.children.value.phi", "tree.field.kind.tag.trunc"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected mixed tree children lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTreeVisitExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def score(node: Lua.Expr) -> i64:
	return visit node:
		Lua.Expr.Nil(expr):
			expr.span
		Lua.Expr.Int(expr):
			expr.value
		Lua.Expr.Binary(expr):
			expr.left.span + expr.right.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_expr.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(%Lua__TreeHandle ", "visit.expr.arm", "visit.expr.phi", "match.tree.tag", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree visit lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTreeKindFieldAndShorthandMembers(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)
	@role(stmt)
	node Stmt:
		Return(value: Expr)
	block Block:
		stmts: darray[Stmt]

def is_binary(node: Lua.Expr) -> bool:
	return node.kind == .Binary

def stmt_is_return(stmt: Lua.Stmt) -> bool:
	return stmt.kind == .Return

def binary_kind(node: Lua.Expr.Binary) -> Lua.Expr.Kind:
	return node.kind

def node_kind(node: Lua.Node) -> Lua.Node.Kind:
	return node.kind

def node_is_binary(node: Lua.Node) -> bool:
	return node.kind == .Expr.Binary

def block_kind(node: Lua.Block) -> Lua.Node.Kind:
	return node.kind

def explicit_binary_kind() -> Lua.Expr.Kind:
	return Lua.Expr.Kind.Binary

def explicit_root_binary_kind() -> Lua.Node.Kind:
	return Lua.Node.Kind.Expr.Binary
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_kind.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @is_binary(%Lua__TreeHandle ", "define i1 @stmt_is_return(%Lua__TreeHandle ", "define i32 @binary_kind(%Lua__TreeHandle ", "define i32 @node_kind(%Lua__TreeHandle ", "define i1 @node_is_binary(%Lua__TreeHandle ", "define i32 @block_kind(%Lua__TreeHandle ", "define i32 @explicit_binary_kind()", "define i32 @explicit_root_binary_kind()", "icmp eq i32"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree kind lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "tree.field.column.ptr") {
		t.Fatalf("expected tree kind lowering to avoid tree field column loads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersExactTreeVisitExpr(t *testing.T) {
	src := `tree Lua:
	@role(stmt)
	node Stmt:
		ExprStmt(child expr: Expr)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def stmt_total(block: Lua.Block) -> i64:
	return visit block:
		Lua.Block(node):
			node.stmts.count.i64()
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_exact_expr.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @stmt_total(%Lua__TreeHandle ", "visit.exact.arm", "visit.exact.end"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected exact tree visit lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTreeFoldExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Call(child callee: Expr, children args: darray[Expr])
		Binary(child left: Expr, child right: Expr)

def score(node: Lua.Expr) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Nil(expr, children):
			expr.span + children.len.i64()
		Lua.Expr.Int(expr, children):
			expr.value + children.len.i64()
		Lua.Expr.Call(expr, callee, args: arg_values):
			callee + arg_values.len.i64() + expr.span
		Lua.Expr.Binary(expr, left, right):
			left + right + expr.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_fold_expr.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(%Lua__TreeHandle ", "define private i64 @tree_fold_", "call i64 @tree_fold_", "fold.arm.buffer", "fold.arm.view.len", "fold.arm.named.args.sub.view.len", "fold.arm.named.left.value", "fold.arm.named.right.value"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree fold lowering to include %q, got:\n%s", check, output)
		}
	}
}
