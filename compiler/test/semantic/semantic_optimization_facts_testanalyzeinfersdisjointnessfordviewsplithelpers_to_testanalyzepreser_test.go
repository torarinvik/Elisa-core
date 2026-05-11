package semantic_test

import "testing"

func TestAnalyzeInfersDisjointnessForDViewSplitHelpers(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_view_prefix[T](view: dview[T], end: usize) -> dview[T]:
	_ = end
	return view

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	_ = start
	return view

def inspect(values: darray[i32, row]&) -> int:
	base: dview[i32] = arena_da_view(values, 0, values.count)
	prefix: dview[i32] = arena_da_view_prefix(base, 2)
	suffix: dview[i32] = arena_da_view_suffix(base, 2)
	full_prefix: dview[i32] = arena_da_view_prefix(base, base.len)
	full_suffix: dview[i32] = arena_da_view_suffix(base, 0)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_dview_split_helpers.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	baseExpr := requireOptimizationFactsVarInitExpr(t, fn, "base")
	prefixExpr := requireOptimizationFactsVarInitExpr(t, fn, "prefix")
	suffixExpr := requireOptimizationFactsVarInitExpr(t, fn, "suffix")
	fullPrefixExpr := requireOptimizationFactsVarInitExpr(t, fn, "full_prefix")
	fullSuffixExpr := requireOptimizationFactsVarInitExpr(t, fn, "full_suffix")

	if !result.ExprsAreDisjoint(prefixExpr, suffixExpr) {
		t.Fatalf("expected dview prefix/suffix helpers to produce disjoint views")
	}
	if !result.ExprsHaveSameExtent(baseExpr, fullPrefixExpr) {
		t.Fatalf("expected full-span arena_da_view_prefix to preserve exact extent")
	}
	if !result.ExprsHaveSameExtent(baseExpr, fullSuffixExpr) {
		t.Fatalf("expected zero-offset arena_da_view_suffix to preserve exact extent")
	}
}
func TestAnalyzeInfersEqualExtentSizeForSplitDViews(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	return view

def inspect(values: darray[i32, 4]&) -> int:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = arena_da_view_slice(base, 0, 2)
	right: dview[i32] = arena_da_view_slice(base, 2, 4)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_equal_extent_size.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right")

	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected left/right split dviews to be disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected left/right split dviews to have equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected left/right split dviews to retain distinct exact bounds")
	}
}
func TestAnalyzeInfersFactsForDirectDViewSliceSyntax(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def inspect(values: darray[i32, 4]&) -> int:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	right: dview[i32] = base[2:4]
	full: dview[i32] = base[0:base.len]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_dview_slice_syntax.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	baseExpr := requireOptimizationFactsVarInitExpr(t, fn, "base")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right")
	fullExpr := requireOptimizationFactsVarInitExpr(t, fn, "full")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	if !leftFacts.HasExactExtent() {
		t.Fatalf("expected direct dview slice syntax to preserve exact extent, got %#v", leftFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected adjacent direct dview slices to be disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected adjacent direct dview slices to have equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected adjacent direct dview slices to retain distinct exact bounds")
	}
	if !result.ExprsHaveSameExtent(baseExpr, fullExpr) {
		t.Fatalf("expected full-span direct dview slice syntax to preserve input extent")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughStandardViewSliceHelperFieldProjectionExpressions(t *testing.T) {
	src := `struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_view_slice(input: view[Views], start: usize, end: usize) -> view[Views]:
	_ = start
	_ = end
	return input

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 2] = [Views(values[0:2], values[2:4]), Views(values[4:6], values[6:8])]
	window: view[Views] = arena_da_view_slice(items[1:2], 0, 1)
	left_projected: view[i32] = window[0].left
	right_projected: view[i32] = window[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_standard_view_slice_helper_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_projected")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_projected")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)

	if !leftFacts.HasExactExtent() || !rightFacts.HasExactExtent() {
		t.Fatalf("expected standard view-slice helper field projections to preserve exact extents, got %#v and %#v", leftFacts, rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected standard view-slice helper field projections to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected standard view-slice helper field projections to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected standard view-slice helper field projections to retain distinct exact bounds")
	}
}
func TestAnalyzePreservesFrozenPackedStoreProvenanceThroughStandardViewSliceHelperFieldProjectionExpressions(t *testing.T) {
	src := `struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

packed enum Expr:
	Int(value: int)

struct Box:
	node: Expr

def arena_da_view_slice(input: view[Box], start: usize, end: usize) -> view[Box]:
	_ = start
	_ = end
	return input

def inspect(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	node0: Expr = new[store] Expr.Int(value: 0)
	node1: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 2] = [Box(node0), Box(node1)]
	frozen: Expr.Store[Frozen] = freeze(move store)
	window: view[Box] = arena_da_view_slice(items[1:2], 0, 1)
	projected: Expr = window[0].node
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_standard_view_slice_helper_frozen_field_projection.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	projectedExpr := requireOptimizationFactsVarInitExpr(t, fn, "projected")
	projectedFacts := requireExprOptimizationFacts(t, result, projectedExpr)
	projectedProvenance := requireExprPackedStoreProvenance(t, result, projectedExpr)

	if !projectedFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected standard view-slice helper packed field projection to stay frozen-store-only, got %#v", projectedFacts)
	}
	if !projectedProvenance.DependsOnlyOnFrozenPackedStores() || projectedProvenance.HasMixedProvenance() {
		t.Fatalf("expected standard view-slice helper packed field projection to expose pure frozen packed-store provenance, got %#v", projectedProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(projectedExpr) || !result.ExprDependsOnFrozenPackedStores(projectedExpr) || result.ExprDependsOnNonFrozenPackedStores(projectedExpr) || result.ExprHasMixedPackedStoreProvenance(projectedExpr) {
		t.Fatalf("expected standard view-slice helper packed field projection to expose frozen-only packed-store helper results")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(projectedExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for standard view-slice helper packed field projection")
	}
}
func TestAnalyzeMarksValuesDependingOnlyOnFrozenPackedStores(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Hold(value: i32&)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	box: Box = wrap_node(node)
	items: array[Box, 1] = [box]
	before_freeze: Expr = node
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	held_before_freeze: Expr = held
	frozen: Expr.Store[Frozen] = freeze(move store)
	after_freeze: Expr = node
	wrapped_after_freeze: Box = wrap_node(node)
	view_after_freeze: view[Box, 0, 1] = items[0:1]
	held_after_freeze: Expr = held
	_ = box
	_ = held_before_freeze
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_store_only.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	beforeFreezeExpr := requireOptimizationFactsVarInitExpr(t, fn, "before_freeze")
	afterFreezeExpr := requireOptimizationFactsVarInitExpr(t, fn, "after_freeze")
	wrappedAfterFreezeExpr := requireOptimizationFactsVarInitExpr(t, fn, "wrapped_after_freeze")
	viewAfterFreezeExpr := requireOptimizationFactsVarInitExpr(t, fn, "view_after_freeze")
	heldAfterFreezeExpr := requireOptimizationFactsVarInitExpr(t, fn, "held_after_freeze")
	localRefExpr := requireOptimizationFactsVarInitExpr(t, fn, "local_ref")

	beforeFreezeFacts := requireExprOptimizationFacts(t, result, beforeFreezeExpr)
	afterFreezeFacts := requireExprOptimizationFacts(t, result, afterFreezeExpr)
	wrappedAfterFreezeFacts := requireExprOptimizationFacts(t, result, wrappedAfterFreezeExpr)
	viewAfterFreezeFacts := requireExprOptimizationFacts(t, result, viewAfterFreezeExpr)
	heldAfterFreezeFacts := requireExprOptimizationFacts(t, result, heldAfterFreezeExpr)
	beforeFreezeProvenance := requireExprPackedStoreProvenance(t, result, beforeFreezeExpr)
	afterFreezeProvenance := requireExprPackedStoreProvenance(t, result, afterFreezeExpr)
	wrappedAfterFreezeProvenance := requireExprPackedStoreProvenance(t, result, wrappedAfterFreezeExpr)
	viewAfterFreezeProvenance := requireExprPackedStoreProvenance(t, result, viewAfterFreezeExpr)
	heldAfterFreezeProvenance := requireExprPackedStoreProvenance(t, result, heldAfterFreezeExpr)

	if beforeFreezeFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected value depending on a local packed store to stay unpublished before freeze, got %#v", beforeFreezeFacts)
	}
	if !beforeFreezeProvenance.HasPackedStoreDeps || !beforeFreezeProvenance.HasNonFrozenPackedStoreDeps || beforeFreezeProvenance.HasFrozenPackedStoreDeps {
		t.Fatalf("expected pre-freeze value to report non-frozen packed-store provenance, got %#v", beforeFreezeProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(beforeFreezeExpr) || result.ExprDependsOnFrozenPackedStores(beforeFreezeExpr) || !result.ExprDependsOnNonFrozenPackedStores(beforeFreezeExpr) || result.ExprHasMixedPackedStoreProvenance(beforeFreezeExpr) {
		t.Fatalf("expected pre-freeze value to expose non-frozen-only packed-store helper results")
	}
	if result.ExprHasOnlyFrozenPackedStoreDeps(beforeFreezeExpr) {
		t.Fatalf("expected pre-freeze value to reject frozen-store-only dependency classification")
	}
	if beforeFreezeProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected pre-freeze value to reject frozen-only classification, got %#v", beforeFreezeProvenance)
	}
	if beforeFreezeProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected pre-freeze value to reject frozen-store-only dependency classification, got %#v", beforeFreezeProvenance)
	}
	if !afterFreezeFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected freeze to remap packed-store provenance for direct values, got %#v", afterFreezeFacts)
	}
	if !afterFreezeProvenance.HasPackedStoreDeps || !afterFreezeProvenance.HasFrozenPackedStoreDeps || afterFreezeProvenance.HasNonFrozenPackedStoreDeps || afterFreezeProvenance.HasMixedProvenance() {
		t.Fatalf("expected post-freeze direct value to report pure frozen packed-store provenance, got %#v", afterFreezeProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(afterFreezeExpr) || !result.ExprDependsOnFrozenPackedStores(afterFreezeExpr) || result.ExprDependsOnNonFrozenPackedStores(afterFreezeExpr) || result.ExprHasMixedPackedStoreProvenance(afterFreezeExpr) {
		t.Fatalf("expected post-freeze value to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(afterFreezeExpr) {
		t.Fatalf("expected post-freeze value to report frozen-store-only packed-store deps")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(afterFreezeExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for direct values")
	}
	if !afterFreezeProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected post-freeze direct value to report frozen-store-only packed-store deps, got %#v", afterFreezeProvenance)
	}
	if !wrappedAfterFreezeFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected helper-return aggregate to inherit frozen-store-only provenance, got %#v", wrappedAfterFreezeFacts)
	}
	if !wrappedAfterFreezeProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected helper-return aggregate to expose frozen-only packed-store provenance, got %#v", wrappedAfterFreezeProvenance)
	}
	if !result.ExprDependsOnFrozenPackedStores(wrappedAfterFreezeExpr) || result.ExprDependsOnNonFrozenPackedStores(wrappedAfterFreezeExpr) || result.ExprHasMixedPackedStoreProvenance(wrappedAfterFreezeExpr) {
		t.Fatalf("expected helper-return aggregate to expose frozen-only packed-store helper results")
	}
	if !viewAfterFreezeFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected sliced aggregate view to retain frozen-store-only provenance, got %#v", viewAfterFreezeFacts)
	}
	if !viewAfterFreezeProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected sliced aggregate view to expose frozen-only packed-store provenance, got %#v", viewAfterFreezeProvenance)
	}
	if !result.ExprDependsOnFrozenPackedStores(viewAfterFreezeExpr) || result.ExprDependsOnNonFrozenPackedStores(viewAfterFreezeExpr) || result.ExprHasMixedPackedStoreProvenance(viewAfterFreezeExpr) {
		t.Fatalf("expected sliced aggregate view to expose frozen-only packed-store helper results")
	}
	if heldAfterFreezeFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected value with local-region provenance to avoid frozen-store-only classification, got %#v", heldAfterFreezeFacts)
	}
	if !heldAfterFreezeProvenance.HasPackedStoreDeps || !heldAfterFreezeProvenance.HasFrozenPackedStoreDeps || !heldAfterFreezeProvenance.HasMixedProvenance() {
		t.Fatalf("expected held value to report mixed frozen-store and non-store provenance, got %#v", heldAfterFreezeProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(heldAfterFreezeExpr) || !result.ExprDependsOnFrozenPackedStores(heldAfterFreezeExpr) || result.ExprDependsOnNonFrozenPackedStores(heldAfterFreezeExpr) || !result.ExprHasMixedPackedStoreProvenance(heldAfterFreezeExpr) {
		t.Fatalf("expected held value to expose mixed packed-store helper results")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(heldAfterFreezeExpr) {
		t.Fatalf("expected held value to report frozen-only packed-store deps even with mixed non-store provenance")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(heldAfterFreezeExpr) {
		t.Fatalf("expected result query to reject mixed local-region and frozen-store provenance")
	}
	if !heldAfterFreezeProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected held value to report frozen-only packed-store deps even with mixed non-store provenance, got %#v", heldAfterFreezeProvenance)
	}
	if result.ExprHasPackedStoreProvenance(localRefExpr) {
		t.Fatalf("expected local region reference without packed-store deps to be excluded from packed-store provenance query")
	}
	if _, ok := result.ExprPackedStoreProvenance(localRefExpr); ok {
		t.Fatalf("expected local region reference without packed-store deps to return no packed-store provenance summary")
	}
}
func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedTagViews(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def inspect(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags_view: dview[Expr.Tag] = frozen.tags
	full_span: dview[Expr.Tag] = frozen.tags[0:frozen.count]
	prefix: dview[Expr.Tag] = frozen.tags[0:1]
	suffix: dview[Expr.Tag] = frozen.tags[1:frozen.count]
	_ = tags_view
	_ = full_span
	_ = prefix
	_ = suffix
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_tag_view.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	tagsExpr := requireOptimizationFactsVarInitExpr(t, fn, "tags_view")
	fullExpr := requireOptimizationFactsVarInitExpr(t, fn, "full_span")
	prefixExpr := requireOptimizationFactsVarInitExpr(t, fn, "prefix")
	suffixExpr := requireOptimizationFactsVarInitExpr(t, fn, "suffix")

	tagsFacts := requireExprOptimizationFacts(t, result, tagsExpr)
	tagsProvenance := requireExprPackedStoreProvenance(t, result, tagsExpr)
	if !tagsFacts.ReadOnly || !tagsFacts.Contiguous || !tagsFacts.UnitStride || !tagsFacts.HasExactExtent() {
		t.Fatalf("expected frozen packed tag view to expose readonly dense exact facts, got %#v", tagsFacts)
	}
	if !tagsFacts.FrozenPackedStoreOnly || !tagsProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected frozen packed tag view to expose frozen-only packed-store provenance, got facts=%#v provenance=%#v", tagsFacts, tagsProvenance)
	}
	if !result.ExprsHaveSameExtent(tagsExpr, fullExpr) {
		t.Fatalf("expected frozen packed tag full-span slice to preserve exact extent")
	}
	if !result.ExprsAreDisjoint(prefixExpr, suffixExpr) {
		t.Fatalf("expected disjoint frozen packed tag slices")
	}
}
func TestAnalyzePreservesFrozenPackedStoreProvenanceThroughTryExpressions(t *testing.T) {
	src := `error ProbeError:
	Fail

packed enum Expr:
	Int(value: int)
	Hold(value: i32&)

def maybe_take(node: Expr, fail: bool) -> Expr error[ProbeError]:
	if fail:
		raise ProbeError.Fail
	return node

def inspect(owner: Arena) -> int error[ProbeError]:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	held_after_freeze: Expr = held
	propagated: Expr = try maybe_take(node, false)
	recovered: Expr = try maybe_take(node, true) else node
	recovered_mixed: Expr = try maybe_take(node, true) else held_after_freeze
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_try_frozen_packed_provenance.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	propagatedExpr := requireOptimizationFactsVarInitExpr(t, fn, "propagated")
	recoveredExpr := requireOptimizationFactsVarInitExpr(t, fn, "recovered")
	recoveredMixedExpr := requireOptimizationFactsVarInitExpr(t, fn, "recovered_mixed")

	propagatedFacts := requireExprOptimizationFacts(t, result, propagatedExpr)
	recoveredFacts := requireExprOptimizationFacts(t, result, recoveredExpr)
	recoveredMixedFacts := requireExprOptimizationFacts(t, result, recoveredMixedExpr)
	propagatedProvenance := requireExprPackedStoreProvenance(t, result, propagatedExpr)
	recoveredProvenance := requireExprPackedStoreProvenance(t, result, recoveredExpr)
	recoveredMixedProvenance := requireExprPackedStoreProvenance(t, result, recoveredMixedExpr)

	if !propagatedFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected try propagation to preserve frozen packed-store-only classification, got %#v", propagatedFacts)
	}
	if !propagatedProvenance.DependsOnlyOnFrozenPackedStores() || propagatedProvenance.HasMixedProvenance() {
		t.Fatalf("expected try propagation to preserve pure frozen packed-store provenance, got %#v", propagatedProvenance)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(propagatedExpr) {
		t.Fatalf("expected try propagation result query to report frozen-store-only provenance")
	}

	if !recoveredFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected try recovery fallback from the same frozen value to stay frozen-store-only, got %#v", recoveredFacts)
	}
	if !recoveredProvenance.DependsOnlyOnFrozenPackedStores() || recoveredProvenance.HasMixedProvenance() {
		t.Fatalf("expected try recovery fallback from the same frozen value to expose pure frozen packed-store provenance, got %#v", recoveredProvenance)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(recoveredExpr) {
		t.Fatalf("expected try recovery fallback from the same frozen value to report frozen-store-only provenance")
	}

	if recoveredMixedFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected try recovery fallback from a mixed value to retain mixed provenance, got %#v", recoveredMixedFacts)
	}
	if !recoveredMixedProvenance.HasPackedStoreDeps || !recoveredMixedProvenance.HasFrozenPackedStoreDeps || recoveredMixedProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected mixed try recovery to keep only frozen packed-store deps, got %#v", recoveredMixedProvenance)
	}
	if !recoveredMixedProvenance.HasMixedProvenance() {
		t.Fatalf("expected mixed try recovery to report mixed non-store provenance, got %#v", recoveredMixedProvenance)
	}
	if !recoveredMixedProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected mixed try recovery to keep frozen-only packed-store deps despite mixed provenance, got %#v", recoveredMixedProvenance)
	}
	if recoveredMixedProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected mixed try recovery to reject strict frozen-only provenance, got %#v", recoveredMixedProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(recoveredMixedExpr) || !result.ExprDependsOnFrozenPackedStores(recoveredMixedExpr) || result.ExprDependsOnNonFrozenPackedStores(recoveredMixedExpr) {
		t.Fatalf("expected mixed try recovery to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(recoveredMixedExpr) {
		t.Fatalf("expected mixed try recovery to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(recoveredMixedExpr) {
		t.Fatalf("expected mixed try recovery to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(recoveredMixedExpr) {
		t.Fatalf("expected mixed try recovery to reject strict frozen-only provenance query")
	}
}
