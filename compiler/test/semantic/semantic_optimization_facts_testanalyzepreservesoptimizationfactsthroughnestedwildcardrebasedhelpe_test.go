package semantic_test

import "testing"

func TestAnalyzePreservesOptimizationFactsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[1:3], 0, 2)
	left_indexed: view[i32] = wrapped.meta.items[0].left
	right_indexed: view[i32] = wrapped.meta.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_wildcard_rebased_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected nested wildcard rebased helper-returned indexed wrapper field projections through sliced sources to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper-returned indexed wrapper field projections through sliced sources to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper-returned indexed wrapper field projections through sliced sources to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper-returned indexed wrapper field projections through sliced sources to retain distinct exact bounds")
	}
}
func TestAnalyzeKeepsOverlapGuardrailsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: Wrapper = wrap_submeta_wild(items[0:1], 0, 1)
	left_overlap: view[i32] = wrapped.meta.items[0].left
	right_overlap: view[i32] = wrapped.meta.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_wildcard_rebased_helper_indexed_overlap_guardrails.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_overlap")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_overlap")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected nested wildcard rebased helper overlap projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper overlap projections to remain potentially aliased")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper overlap projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected nested wildcard rebased helper overlap projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughNestedRebasedHelperReturnedIndexedFieldProjectionExpressions(t *testing.T) {
	src := `struct Views:
	left: view[i32]
	right: view[i32]

struct Meta:
	items: view[Views]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Views], start: usize, end: usize) -> Wrapper

def inspect(values: array[i32, 4]) -> int:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: Wrapper = wrap_submeta(items[1:2], 0, 1)
	left_indexed: view[i32] = wrapped.meta.items[0].left
	right_indexed: view[i32] = wrapped.meta.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_rebased_helper_indexed_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_indexed")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_indexed")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected nested rebased helper-returned indexed wrapper field projections through sliced sources to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected nested rebased helper-returned indexed wrapper field projections through sliced sources to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected nested rebased helper-returned indexed wrapper field projections through sliced sources to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected nested rebased helper-returned indexed wrapper field projections through sliced sources to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedNestedRebasedHelperIndexedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(new[store] Expr.Int(value: 2)), Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	wrapped: Wrapper = wrap_submeta(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	move wrapped.meta.items[0].node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_rebased_helper_indexed_field_move_as.elisa", src)
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
		t.Fatalf("expected left nested rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right nested rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through nested rebased helper-indexed frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through nested rebased helper-indexed move-as")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedWildcardRebasedHelperIndexedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct BoxWindow:
	items: view[Box]

@borrows_return_field_rebased(items[*].node, src[*].node)
extern wrap_nodes_wild(src: view[Box], start: usize, end: usize) -> BoxWindow

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(new[store] Expr.Int(value: 2)), Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	wrapped: BoxWindow = wrap_nodes_wild(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	move wrapped.items[0].node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_wildcard_rebased_helper_indexed_field_move_as.elisa", src)
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
		t.Fatalf("expected left wildcard rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right wildcard rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wildcard rebased helper-indexed packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wildcard rebased helper-indexed packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wildcard rebased helper-indexed packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through wildcard rebased helper-indexed frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through wildcard rebased helper-indexed move-as")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedNestedWildcardRebasedHelperIndexedFieldMoveAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(new[store] Expr.Int(value: 2)), Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	move wrapped.meta.items[0].node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_wildcard_rebased_helper_indexed_field_move_as.elisa", src)
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
		t.Fatalf("expected left nested wildcard rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right nested wildcard rebased helper-indexed packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through nested wildcard rebased helper-indexed frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through nested wildcard rebased helper-indexed move-as")
	}
}
func TestAnalyzeCollectsOptimizationFactsForProofCarryingViewHelpers(t *testing.T) {
	src := `def inspect(values: darray[i32, 4]) -> int:
	whole: dview[i32] = values[0:4]
	readonly_whole: dview[i32] = readonly(whole)
	halves: SplitView[i32] = split_at(whole, 2)
	left: dview[i32] = halves.left
	right: dview[i32] = halves.right
	chunks: ChunksExactView[i32] = chunks_exact(readonly_whole, 2)
	first_chunk: dview[i32] = chunks[0]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_proof_carrying_view_helpers.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	wholeExpr := requireOptimizationFactsVarInitExpr(t, fn, "whole")
	readonlyExpr := requireOptimizationFactsVarInitExpr(t, fn, "readonly_whole")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right")
	firstChunkExpr := requireOptimizationFactsVarInitExpr(t, fn, "first_chunk")

	wholeFacts := requireExprOptimizationFacts(t, result, wholeExpr)
	readonlyFacts := requireExprOptimizationFacts(t, result, readonlyExpr)
	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)
	firstChunkFacts := requireExprOptimizationFacts(t, result, firstChunkExpr)

	if !wholeFacts.HasExactExtent() || !wholeFacts.Contiguous || !wholeFacts.UnitStride {
		t.Fatalf("expected full dview slice to expose dense exact facts, got %#v", wholeFacts)
	}
	if !readonlyFacts.ReadOnly || !readonlyFacts.HasExactExtent() {
		t.Fatalf("expected readonly helper to preserve exact extent and mark readonly, got %#v", readonlyFacts)
	}
	if result.ExprSupportsDenseWrite(readonlyExpr) {
		t.Fatalf("expected readonly helper result to reject dense writes")
	}
	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected split_at halves to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split_at halves to be disjoint")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split_at halves to retain distinct exact bounds")
	}
	if !firstChunkFacts.ReadOnly || !firstChunkFacts.HasExactExtent() {
		t.Fatalf("expected chunks_exact item to preserve readonly exact facts, got %#v", firstChunkFacts)
	}
	if result.ExprSupportsDenseWrite(firstChunkExpr) {
		t.Fatalf("expected chunks_exact item to reject dense writes after readonly")
	}
	if !result.ExprsHaveSameExtent(leftExpr, firstChunkExpr) {
		t.Fatalf("expected first chunks_exact item to match split_at left half extent")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedNestedRebasedHelperIndexedFieldMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(new[store] Expr.Int(value: 2)), Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	wrapped: Wrapper = wrap_submeta(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.meta.items[0].node in frozen:
		Expr.HoldViews(left: left, right: right, child: child_alias):
			left_copy: view[i32] = left
			right_copy: view[i32] = right
			child_copy: Expr = child_alias
			_ = wrapped
			_ = frozen
			_ = child_copy
			return 0
		Expr.Int(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_rebased_helper_indexed_field_match.elisa", src)
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
		t.Fatalf("expected left nested rebased helper-indexed packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right nested rebased helper-indexed packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested rebased helper-indexed packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through nested rebased helper-indexed frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through nested rebased helper-indexed match")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedNestedWildcardRebasedHelperIndexedFieldMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(new[store] Expr.Int(value: 2)), Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1:2], 0, 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match wrapped.meta.items[0].node in frozen:
		Expr.HoldViews(left: left, right: right, child: child_alias):
			left_copy: view[i32] = left
			right_copy: view[i32] = right
			child_copy: Expr = child_alias
			_ = wrapped
			_ = frozen
			_ = child_copy
			return 0
		Expr.Int(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_wildcard_rebased_helper_indexed_field_match.elisa", src)
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
		t.Fatalf("expected left nested wildcard rebased helper-indexed packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right nested wildcard rebased helper-indexed packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through nested wildcard rebased helper-indexed packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through nested wildcard rebased helper-indexed frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through nested wildcard rebased helper-indexed match")
	}
}
