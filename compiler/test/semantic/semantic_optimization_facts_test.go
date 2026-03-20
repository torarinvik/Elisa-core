package semantic_test

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/semantic"
)

func requireOptimizationFactsFunctionDecl(t *testing.T, result *semantic.Result, name string) *ast.FuncDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected %s decl to be a function, got %T", name, sym.Node)
	}
	return fn
}

func requireOptimizationFactsVarInitExpr(t *testing.T, fn *ast.FuncDecl, name string) ast.Expr {
	t.Helper()
	for _, stmt := range fn.Body {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		if decl.Name == name {
			return decl.Value
		}
	}
	t.Fatalf("expected var decl %q in function %q", name, fn.Name)
	return nil
}

func requireExprOptimizationFacts(t *testing.T, result *semantic.Result, expr ast.Expr) semantic.OptimizationFacts {
	t.Helper()
	facts, ok := result.ExprOptimizationFacts(expr)
	if !ok {
		t.Fatalf("expected optimization facts for %T", expr)
	}
	return facts
}

func TestAnalyzeCollectsOptimizationFactsForShapeBackedCollections(t *testing.T) {
	src := `def inspect(values: darray[i32, row], other: darray[i32, row], text: dstr[row], any_values: darray[i32], buf: array[i32, 4]) -> int:
	same_a: darray[i32, row] = values
	same_b: darray[i32, row] = other
	text_copy: dstr[row] = text
	wildcard_copy: darray[i32] = any_values
	slice: view[i32, 0u, 2u] = buf[0u:2u]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_shape_backed.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	sameAFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "same_a"))
	sameBFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "same_b"))
	textFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "text_copy"))
	wildcardFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "wildcard_copy"))
	sliceFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "slice"))

	if !sameAFacts.Contiguous || !sameAFacts.UnitStride {
		t.Fatalf("expected same_a facts to mark contiguous unit-stride access, got %#v", sameAFacts)
	}
	if sameAFacts.ReadOnly {
		t.Fatalf("expected same_a facts to stay writable, got %#v", sameAFacts)
	}
	if !sameAFacts.HasExactExtent() {
		t.Fatalf("expected same_a facts to preserve exact shape extent, got %#v", sameAFacts)
	}
	if !sameAFacts.SameExtent(sameBFacts) {
		t.Fatalf("expected same-shape darray values to share exact extent, got %#v vs %#v", sameAFacts, sameBFacts)
	}

	if !textFacts.ReadOnly || !textFacts.Contiguous || !textFacts.UnitStride {
		t.Fatalf("expected dstr facts to be readonly contiguous unit-stride, got %#v", textFacts)
	}
	if !textFacts.HasExactExtent() {
		t.Fatalf("expected dstr facts to preserve exact shape extent, got %#v", textFacts)
	}
	if !sameAFacts.SameExtent(textFacts) {
		t.Fatalf("expected shared shape identity between darray and dstr facts, got %#v vs %#v", sameAFacts, textFacts)
	}

	if wildcardFacts.HasExactExtent() {
		t.Fatalf("expected wildcard-shape darray facts to keep extent unknown, got %#v", wildcardFacts)
	}
	if !wildcardFacts.Contiguous || !wildcardFacts.UnitStride {
		t.Fatalf("expected wildcard-shape darray facts to retain contiguity/stride, got %#v", wildcardFacts)
	}

	if !sliceFacts.Contiguous || !sliceFacts.UnitStride {
		t.Fatalf("expected fixed-array slice facts to mark contiguous unit-stride access, got %#v", sliceFacts)
	}
	if !sliceFacts.HasExactExtent() {
		t.Fatalf("expected bounded slice facts to preserve exact view bounds, got %#v", sliceFacts)
	}
	if sliceFacts.ReadOnly {
		t.Fatalf("expected numeric array slice facts to stay writable by default, got %#v", sliceFacts)
	}
	if sliceFacts.SameExtent(sameAFacts) {
		t.Fatalf("expected bounded slice extent to differ from shape-backed row extent, got %#v vs %#v", sliceFacts, sameAFacts)
	}
}

func TestAnalyzeMarksFreshRegionAllocationsAsExclusive(t *testing.T) {
	src := `def inspect(seed: i32) -> i32:
	region scratch(1024u)
	slot: any i32& = new[scratch] seed
	alias: any i32& = slot
	return alias[0u]
`
	result, errs := parseAndAnalyze(t, "optimization_facts_region_alloc_exclusive.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	allocFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "slot"))
	aliasFacts := requireExprOptimizationFacts(t, result, requireOptimizationFactsVarInitExpr(t, fn, "alias"))

	if !allocFacts.Exclusive {
		t.Fatalf("expected fresh region allocation to be marked exclusive, got %#v", allocFacts)
	}
	if aliasFacts.Exclusive {
		t.Fatalf("expected plain identifier rebinding to avoid claiming exclusivity, got %#v", aliasFacts)
	}
	if allocFacts.Contiguous || allocFacts.UnitStride || allocFacts.ReadOnly || allocFacts.HasExactExtent() {
		t.Fatalf("expected scalar region allocation to only contribute exclusivity, got %#v", allocFacts)
	}
}

func TestAnalyzePreservesOptimizationFactsThroughRuntimeViewHelpers(t *testing.T) {
	src := `repr(c) struct Arena:
	begin: mutable any void&?
	end: mutable any void&?

repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	return view

def arena_da_from_view[T](a: any Arena&, view: dview[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	_ = end
	return StringView("", 0)

def string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	_ = start
	_ = end
	return view

def string_view_copy(view: StringView) -> any u8&:
	return view.data

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	return string_view_slice(view, start, end)

def ctx_string_from_view(view: StringView) -> dstr[shape_out]:
	return string_view_copy(view)

def inspect(a: any Arena&, values: any darray[i32, row]&, other: any darray[i32, row]&, text: dstr[row]) -> int:
	whole_a: dview[i32] = arena_da_view(values, 0u, values.count)
	whole_b: dview[i32] = arena_da_view(other, 0u, other.count)
	sub_a: dview[i32] = arena_da_view_slice(whole_a, 1u, 3u)
	sub_b: dview[i32] = arena_da_view_slice(whole_b, 1u, 3u)
	copied: darray[i32] = arena_da_from_view(a, sub_a)
	text_view: StringView = ctx_string_view(text, 0, 2)
	text_sub: StringView = ctx_string_view_slice(text_view, 0, text_view.len)
	text_copy: dstr = ctx_string_from_view(text_sub)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_runtime_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	wholeAExpr := requireOptimizationFactsVarInitExpr(t, fn, "whole_a")
	wholeBExpr := requireOptimizationFactsVarInitExpr(t, fn, "whole_b")
	subAExpr := requireOptimizationFactsVarInitExpr(t, fn, "sub_a")
	subBExpr := requireOptimizationFactsVarInitExpr(t, fn, "sub_b")
	copiedExpr := requireOptimizationFactsVarInitExpr(t, fn, "copied")
	textViewExpr := requireOptimizationFactsVarInitExpr(t, fn, "text_view")
	textSubExpr := requireOptimizationFactsVarInitExpr(t, fn, "text_sub")
	textCopyExpr := requireOptimizationFactsVarInitExpr(t, fn, "text_copy")

	wholeAFacts := requireExprOptimizationFacts(t, result, wholeAExpr)
	subAFacts := requireExprOptimizationFacts(t, result, subAExpr)
	textViewFacts := requireExprOptimizationFacts(t, result, textViewExpr)
	textCopyFacts := requireExprOptimizationFacts(t, result, textCopyExpr)

	if !wholeAFacts.HasExactExtent() {
		t.Fatalf("expected full-span arena_da_view to preserve exact source extent, got %#v", wholeAFacts)
	}
	if !result.ExprsHaveSameExtent(wholeAExpr, wholeBExpr) {
		t.Fatalf("expected full-span arena_da_view results over same-shape arrays to share exact extent")
	}

	if !subAFacts.HasExactExtent() {
		t.Fatalf("expected arena_da_view_slice to preserve bounded extent, got %#v", subAFacts)
	}
	if !result.ExprsHaveSameExtent(subAExpr, subBExpr) {
		t.Fatalf("expected matching bounded arena_da_view_slice results to share exact extent")
	}
	if result.ExprsHaveSameExtent(wholeAExpr, subAExpr) {
		t.Fatalf("expected bounded subview extent to differ from full-span source extent")
	}
	if !result.ExprsHaveSameExtent(subAExpr, copiedExpr) {
		t.Fatalf("expected arena_da_from_view to preserve exact extent from its input view")
	}

	if !textViewFacts.HasExactExtent() {
		t.Fatalf("expected ctx_string_view to preserve bounded extent, got %#v", textViewFacts)
	}
	if !result.ExprsHaveSameExtent(textViewExpr, textSubExpr) {
		t.Fatalf("expected full-span ctx_string_view_slice to preserve input extent")
	}
	if !textCopyFacts.ReadOnly || !textCopyFacts.HasExactExtent() {
		t.Fatalf("expected ctx_string_from_view to preserve readonly exact extent facts, got %#v", textCopyFacts)
	}
	if !result.ExprsHaveSameExtent(textSubExpr, textCopyExpr) {
		t.Fatalf("expected ctx_string_from_view to preserve exact extent from its input view")
	}
}

func TestAnalyzeInfersDisjointnessForNonOverlappingViewsAndFreshAllocations(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	_ = end
	return StringView("", 0)

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return string_view(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return string_view(view.data, start, view.len)

def inspect(text: dstr[row], buf: array[i32, 8]) -> int:
	left: view[i32, 0u, 2u] = buf[0u:2u]
	right: view[i32, 2u, 4u] = buf[2u:4u]
	overlap: view[i32, 1u, 3u] = buf[1u:3u]
	base: StringView = ctx_string_view(text, 0, 4)
	first: StringView = ctx_string_view(text, 0, 2)
	second: StringView = ctx_string_view(text, 2, 4)
	middle: StringView = ctx_string_view(text, 1, 3)
	prefix: StringView = ctx_string_view_prefix(base, 2)
	suffix: StringView = ctx_string_view_suffix(base, 2)
	full_prefix: StringView = ctx_string_view_prefix(base, base.len)
	full_suffix: StringView = ctx_string_view_suffix(base, 0)
	region scratch(1024u)
	fresh_view_a: StringView = string_view(new[scratch] 3u8, 0, 1)
	fresh_view_b: StringView = string_view(new[scratch] 4u8, 0, 1)
	alloc_a: scratch i32& = new[scratch] 1i32
	alloc_b: scratch i32& = new[scratch] 2i32
	alloc_alias: scratch i32& = alloc_a
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_disjoint_views.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right")
	overlapExpr := requireOptimizationFactsVarInitExpr(t, fn, "overlap")
	baseExpr := requireOptimizationFactsVarInitExpr(t, fn, "base")
	firstExpr := requireOptimizationFactsVarInitExpr(t, fn, "first")
	secondExpr := requireOptimizationFactsVarInitExpr(t, fn, "second")
	middleExpr := requireOptimizationFactsVarInitExpr(t, fn, "middle")
	prefixExpr := requireOptimizationFactsVarInitExpr(t, fn, "prefix")
	suffixExpr := requireOptimizationFactsVarInitExpr(t, fn, "suffix")
	fullPrefixExpr := requireOptimizationFactsVarInitExpr(t, fn, "full_prefix")
	fullSuffixExpr := requireOptimizationFactsVarInitExpr(t, fn, "full_suffix")
	freshViewAExpr := requireOptimizationFactsVarInitExpr(t, fn, "fresh_view_a")
	freshViewBExpr := requireOptimizationFactsVarInitExpr(t, fn, "fresh_view_b")
	allocAExpr := requireOptimizationFactsVarInitExpr(t, fn, "alloc_a")
	allocBExpr := requireOptimizationFactsVarInitExpr(t, fn, "alloc_b")
	allocAliasExpr := requireOptimizationFactsVarInitExpr(t, fn, "alloc_alias")

	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected adjacent fixed-array slices to be disjoint")
	}
	if result.ExprsAreDisjoint(leftExpr, overlapExpr) {
		t.Fatalf("expected overlapping fixed-array slices to remain potentially aliased")
	}
	if !result.ExprsAreDisjoint(firstExpr, secondExpr) {
		t.Fatalf("expected adjacent string views over the same base to be disjoint")
	}
	if result.ExprsAreDisjoint(firstExpr, middleExpr) {
		t.Fatalf("expected overlapping string views to remain potentially aliased")
	}
	if !result.ExprsAreDisjoint(prefixExpr, suffixExpr) {
		t.Fatalf("expected split prefix/suffix string views to be disjoint")
	}
	if !result.ExprsHaveSameExtent(baseExpr, fullPrefixExpr) {
		t.Fatalf("expected full-span string_view_prefix to preserve exact extent")
	}
	if !result.ExprsHaveSameExtent(baseExpr, fullSuffixExpr) {
		t.Fatalf("expected zero-offset string_view_suffix to preserve exact extent")
	}
	if !result.ExprsAreDisjoint(freshViewAExpr, freshViewBExpr) {
		t.Fatalf("expected fresh-allocation string views to be disjoint")
	}
	if !result.ExprsAreDisjoint(allocAExpr, allocBExpr) {
		t.Fatalf("expected distinct fresh allocations to be disjoint")
	}
	if result.ExprsAreDisjoint(allocAExpr, allocAliasExpr) {
		t.Fatalf("expected an alias of a fresh allocation to remain non-disjoint from the source")
	}
}

func TestAnalyzeInfersDisjointnessForDViewSplitHelpers(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_view_prefix[T](view: dview[T], end: usize) -> dview[T]:
	_ = end
	return view

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	_ = start
	return view

def inspect(values: any darray[i32, row]&) -> int:
	base: dview[i32] = arena_da_view(values, 0u, values.count)
	prefix: dview[i32] = arena_da_view_prefix(base, 2u)
	suffix: dview[i32] = arena_da_view_suffix(base, 2u)
	full_prefix: dview[i32] = arena_da_view_prefix(base, base.len)
	full_suffix: dview[i32] = arena_da_view_suffix(base, 0u)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_dview_split_helpers.llcontext", src)
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
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	return view

def inspect(values: any darray[i32, 4]&) -> int:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = arena_da_view_slice(base, 0u, 2u)
	right: dview[i32] = arena_da_view_slice(base, 2u, 4u)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_equal_extent_size.llcontext", src)
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
