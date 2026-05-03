//go:build cgo

package backend

import (
	"strings"
	"testing"
)

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
	if node as Lua.Expr.Binary:
		binary: treeview[Lua.Expr.Binary] = node
		kept: treeview[Lua.Expr.Binary] = keep_binary(binary)
		return kept.left.span + binary.right.span + node.left.span
	return node.span

def left_value(node: Lua.Expr) -> i64:
	if node as Lua.Expr.Binary(Lua.Expr.Int(value), rhs):
		return value + rhs.span
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_open_view.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @child_span(%Lua__TreeHandle ", "define i64 @left_value(%Lua__TreeHandle ", "call %Lua__TreeHandle @keep_binary(%Lua__TreeHandle ", "match.tree.tag", "tree.field.column.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree open/view lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected tree if-pattern lowering to keep treeview as the existing tree handle carrier, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersTreeChildrenLoops(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Unary(op: i32, expr: Expr)
		Binary(op: i32, left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr], link source_expr: Expr)

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
func TestGenerateLLVMIRLowersEnumerateTupleLoops(t *testing.T) {
	src := `def sum_pairs(items: darray[usize]) -> usize:
	total: mutable usize = 0
	for index, value in enumerate(items):
		total <- total + index + value
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_enumerate_tuple_loop.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @sum_pairs(", "enumerate.source.insert", "enumerate.item.index.insert", "enumerate.item.value.insert", "iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enumerate tuple loop lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersMixedTreeChildrenToRootLoops(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		IfStmt(condition: Lua.Expr, body: Lua.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Lua.Stmt]

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt as Lua.Node):
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
func TestGenerateLLVMIRLowersTreeSequenceFieldViews(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		Return(value: Expr)
		ElseIf(condition: Expr, body: Block)
		IfStmt(condition: Expr, then_block: Block, elseifs: darray[Stmt], has_else: bool, else_block: Block)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def block_total(block: Lua.Block) -> i64:
	total: mutable i64 = block.stmts.len.i64()
	for stmt in block.stmts:
		total <- total + stmt.kind.i64()
	return total + block.stmts[0].kind.i64()

def elseif_total(stmt: Lua.Stmt.IfStmt) -> i64:
	total: mutable i64 = stmt.elseifs.len.i64()
	for branch in stmt.elseifs:
		total <- total + branch.kind.i64()
	return total + stmt.elseifs[0].kind.i64()
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_sequence_fields.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @block_total(%Lua__TreeHandle ", "define i64 @elseif_total(%Lua__TreeHandle ", "DynArrayView", "tree.field.surface.view.len", "tree.field.surface.view.elem_size", "iter.len.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree sequence field lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersOptionalTreeChildFields(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ElseIf(condition: Expr, body: Block)
		IfStmt(condition: Expr, then_block: Block, elseifs: darray[Stmt], else_block?: Block)
		NumericFor(name_index: u32, start: Expr, limit: Expr, step?: Expr, body: Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Stmt]

def optional_i64_value(value: i64?) -> i64:
	return value if value != null else 0

def has_else(stmt: Lua.Stmt.IfStmt) -> bool:
	else_block: Lua.Block? = stmt.else_block
	return else_block != null

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt as Lua.Node):
		total <- total + child.kind.i64()
	return total

def score(node: Lua.Stmt) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Name(expr):
			expr.name_index.i64() + expr.span
		Lua.Block(block, children):
			children.len.i64() + block.span
		Lua.Stmt.ElseIf(stmt, condition, body):
			condition + body + stmt.span
		Lua.Stmt.IfStmt(stmt, condition, then_block, elseifs: elseif_values, else_block):
			optional_i64_value(else_block) + condition + then_block + elseif_values.len.i64() + stmt.span
		Lua.Stmt.NumericFor(stmt, start, limit, step, body):
			optional_i64_value(step) + start + limit + body + stmt.name_index.i64()
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_optional_child_fields.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @has_else(%Lua__TreeHandle ", "define i64 @count_children(%Lua__TreeHandle ", "define i64 @score(%Lua__TreeHandle ", "Optional__Lua_Block", "Optional__Lua_Expr", "Optional__i64", "optional.present", "fold.arm.named.else_block.value", "fold.arm.named.step.value", "fold.arm.child.edge.count"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected optional tree child lowering to include %q, got:\n%s", check, output)
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
		Binary(left: Expr, right: Expr)

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
func TestGenerateLLVMIRLowersGuardedTreeVisitExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ExprStmt(expr: Expr)
	@role(expr)
	node Expr:
		Name(name_index: u32)
		Call(callee: Expr)
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def score_expr(node: Lua.Expr) -> i64:
	return visit node:
		Lua.Expr.Int(expr) when expr.value > 0:
			expr.value
		_:
			0

def score_node(node: Lua.Node) -> i64:
	return visit node as Lua.Node:
		Lua.Stmt.ExprStmt(stmt) when stmt.expr.kind == .Call:
			stmt.span + 1
		Lua.Stmt.ExprStmt(stmt) when stmt.expr.kind == .Name:
			stmt.span + 2
		_:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_guard.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_expr(%Lua__TreeHandle ", "define i64 @score_node(%Lua__TreeHandle ", "visit.expr.guard.body", "visit.node.exact.guard.body", "visit.node.exact.phi"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded visit lowering to include %q, got:\n%s", check, output)
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
		ExprStmt(expr: Expr)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def stmt_total(block: Lua.Block) -> i64:
	return visit block:
		Lua.Block(node):
			node.stmts.len.i64()
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
		Call(callee: Expr, args: darray[Expr])
		Binary(left: Expr, right: Expr)

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
func TestGenerateLLVMIRLowersDirectTreeAttributeReads(t *testing.T) {
	src := `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.checksum -> i64:
	Lua.Expr.Int(expr):
		return expr.value
	Lua.Expr.Binary(expr, left, right):
		return left.checksum + right.checksum

def checksum_of(node: Lua.Expr) -> i64:
	return node.checksum
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_direct.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @checksum_of(%Lua__TreeHandle ", "define private i64 @tree_attr_", "tree.attr.call", "add i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected direct tree attribute lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersProjectedTreeAttributeReads(t *testing.T) {
	src := `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.node_count -> usize:
	Lua.Expr.Int(_):
		return 1u
	Lua.Expr.Binary(expr, left, right):
		total: mutable usize = 1u
		for child_count in children.node_count:
			total <- total + child_count
		return total

def count_of(node: Lua.Expr) -> usize:
	return node.node_count
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_projected.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @count_of(%Lua__TreeHandle ", "define private i64 @tree_attr_", "TreeAttributeSeq", "call i64 @tree_attr_"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected projected tree attribute lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeAttributeAggregateHelpers(t *testing.T) {
	src := `tree Lua:
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.is_leaf -> bool:
	Lua.Expr.Nil(_):
		return true
	Lua.Expr.Binary(_):
		return false

attribute Lua.Expr.has_control_flow -> bool:
	Lua.Expr.Nil(_):
		return false
	Lua.Expr.Binary(expr, left, right):
		return any(children.has_control_flow)

def has_control(node: Lua.Expr) -> bool:
	return node.has_control_flow

def all_children_leaf(node: Lua.Expr) -> bool:
	return all(children(node).is_leaf)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_aggregate_helpers.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @has_control(%Lua__TreeHandle ", "define i1 @all_children_leaf(%Lua__TreeHandle ", "any.cond", "all.cond", "TreeAttributeSeq", "call i1 @tree_attr_"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree attribute aggregate helper lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeRewriteExpr(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_expr.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ", "define private %Lua__TreeHandle @tree_fold_", "call %Lua__TreeHandle @tree_fold_", "fold.arm.named.left.value", "fold.arm.named.right.value"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree rewrite lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersExactTreeRecordUpdates(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}

def rewrite_binary_explicit(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	return new[alloc] node{left, right}
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_exact_update.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @rewrite_binary(%Lua__TreeHandle ", "define %Lua__TreeHandle @rewrite_binary_explicit(%Arena ", "tree.update.src", "tree.update.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected exact tree update lowering to include %q, got:\n%s", check, output)
		}
	}
}
