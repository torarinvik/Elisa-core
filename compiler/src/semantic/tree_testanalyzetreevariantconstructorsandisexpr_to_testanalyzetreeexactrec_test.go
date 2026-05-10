package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeTreeVariantConstructorsAndIsExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_behaviors.elisa", `tree Lua:
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

def has_nil_left_named(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary(left: Lua.Expr.Nil)

def has_any_right_named(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary(right: _)

def branch_on_nil_left_named(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary(left: Lua.Expr.Nil):
		return 1
	return 0

def bind_right_named(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary(right: rhs):
		return rhs.span
	return 0
`)
}
func TestAnalyzeStructPatternIsConditionWithBindings(t *testing.T) {
	analyzeTreeTestSource(t, "struct_is_condition_bindings.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, span: Span(start: start), value: value):
		return start + value
	return 0
`)
}
func TestAnalyzeStructPatternIsConditionTruthyOrBindings(t *testing.T) {
	analyzeTreeTestSource(t, "struct_is_condition_truthy_or_bindings.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, value: value) or tok is Token(kind: 2, value: value):
		return value
	return 0
`)
}
func TestAnalyzeStructPatternIsConditionRejectsMissingNestedField(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_is_condition_missing_nested_field.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, span: Span(missing: start), value: value):
		return start + value
	return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `struct "Span" has no field "missing"`) {
		t.Fatalf("expected nested struct is-pattern diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTreeConstructorsSupportExplicitAndScopedOwners(t *testing.T) {
	analyzeTreeTestSource(t, "tree_owner_surface.elisa", `tree Lua:
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

def build_perm() -> Lua.Expr:
	return new[perm] Lua.Expr.Nil(span: 7)
`)
}
func TestAnalyzeNodeConstructionSugarInjectsAllocAndSpan(t *testing.T) {
	analyzeTreeTestSource(t, "tree_node_construction_sugar.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def build(alloc: mutable Arena&) -> Lua.Expr:
	left: Lua.Expr = node[span = 1] Lua.Expr.Nil
	right: Lua.Expr = node[alloc = alloc, span = 2] Lua.Expr.Nil
	return node[span = 3] Lua.Expr.Binary(left: left, right: right)
`)
}
func TestAnalyzeRejectsBareTreeConstructorsOutsideOwnerScope(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_owner_required.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	return Lua.Expr.Nil(span: 1)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree constructor "Lua.Expr.Nil" requires an active in <owner>: scope or explicit new[owner]`) {
		t.Fatalf("expected bare tree constructor owner diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsMissingCommonTreeConstructorFields(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_common_fields_required.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	return new[perm] Lua.Expr.Nil

def make_binary(left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in perm:
		return Lua.Expr.Binary(left: left, right: right)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree constructor "Lua.Expr.Nil" requires explicit common fields; use call syntax with named arguments`) {
		t.Fatalf("expected explicit common field diagnostic for bare tree constructor, got:\n%s", all)
	}
	if !strings.Contains(all, `tree constructor "Lua.Expr.Binary" is missing common field "span"`) {
		t.Fatalf("expected missing common field diagnostic for tree constructor call, got:\n%s", all)
	}
}
func TestAnalyzeTreeViewSurfaceTypeAndRefinedCalls(t *testing.T) {
	analyzeTreeTestSource(t, "tree_view_surface.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node

def score_binary(view_node: treeview[Lua.Expr.Binary]) -> i64:
	kept: treeview[Lua.Expr.Binary] = keep_binary(view_node)
	return kept.left.span + kept.right.span + kept.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`)
}
func TestAnalyzeTreeVariantBareTypeSugarAndRefinedCalls(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_bare_type_surface.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: Lua.Expr.Binary) -> Lua.Expr.Binary:
	return view_node

def score_binary(view_node: Lua.Expr.Binary) -> i64:
	kept: Lua.Expr.Binary = keep_binary(view_node)
	return kept.left.span + kept.right.span + kept.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`)
}
func TestAnalyzeTreeVariantViewTypeStringCanonicalizesToBareVariant(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_variant_view_string.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node
`)
	sym, ok := result.GlobalScope.Lookup("keep_binary")
	if !ok {
		t.Fatal("expected keep_binary symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected keep_binary function type, got %T", sym.Type)
	}
	if fnType.ExplicitParamCount != 1 || len(fnType.Params) < 1 || fnType.Return == nil {
		t.Fatalf("unexpected keep_binary function type shape: %#v", fnType)
	}
	if fnType.Params[0].String() != "Lua.Expr.Binary" {
		t.Fatalf("expected canonical bare refined variant parameter type, got %q", fnType.Params[0].String())
	}
	if fnType.Return.String() != "Lua.Expr.Binary" {
		t.Fatalf("expected canonical bare refined variant return type, got %q", fnType.Return.String())
	}
}
func TestAnalyzeTreeMatchStatementsAndExpressions(t *testing.T) {
	analyzeTreeTestSource(t, "tree_match_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeRejectsPartialNamedTreeMatchArm(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_match_partial_named_reject.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def child_span(node: Lua.Expr) -> i64:
	match node:
		Lua.Expr.Binary(left: Lua.Expr.Nil):
			return 1
		_:
			return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `match arm "Lua.Expr.Binary" is missing named payload patterns for: right`) {
		t.Fatalf("expected partial named match arm diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTreeIfPatternViewAliasAndNestedPatterns(t *testing.T) {
	analyzeTreeTestSource(t, "tree_if_pattern_view_alias_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeChildrenLoops(t *testing.T) {
	analyzeTreeTestSource(t, "tree_children_surface.elisa", `tree Lua:
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

def count_binary(binary: Lua.Expr.Binary) -> i64:
	total: mutable i64 = 0
	for child in children(binary):
		total <- total + child.span
	return total
`)
}
func TestAnalyzeTreeChildrenMixedToRootLoop(t *testing.T) {
	analyzeTreeTestSource(t, "tree_children_root_surface.elisa", `tree Lua:
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

def visit(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt as Lua.Node):
		total <- total + child.kind.i64()
	return total
`)
}
func TestAnalyzeTreeVisitExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_visit_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeVisitExactMemberExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_visit_exact_member_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeSequenceFieldsSurfaceAsViews(t *testing.T) {
	analyzeTreeTestSource(t, "tree_sequence_field_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeOptionalChildFields(t *testing.T) {
	analyzeTreeTestSource(t, "tree_optional_child_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeFoldExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_fold_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeTreeRewriteExprPreservesHeterogeneousChildTypes(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_heterogeneous_surface.elisa", `tree Lua:
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

def clone_block(block: Lua.Block) -> Lua.Block:
	in perm:
		return rewrite block as Lua.Node:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Function(expr, body: body):
				default
			Lua.Block(block, items: items):
				default
`)
}
func TestAnalyzeTreeExactRecordUpdateExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_exact_update_surface.elisa", `tree Lua:
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
`)
}
