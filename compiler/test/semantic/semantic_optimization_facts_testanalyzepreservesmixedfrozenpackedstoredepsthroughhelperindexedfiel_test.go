package semantic_test

import "testing"

func TestAnalyzePreservesMixedFrozenPackedStoreDepsThroughHelperIndexedFieldMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: i32&)
	Wrap(child: Expr)


struct Box:
	node: Expr

struct BoxHolder:
	items: array[Box, 1]

@borrows_return(field, items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	local_ref: i32& @scratch = new[scratch] 7
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.items[0].node in frozen:
		Expr.Wrap(child: child_alias):
			child_copy: Expr = child_alias
			_ = wrapped
			_ = frozen
			_ = child_copy
			return child_copy.span
		Expr.Int(value: _):
			return 0
		Expr.Hold(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_helper_indexed_field_match_mixed_child.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	childExpr := requireOptimizationFactsVarInitExpr(t, fn, "child_copy")

	childFacts := requireExprOptimizationFacts(t, result, childExpr)
	childProvenance := requireExprPackedStoreProvenance(t, result, childExpr)

	if childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to retain mixed non-store provenance, got %#v", childFacts)
	}
	if !childProvenance.HasPackedStoreDeps || !childProvenance.HasFrozenPackedStoreDeps || childProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to keep only frozen packed-store deps, got %#v", childProvenance)
	}
	if !childProvenance.HasMixedProvenance() {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to keep mixed non-store provenance, got %#v", childProvenance)
	}
	if !childProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to keep frozen-only packed-store deps despite mixed provenance, got %#v", childProvenance)
	}
	if childProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to reject pure frozen-only provenance, got %#v", childProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(childExpr) || !result.ExprDependsOnFrozenPackedStores(childExpr) || result.ExprDependsOnNonFrozenPackedStores(childExpr) {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(childExpr) {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(childExpr) {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected helper-indexed packed child payload recovered through frozen match to reject strict frozen-only provenance query")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughAllocatedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

def inspect(buf: array[i32, 4]) -> int:
	region scratch(1024)
	boxed: Views& @scratch = new[scratch] Views{left: buf[0:2], right: buf[2:4]}
	left_alloc: view[i32] = boxed.left
	right_alloc: view[i32] = boxed.right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_allocated_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_alloc")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_alloc")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected allocated wrapper field projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected allocated wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected allocated wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected allocated wrapper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedAllocatedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	boxed: Box& @scratch = new[scratch] Box{node: new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child)}
	frozen: Expr.Store[Frozen] = freeze(move store)
	move boxed.node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_allocated_field_move_as.elisa", src)
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
		t.Fatalf("expected left allocated-wrapper packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right allocated-wrapper packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through allocated-wrapper packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through allocated-wrapper packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through allocated-wrapper packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through allocated-wrapper frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through allocated-wrapper move-as")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

def inspect(buf: array[i32, 4]) -> int:
	items: array[Views, 1] = [Views{left: buf[0:2], right: buf[2:4]}]
	left_indexed: view[i32] = items[0].left
	right_indexed: view[i32] = items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected indexed wrapper field projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected indexed wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected indexed wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected indexed wrapper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughDirectIndexedExpressions(t *testing.T) {
	src := `def inspect(buf: array[i32, 4]) -> int:
	items: array[view[i32], 2] = [buf[0:2], buf[2:4]]
	left_indexed: view[i32] = items[0]
	right_indexed: view[i32] = items[1]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_indexed_values.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected direct indexed values to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected direct indexed values to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected direct indexed values to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected direct indexed values to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesFrozenPackedStoreProvenanceThroughDirectIndexedExpressions(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Hold(value: i32&)

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	local_ref: i32& @scratch = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	items: array[Expr, 2] = [node, held]
	frozen: Expr.Store[Frozen] = freeze(move store)
	pure_indexed: Expr = items[0]
	mixed_indexed: Expr = items[1]
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_indexed_frozen_packed_provenance.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	pureExpr := requireOptimizationFactsVarInitExpr(t, fn, "pure_indexed")
	mixedExpr := requireOptimizationFactsVarInitExpr(t, fn, "mixed_indexed")

	pureFacts := requireExprOptimizationFacts(t, result, pureExpr)
	mixedFacts := requireExprOptimizationFacts(t, result, mixedExpr)
	pureProvenance := requireExprPackedStoreProvenance(t, result, pureExpr)
	mixedProvenance := requireExprPackedStoreProvenance(t, result, mixedExpr)

	if !pureFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected direct indexed frozen packed value to stay frozen-store-only, got %#v", pureFacts)
	}
	if !pureProvenance.DependsOnlyOnFrozenPackedStores() || pureProvenance.HasMixedProvenance() {
		t.Fatalf("expected direct indexed frozen packed value to expose pure frozen packed-store provenance, got %#v", pureProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(pureExpr) || !result.ExprDependsOnFrozenPackedStores(pureExpr) || result.ExprDependsOnNonFrozenPackedStores(pureExpr) || result.ExprHasMixedPackedStoreProvenance(pureExpr) {
		t.Fatalf("expected direct indexed frozen packed value to expose frozen-only packed-store helper results")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(pureExpr) {
		t.Fatalf("expected direct indexed frozen packed value to report frozen-store-only provenance")
	}

	if mixedFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected direct indexed mixed packed value to retain mixed provenance, got %#v", mixedFacts)
	}
	if !mixedProvenance.HasPackedStoreDeps || !mixedProvenance.HasFrozenPackedStoreDeps || mixedProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected direct indexed mixed packed value to keep only frozen packed-store deps, got %#v", mixedProvenance)
	}
	if !mixedProvenance.HasMixedProvenance() {
		t.Fatalf("expected direct indexed mixed packed value to report mixed non-store provenance, got %#v", mixedProvenance)
	}
	if !mixedProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected direct indexed mixed packed value to keep frozen-only packed-store deps despite mixed provenance, got %#v", mixedProvenance)
	}
	if mixedProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected direct indexed mixed packed value to reject strict frozen-only provenance, got %#v", mixedProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(mixedExpr) || !result.ExprDependsOnFrozenPackedStores(mixedExpr) || result.ExprDependsOnNonFrozenPackedStores(mixedExpr) {
		t.Fatalf("expected direct indexed mixed packed value to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(mixedExpr) {
		t.Fatalf("expected direct indexed mixed packed value to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(mixedExpr) {
		t.Fatalf("expected direct indexed mixed packed value to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(mixedExpr) {
		t.Fatalf("expected direct indexed mixed packed value to reject strict frozen-only provenance query")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

@borrows_return(field, items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def inspect(buf: array[i32, 4]) -> int:
	wrapped: ViewHolder = wrap_indexed_views(buf[0:2], buf[2:4])
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected helper-returned indexed wrapper field projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected helper-returned indexed wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected helper-returned indexed wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected helper-returned indexed wrapper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughNestedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewHolder:
	items: array[Views, 1]

struct NestedHolder:
	holder: ViewHolder

@borrows_return(field, holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def inspect(buf: array[i32, 4]) -> int:
	wrapped: NestedHolder = wrap_nested_indexed_views(buf[0:2], buf[2:4])
	left_indexed: view[i32] = wrapped.holder.items[0].left
	right_indexed: view[i32] = wrapped.holder.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected nested helper-returned indexed wrapper field projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected nested helper-returned indexed wrapper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected nested helper-returned indexed wrapper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected nested helper-returned indexed wrapper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return(field, rebased, items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 4]) -> int:
	items: array[Views, 2] = [Views{left: values[0:1], right: values[1:2]}, Views{left: values[2:3], right: values[3:4]}]
	wrapped: ViewWindow = wrap_sub(items[1:2], 0, 1)
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_rebased_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected rebased helper-returned indexed wrapper field projections through sliced sources to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected rebased helper-returned indexed wrapper field projections through sliced sources to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected rebased helper-returned indexed wrapper field projections through sliced sources to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected rebased helper-returned indexed wrapper field projections through sliced sources to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughWildcardRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return(field, rebased, items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 4] = [Views{left: values[0:1], right: values[1:2]}, Views{left: values[2:3], right: values[3:4]}, Views{left: values[4:5], right: values[5:6]}, Views{left: values[6:7], right: values[7:8]}]
	wrapped: ViewWindow = wrap_sub_wild(items[1:3], 0, 2)
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected wildcard rebased helper-returned indexed wrapper field projections through sliced sources to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper-returned indexed wrapper field projections through sliced sources to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper-returned indexed wrapper field projections through sliced sources to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper-returned indexed wrapper field projections through sliced sources to retain distinct exact bounds")
	}
}
func TestAnalyzeKeepsOverlapGuardrailsThroughWildcardRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct ViewWindow:
	items: view[Views]

@borrows_return(field, rebased, items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 2] = [Views{left: values[0:3], right: values[1:4]}, Views{left: values[4:7], right: values[5:8]}]
	wrapped: ViewWindow = wrap_sub_wild(items[0:1], 0, 1)
	left_overlap: view[i32] = wrapped.items[0].left
	right_overlap: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_overlap")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_overlap")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected wildcard rebased helper overlap projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper overlap projections to remain potentially aliased")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper overlap projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected wildcard rebased helper overlap projections to retain distinct exact bounds")
	}
}
