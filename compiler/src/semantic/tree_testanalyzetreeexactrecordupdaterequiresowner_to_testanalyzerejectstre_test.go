package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeTreeExactRecordUpdateRequiresOwner(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_exact_update_owner_required.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	return node{left, right}
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree update of "Lua.Expr.Binary" requires an active in <owner>: scope or explicit new[owner]`) {
		t.Fatalf("expected tree update owner diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTreeRewriteDefaultExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_default_surface.elisa", `tree Lua:
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
func TestAnalyzeTreeRewriteImplicitDefaultExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_implicit_default_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeSequenceRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_surface.elisa", `def keep_non_zero(owner: mutable Arena&, items: dview[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[u32]:
				item when item != 0u32:
					emit item
`)
}
func TestAnalyzeTreeTargetSequenceRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_tree_target_surface.elisa", `tree Lua:
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
`)
}
func TestAnalyzeSequenceRewriteEmitAllExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_emit_all_surface.elisa", `def concat(owner: mutable Arena&, left: dview[u32], right: dview[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			segments: darray[dview[u32]] = [left, right]
			return rewrite segments as sequence[u32]:
				segment:
					emit all segment
`)
}
func TestAnalyzeTreeRewriteRemainsExhaustiveWithoutImplicitDefault(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_rewrite_requires_exhaustive.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `non-exhaustive rewrite over Lua.Expr; missing Lua.Expr.Int`) {
		t.Fatalf("expected non-exhaustive rewrite diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTreeRewriteDefaultRequiresExactArm(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_rewrite_default_requires_exact.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Node:
			_:
				default
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `default is only allowed inside an exact tree rewrite arm`) {
		t.Fatalf("expected exact rewrite default diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeTreeVariantPayloadKindShadowsSyntheticKind(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_payload_kind_shadow.elisa", `tree Syntax:
	node Form:
		Atom(kind: bool)

def payload_kind(form: Syntax.Form) -> bool:
	return visit form as Syntax.Form:
		Syntax.Form.Atom(node):
			node.kind
`)
}
func TestAnalyzeRejectsChildrenOnMixedStructuralItemTypes(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_children_mixed.elisa", `tree Lua:
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
	for child in children(stmt):
		total <- total + 1
	return total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `children(...) requires all structural child payloads to have the same item type`) {
		t.Fatalf("expected mixed structural child type diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsChildrenOverrideIncompatibleType(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_children_override_incompatible.elisa", `tree Lua:
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
	for child in children(stmt as Lua.Expr):
		total <- total + child.name_index.i64()
	return total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `children(...) override Lua.Expr is incompatible with structural child Lua.Block`) {
		t.Fatalf("expected incompatible children override diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsTreeIfPatternStoreClauses(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_if_pattern_store_reject.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)

def bad_open(node: Lua.Expr, slot: i64) -> i64:
	if node in slot as Lua.Expr.Int(value: value):
		return value
	return 0

def bad_view(node: Lua.Expr, slot: i64) -> i64:
	if node in slot as Lua.Expr.Int(value: value):
		return value + node.value
	return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree match over "Lua.Expr" does not take an in-store clause`) {
		t.Fatalf("expected tree match in-store rejection, got:\n%s", all)
	}
}
