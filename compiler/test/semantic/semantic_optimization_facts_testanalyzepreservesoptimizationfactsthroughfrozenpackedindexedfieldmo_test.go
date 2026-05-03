package semantic_test

import "testing"

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedIndexedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 1] = [Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	frozen: Expr.Store[Frozen] = freeze(move store)
	move items[0].node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_indexed_field_move_as.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_copy")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_copy")
	childExpr := requireOptimizationFactsVarInitExpr(t, fn, "child_copy")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)
	childFacts := requireExprOptimizationFacts(t, result, childExpr)

	if !leftFacts.HasExactExtent() {
		t.Fatalf("expected left indexed-wrapper packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right indexed-wrapper packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through indexed-wrapper packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through indexed-wrapper packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through indexed-wrapper packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through indexed-wrapper frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through indexed-wrapper move-as")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedHelperIndexedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	node: Expr = new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	move wrapped.items[0].node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_helper_indexed_field_move_as.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_copy")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_copy")
	childExpr := requireOptimizationFactsVarInitExpr(t, fn, "child_copy")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)
	childFacts := requireExprOptimizationFacts(t, result, childExpr)

	if !leftFacts.HasExactExtent() {
		t.Fatalf("expected left helper-indexed packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right helper-indexed packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through helper-indexed frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through helper-indexed move-as")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughDirectFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return_field(left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def inspect(buf: array[i32, 4]) -> int:
	boxed: Views = Views(buf[0:2], buf[2:4])
	wrapped: Views = wrap_views(buf[0:2], buf[2:4])
	left_direct: view[i32] = boxed.left
	right_direct: view[i32] = boxed.right
	left_wrapped: view[i32] = wrapped.left
	right_wrapped: view[i32] = wrapped.right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_field_projection.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftDirectExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_direct")
	rightDirectExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_direct")
	leftWrappedExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_wrapped")
	rightWrappedExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_wrapped")

	leftDirectFacts := requireExprOptimizationFacts(t, result, leftDirectExpr)
	rightDirectFacts := requireExprOptimizationFacts(t, result, rightDirectExpr)
	leftWrappedFacts := requireExprOptimizationFacts(t, result, leftWrappedExpr)
	rightWrappedFacts := requireExprOptimizationFacts(t, result, rightWrappedExpr)

	if !leftDirectFacts.HasExactExtent() || !rightDirectFacts.HasExactExtent() {
		t.Fatalf("expected direct wrapper field projections to preserve exact extents, got %#v and %#v", leftDirectFacts, rightDirectFacts)
	}
	if !leftWrappedFacts.HasExactExtent() || !rightWrappedFacts.HasExactExtent() {
		t.Fatalf("expected helper-returned wrapper field projections to preserve exact extents, got %#v and %#v", leftWrappedFacts, rightWrappedFacts)
	}
	if !result.ExprsAreDisjoint(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected direct wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected direct wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected direct wrapper field projections to retain distinct exact bounds")
	}
	if !result.ExprsAreDisjoint(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected helper-returned wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected helper-returned wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected helper-returned wrapper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughNestedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct NestedViews:
	inner: Views

@borrows_return_field(inner.left, left, inner.right, right)
extern wrap_nested_views(left: view[i32], right: view[i32]) -> NestedViews

def inspect(buf: array[i32, 4]) -> int:
	direct: NestedViews = NestedViews(Views(buf[0:2], buf[2:4]))
	wrapped: NestedViews = wrap_nested_views(buf[0:2], buf[2:4])
	left_direct: view[i32] = direct.inner.left
	right_direct: view[i32] = direct.inner.right
	left_wrapped: view[i32] = wrapped.inner.left
	right_wrapped: view[i32] = wrapped.inner.right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_field_projection.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftDirectExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_direct")
	rightDirectExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_direct")
	leftWrappedExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_wrapped")
	rightWrappedExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_wrapped")

	leftDirectFacts := requireExprOptimizationFacts(t, result, leftDirectExpr)
	rightDirectFacts := requireExprOptimizationFacts(t, result, rightDirectExpr)
	leftWrappedFacts := requireExprOptimizationFacts(t, result, leftWrappedExpr)
	rightWrappedFacts := requireExprOptimizationFacts(t, result, rightWrappedExpr)

	if !leftDirectFacts.HasExactExtent() || !rightDirectFacts.HasExactExtent() {
		t.Fatalf("expected nested direct wrapper field projections to preserve exact extents, got %#v and %#v", leftDirectFacts, rightDirectFacts)
	}
	if !leftWrappedFacts.HasExactExtent() || !rightWrappedFacts.HasExactExtent() {
		t.Fatalf("expected nested helper-returned wrapper field projections to preserve exact extents, got %#v and %#v", leftWrappedFacts, rightWrappedFacts)
	}
	if !result.ExprsAreDisjoint(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected nested direct wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected nested direct wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftDirectExpr, rightDirectExpr) {
		t.Fatalf("expected nested direct wrapper field projections to retain distinct exact bounds")
	}
	if !result.ExprsAreDisjoint(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected nested helper-returned wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected nested helper-returned wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftWrappedExpr, rightWrappedExpr) {
		t.Fatalf("expected nested helper-returned wrapper field projections to retain distinct exact bounds")
	}
}
