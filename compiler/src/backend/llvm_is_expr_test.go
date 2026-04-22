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

func TestGenerateLLVMIRLowersIsExprWithAlternativeValueTargets(t *testing.T) {
	src := `const enum Tok of i32:
	LT = 1
	LTEQ = 2
	GT = 3
	GTEQ = 4

def is_rel(kind: Tok) -> bool:
	return kind is .LT | .LTEQ | .GT | .GTEQ
`
	result := parseAndAnalyzeBackendTest(t, "backend_is_expr_alternatives.llcontext", src)
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
	return match tok:
		Token(kind: .INTEGER, span: Span(start: start), value: value):
			start + value
		_:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_pattern_match_is.llcontext", src)
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
	return match op:
		Op.ADD:
			10
		Op.SUB:
			20
		Op.MUL:
			30
`
	result := parseAndAnalyzeBackendTest(t, "backend_const_enum_match.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @apply_stmt(i32 ", "define i64 @apply_expr(i32 ", "match.tag", "match.expr.phi"} {
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
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings_and.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_struct_if_while_bindings_or.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_variant_let_bindings.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(", "define i64 @fallback(", "cond.and.rhs", "cond.let.bind", "store i64"} {
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
		MaybeStep(child step?: Expr)

def score(stmt: Tiny.Stmt) -> i64:
	if stmt is Tiny.Stmt.MaybeStep(step):
		if let step_expr = step:
			if step_expr is Tiny.Expr.IntegerLit(value):
				return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_nested_let_optional.llcontext", src)
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

func TestGenerateLLVMIRLowersTreeSequenceFieldViews(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		Return(child value: Expr)
		ElseIf(child condition: Expr, child body: Block)
		IfStmt(child condition: Expr, child then_block: Block, children elseifs: darray[Stmt], has_else: bool, child else_block: Block)
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
		ElseIf(child condition: Expr, child body: Block)
		IfStmt(child condition: Expr, child then_block: Block, children elseifs: darray[Stmt], child else_block?: Block)
		NumericFor(name_index: u32, child start: Expr, child limit: Expr, child step?: Expr, child body: Block)
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
	for child in children(stmt to Lua.Node):
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

func TestGenerateLLVMIRLowersGuardedTreeVisitExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ExprStmt(child expr: Expr)
	@role(expr)
	node Expr:
		Name(name_index: u32)
		Call(child callee: Expr)
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
		ExprStmt(child expr: Expr)
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

func TestGenerateLLVMIRLowersDirectTreeAttributeReads(t *testing.T) {
	src := `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

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
		Binary(child left: Expr, child right: Expr)

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

func TestGenerateLLVMIRLowersTreeRewriteExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

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
		Binary(child left: Expr, child right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}

def rewrite_binary_explicit(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
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

func TestGenerateLLVMIRLowersTreeRewriteDefaultExpr(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ", "tree.default.src", "tree.default.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree rewrite default lowering to include %q, got:\n%s", check, output)
		}
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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_default_children.llcontext", src)
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
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr default:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_implicit_default_update.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_sequence_rewrite_expr.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_target_sequence_rewrite_expr.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_sequence_rewrite_emit_all_expr.llcontext", src)
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
		Function(child body: Block)
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
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_heterogeneous_expr.llcontext", src)
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
		Binary(child left: Expr, child right: Expr)

def score(node: Lua.Expr) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Int(expr, children) when expr.value > 0:
			expr.value + children.len.i64()
		Lua.Expr.Int(expr, children):
			0
		Lua.Expr.Binary(expr, left, right):
			left + right + expr.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_fold_guard.llcontext", src)
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
