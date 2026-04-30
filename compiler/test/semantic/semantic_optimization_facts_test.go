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
	expr, ok := findOptimizationFactsVarInitExpr(fn.Body, name)
	if ok {
		return expr
	}
	t.Fatalf("expected var decl %q in function %q", name, fn.Name)
	return nil
}

func findOptimizationFactsVarInitExpr(stmts []ast.Stmt, name string) (ast.Expr, bool) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDeclStmt:
			if s.Name == name {
				return s.Value, true
			}
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				if expr, ok := findOptimizationFactsVarInitExpr(arm.Body, name); ok {
					return expr, true
				}
			}
		}
	}
	return nil, false
}

func requireExprOptimizationFacts(t *testing.T, result *semantic.Result, expr ast.Expr) semantic.OptimizationFacts {
	t.Helper()
	facts, ok := result.ExprOptimizationFacts(expr)
	if !ok {
		t.Fatalf("expected optimization facts for %T", expr)
	}
	return facts
}

func requireExprPackedStoreProvenance(t *testing.T, result *semantic.Result, expr ast.Expr) semantic.PackedStoreProvenance {
	t.Helper()
	provenance, ok := result.ExprPackedStoreProvenance(expr)
	if !ok {
		t.Fatalf("expected packed-store provenance for %T", expr)
	}
	return provenance
}

func TestAnalyzeCollectsOptimizationFactsForShapeBackedCollections(t *testing.T) {
	src := `def inspect(values: darray[i32, row], other: darray[i32, row], text: cstr[row], any_values: darray[i32], buf: array[i32, 4]) -> int:
	same_a: darray[i32, row] = values
	same_b: darray[i32, row] = other
	text_copy: cstr[row] = text
	wildcard_copy: darray[i32] = any_values
	slice: view[i32, 0, 2] = buf[0:2]
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
	if !result.ExprSupportsDenseWrite(requireOptimizationFactsVarInitExpr(t, fn, "same_a")) {
		t.Fatalf("expected same_a to support dense write helpers")
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
		t.Fatalf("expected cstr facts to be readonly contiguous unit-stride, got %#v", textFacts)
	}
	if result.ExprSupportsDenseWrite(requireOptimizationFactsVarInitExpr(t, fn, "text_copy")) {
		t.Fatalf("expected readonly cstr value to reject dense write helpers")
	}
	if !textFacts.HasExactExtent() {
		t.Fatalf("expected cstr facts to preserve exact shape extent, got %#v", textFacts)
	}
	if !sameAFacts.SameExtent(textFacts) {
		t.Fatalf("expected shared shape identity between darray and cstr facts, got %#v vs %#v", sameAFacts, textFacts)
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
	if !result.ExprSupportsDenseWrite(requireOptimizationFactsVarInitExpr(t, fn, "slice")) {
		t.Fatalf("expected numeric fixed-array slice to support dense write helpers")
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
	region scratch(1024)
	slot: i32& = new[scratch] seed
	alias: i32& = slot
	return alias[0]
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
	src := `struct Arena:
	begin: mutable void&?
	end: mutable void&?

struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

struct StringView:
	data: mutable u8&
	len: mutable i64

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	return view

def arena_da_from_view[T](a: Arena&, view: dview[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def string_view(value: u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	_ = end
	return StringView("", 0)

def string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	_ = start
	_ = end
	return view

def string_view_copy(view: StringView) -> u8&:
	return view.data

def ctx_string_view(value: cstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	return string_view_slice(view, start, end)

def ctx_string_from_view(view: StringView) -> cstr[shape_out]:
	return string_view_copy(view)

def ctx_string_slice(value: cstr[shape_in], start: i64, end: i64) -> cstr[shape_out]:
	_ = start
	_ = end
	return value

def inspect(a: Arena&, values: darray[i32, row]&, other: darray[i32, row]&, text: cstr[row]) -> int:
	whole_a: dview[i32] = arena_da_view(values, 0, values.count)
	whole_b: dview[i32] = arena_da_view(other, 0, other.count)
	sub_a: dview[i32] = arena_da_view_slice(whole_a, 1, 3)
	sub_b: dview[i32] = arena_da_view_slice(whole_b, 1, 3)
	copied: darray[i32] = arena_da_from_view(a, sub_a)
	text_view: StringView = ctx_string_view(text, 1, 3)
	text_sub: StringView = ctx_string_view_slice(text_view, 0, text_view.len)
	text_copy: cstr = ctx_string_from_view(text_sub)
	text_slice: cstr = ctx_string_slice(text, 1, 3)
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
	textSliceExpr := requireOptimizationFactsVarInitExpr(t, fn, "text_slice")

	wholeAFacts := requireExprOptimizationFacts(t, result, wholeAExpr)
	subAFacts := requireExprOptimizationFacts(t, result, subAExpr)
	textViewFacts := requireExprOptimizationFacts(t, result, textViewExpr)
	textCopyFacts := requireExprOptimizationFacts(t, result, textCopyExpr)
	textSliceFacts := requireExprOptimizationFacts(t, result, textSliceExpr)

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
	if !textSliceFacts.ReadOnly || !textSliceFacts.HasExactExtent() {
		t.Fatalf("expected ctx_string_slice to preserve readonly exact extent facts, got %#v", textSliceFacts)
	}
	if !result.ExprsHaveSameExtent(textViewExpr, textSliceExpr) {
		t.Fatalf("expected ctx_string_slice to preserve exact extent matching an equivalent ctx_string_view")
	}
}

func TestAnalyzeInfersDenseWritableFactsForNodeTableValues(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: i32
	Lit(value: i32)
	Add(left: Expr, right: Expr)

def inspect(owner: Arena) -> i32:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[2]
	key: NodeKey[Expr] = dense_key(node, frozen)
	table: NodeTable[Expr, i32] = node_table_fill.specialize[Expr, i32]()(owner, frozen, -1)
	table[key] <- 0
	values: dview[i32] = table.values
	nodes: dview[Expr] = frozen[0:frozen.count]
	if values.len == nodes.len:
		return values[0]
	return -1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_node_table_values.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	valuesExpr := requireOptimizationFactsVarInitExpr(t, fn, "values")
	nodesExpr := requireOptimizationFactsVarInitExpr(t, fn, "nodes")
	valuesFacts := requireExprOptimizationFacts(t, result, valuesExpr)
	nodesFacts := requireExprOptimizationFacts(t, result, nodesExpr)
	valuesProvenance := requireExprPackedStoreProvenance(t, result, valuesExpr)

	if valuesFacts.ReadOnly {
		t.Fatalf("expected node-table values view to stay writable, got %#v", valuesFacts)
	}
	if !valuesFacts.Contiguous || !valuesFacts.UnitStride {
		t.Fatalf("expected node-table values view to be contiguous unit-stride, got %#v", valuesFacts)
	}
	if !valuesFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected node-table values view to be marked frozen-packed-store-only, got %#v", valuesFacts)
	}
	if !valuesFacts.HasExactExtent() {
		t.Fatalf("expected node-table values view to preserve exact frozen-store extent, got %#v", valuesFacts)
	}
	if !result.ExprSupportsDenseWrite(valuesExpr) {
		t.Fatalf("expected node-table values view to support dense writes")
	}
	if !valuesProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected node-table values view provenance to depend only on frozen packed stores, got %#v", valuesProvenance)
	}
	if !nodesFacts.HasExactExtent() {
		t.Fatalf("expected full frozen-store slice to preserve an exact extent, got %#v", nodesFacts)
	}
	if !result.ExprsHaveSameExtent(valuesExpr, nodesExpr) {
		t.Fatalf("expected node-table values view extent to match the frozen-store full-span slice, got values=%s nodes=%s", valuesFacts.Extent, nodesFacts.Extent)
	}
}

func TestAnalyzeInfersDenseWritableFactsForNodeTableValuesFromHiddenFrozenStoreFieldRoot(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: i32
	Lit(value: i32)
	Add(left: Expr, right: Expr)

struct FrozenBox:
	store: Expr.Store[Frozen]

def make_box(owner: Arena) -> FrozenBox:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return FrozenBox(frozen)

def inspect(owner: Arena) -> i32:
	box: FrozenBox = make_box(owner)
	node: Expr = box.store[2]
	key: NodeKey[Expr] = dense_key(node, box.store)
	table: NodeTable[Expr, i32] = node_table_fill.specialize[Expr, i32]()(owner, box.store, -1)
	table[key] <- 0
	values: dview[i32] = table.values
	nodes: dview[Expr] = box.store[0:box.store.count]
	if values.len == nodes.len:
		return values[0]
	return -1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_node_table_values_hidden_frozen_field_root.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	valuesExpr := requireOptimizationFactsVarInitExpr(t, fn, "values")
	nodesExpr := requireOptimizationFactsVarInitExpr(t, fn, "nodes")
	valuesFacts := requireExprOptimizationFacts(t, result, valuesExpr)
	nodesFacts := requireExprOptimizationFacts(t, result, nodesExpr)
	valuesProvenance := requireExprPackedStoreProvenance(t, result, valuesExpr)

	if valuesFacts.ReadOnly {
		t.Fatalf("expected hidden-root node-table values view to stay writable, got %#v", valuesFacts)
	}
	if !valuesFacts.Contiguous || !valuesFacts.UnitStride {
		t.Fatalf("expected hidden-root node-table values view to be contiguous unit-stride, got %#v", valuesFacts)
	}
	if !valuesFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected hidden-root node-table values view to be marked frozen-packed-store-only, got %#v", valuesFacts)
	}
	if !valuesFacts.HasExactExtent() {
		t.Fatalf("expected hidden-root node-table values view to preserve exact frozen-store extent, got %#v", valuesFacts)
	}
	if !result.ExprSupportsDenseWrite(valuesExpr) {
		t.Fatalf("expected hidden-root node-table values view to support dense writes")
	}
	if !valuesProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected hidden-root node-table values view provenance to depend only on frozen packed stores, got %#v", valuesProvenance)
	}
	if !nodesFacts.HasExactExtent() {
		t.Fatalf("expected hidden-root full frozen-store slice to preserve an exact extent, got %#v", nodesFacts)
	}
	if !result.ExprsHaveSameExtent(valuesExpr, nodesExpr) {
		t.Fatalf("expected hidden-root node-table values view extent to match the boxed frozen-store full-span slice, got values=%s nodes=%s", valuesFacts.Extent, nodesFacts.Extent)
	}
}

func TestAnalyzeInfersDisjointnessForNonOverlappingViewsAndFreshAllocations(t *testing.T) {
	src := `struct StringView:
	data: mutable u8&
	len: mutable i64

def string_view(value: u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	_ = end
	return StringView("", 0)

def ctx_string_view(value: cstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return string_view(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return string_view(view.data, start, view.len)

def inspect(text: cstr[row], buf: array[i32, 8]) -> int:
	left: view[i32, 0, 2] = buf[0:2]
	right: view[i32, 2, 4] = buf[2:4]
	overlap: view[i32, 1, 3] = buf[1:3]
	base: StringView = ctx_string_view(text, 0, 4)
	first: StringView = ctx_string_view(text, 0, 2)
	second: StringView = ctx_string_view(text, 2, 4)
	middle: StringView = ctx_string_view(text, 1, 3)
	prefix: StringView = ctx_string_view_prefix(base, 2)
	suffix: StringView = ctx_string_view_suffix(base, 2)
	full_prefix: StringView = ctx_string_view_prefix(base, base.len)
	full_suffix: StringView = ctx_string_view_suffix(base, 0)
	region scratch(1024)
	fresh_view_a: StringView = string_view(new[scratch] 3u8, 0, 1)
	fresh_view_b: StringView = string_view(new[scratch] 4u8, 0, 1)
	alloc_a: scratch i32& = new[scratch] 1
	alloc_b: scratch i32& = new[scratch] 2
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
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

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
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

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
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def inspect(values: darray[i32, 4]&) -> int:
	base: dview[i32] = arena_da_view(values, 0, 4)
	left: dview[i32] = base[0:2]
	right: dview[i32] = base[2:4]
	full: dview[i32] = base[0:base.len]
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_dview_slice_syntax.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_standard_view_slice_helper_field_projection.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_standard_view_slice_helper_frozen_field_projection.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_store_only.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_tag_view.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_try_frozen_packed_provenance.llcontext", src)
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

func TestAnalyzePreservesFrozenPackedStoreProvenanceThroughUnwrapElseExpressions(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Hold(value: i32&)

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	other: Expr = new[store] Expr.Int(value: 2)
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	node_after_freeze: Expr = node
	other_after_freeze: Expr = other
	held_after_freeze: Expr = held
	node_ref: mutable stack Expr& = &node_after_freeze
	other_ref: mutable stack Expr& = &other_after_freeze
	held_ref: mutable stack Expr& = &held_after_freeze
	node_ptr: mutable stack Expr&? = node_ref
	pure_recovered: mutable stack Expr& = node_ptr else other_ref
	mixed_recovered: mutable stack Expr& = node_ptr else held_ref
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_unwrap_else_frozen_packed_provenance.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	pureRecoveredExpr := requireOptimizationFactsVarInitExpr(t, fn, "pure_recovered")
	mixedRecoveredExpr := requireOptimizationFactsVarInitExpr(t, fn, "mixed_recovered")

	pureRecoveredFacts := requireExprOptimizationFacts(t, result, pureRecoveredExpr)
	mixedRecoveredFacts := requireExprOptimizationFacts(t, result, mixedRecoveredExpr)
	pureRecoveredProvenance := requireExprPackedStoreProvenance(t, result, pureRecoveredExpr)
	mixedRecoveredProvenance := requireExprPackedStoreProvenance(t, result, mixedRecoveredExpr)

	if !pureRecoveredFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected else recovery over frozen-only packed refs to stay frozen-store-only, got %#v", pureRecoveredFacts)
	}
	if !pureRecoveredProvenance.DependsOnlyOnFrozenPackedStores() || pureRecoveredProvenance.HasMixedProvenance() {
		t.Fatalf("expected else recovery over frozen-only packed refs to expose pure frozen packed-store provenance, got %#v", pureRecoveredProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(pureRecoveredExpr) || !result.ExprDependsOnFrozenPackedStores(pureRecoveredExpr) || result.ExprDependsOnNonFrozenPackedStores(pureRecoveredExpr) || result.ExprHasMixedPackedStoreProvenance(pureRecoveredExpr) {
		t.Fatalf("expected else recovery over frozen-only packed refs to expose frozen-only packed-store helper results")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(pureRecoveredExpr) {
		t.Fatalf("expected else recovery over frozen-only packed refs to report frozen-store-only provenance")
	}

	if mixedRecoveredFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected else recovery with mixed packed fallback to retain mixed provenance, got %#v", mixedRecoveredFacts)
	}
	if !mixedRecoveredProvenance.HasPackedStoreDeps || !mixedRecoveredProvenance.HasFrozenPackedStoreDeps || mixedRecoveredProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected else recovery with mixed packed fallback to keep only frozen packed-store deps, got %#v", mixedRecoveredProvenance)
	}
	if !mixedRecoveredProvenance.HasMixedProvenance() {
		t.Fatalf("expected else recovery with mixed packed fallback to report mixed non-store provenance, got %#v", mixedRecoveredProvenance)
	}
	if !mixedRecoveredProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected else recovery with mixed packed fallback to keep frozen-only packed-store deps despite mixed provenance, got %#v", mixedRecoveredProvenance)
	}
	if mixedRecoveredProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected else recovery with mixed packed fallback to reject strict frozen-only provenance, got %#v", mixedRecoveredProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(mixedRecoveredExpr) || !result.ExprDependsOnFrozenPackedStores(mixedRecoveredExpr) || result.ExprDependsOnNonFrozenPackedStores(mixedRecoveredExpr) {
		t.Fatalf("expected else recovery with mixed packed fallback to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(mixedRecoveredExpr) {
		t.Fatalf("expected else recovery with mixed packed fallback to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(mixedRecoveredExpr) {
		t.Fatalf("expected else recovery with mixed packed fallback to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(mixedRecoveredExpr) {
		t.Fatalf("expected else recovery with mixed packed fallback to reject strict frozen-only provenance query")
	}
}

func TestAnalyzePreservesFrozenPackedStoreProvenanceThroughTernaryExpressions(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Hold(value: i32&)

def inspect(owner: Arena, pick_left: bool) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	left_node: Expr = new[store] Expr.Int(value: 1)
	right_node: Expr = new[store] Expr.Int(value: 2)
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	left_after_freeze: Expr = left_node
	right_after_freeze: Expr = right_node
	held_after_freeze: Expr = held
	pure_choice: Expr = left_after_freeze if pick_left else right_after_freeze
	mixed_choice: Expr = left_after_freeze if pick_left else held_after_freeze
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_ternary_frozen_packed_provenance.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	pureChoiceExpr := requireOptimizationFactsVarInitExpr(t, fn, "pure_choice")
	mixedChoiceExpr := requireOptimizationFactsVarInitExpr(t, fn, "mixed_choice")

	pureChoiceFacts := requireExprOptimizationFacts(t, result, pureChoiceExpr)
	mixedChoiceFacts := requireExprOptimizationFacts(t, result, mixedChoiceExpr)
	pureChoiceProvenance := requireExprPackedStoreProvenance(t, result, pureChoiceExpr)
	mixedChoiceProvenance := requireExprPackedStoreProvenance(t, result, mixedChoiceExpr)

	if !pureChoiceFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected ternary over frozen-only packed values to stay frozen-store-only, got %#v", pureChoiceFacts)
	}
	if !pureChoiceProvenance.DependsOnlyOnFrozenPackedStores() || pureChoiceProvenance.HasMixedProvenance() {
		t.Fatalf("expected ternary over frozen-only packed values to expose pure frozen packed-store provenance, got %#v", pureChoiceProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(pureChoiceExpr) || !result.ExprDependsOnFrozenPackedStores(pureChoiceExpr) || result.ExprDependsOnNonFrozenPackedStores(pureChoiceExpr) || result.ExprHasMixedPackedStoreProvenance(pureChoiceExpr) {
		t.Fatalf("expected ternary over frozen-only packed values to expose frozen-only packed-store helper results")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(pureChoiceExpr) {
		t.Fatalf("expected ternary over frozen-only packed values to report frozen-store-only provenance")
	}

	if mixedChoiceFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected ternary with mixed fallback to retain mixed provenance, got %#v", mixedChoiceFacts)
	}
	if !mixedChoiceProvenance.HasPackedStoreDeps || !mixedChoiceProvenance.HasFrozenPackedStoreDeps || mixedChoiceProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected ternary with mixed fallback to keep only frozen packed-store deps, got %#v", mixedChoiceProvenance)
	}
	if !mixedChoiceProvenance.HasMixedProvenance() {
		t.Fatalf("expected ternary with mixed fallback to report mixed non-store provenance, got %#v", mixedChoiceProvenance)
	}
	if !mixedChoiceProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected ternary with mixed fallback to keep frozen-only packed-store deps despite mixed provenance, got %#v", mixedChoiceProvenance)
	}
	if mixedChoiceProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected ternary with mixed fallback to reject strict frozen-only provenance, got %#v", mixedChoiceProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(mixedChoiceExpr) || !result.ExprDependsOnFrozenPackedStores(mixedChoiceExpr) || result.ExprDependsOnNonFrozenPackedStores(mixedChoiceExpr) {
		t.Fatalf("expected ternary with mixed fallback to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(mixedChoiceExpr) {
		t.Fatalf("expected ternary with mixed fallback to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(mixedChoiceExpr) {
		t.Fatalf("expected ternary with mixed fallback to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(mixedChoiceExpr) {
		t.Fatalf("expected ternary with mixed fallback to reject strict frozen-only provenance query")
	}
}

func TestAnalyzePreservesShapeExtentFactsThroughTernaryExpressions(t *testing.T) {
	src := `def inspect(left: darray[i32, row], right: darray[i32, row], pick_left: bool) -> int:
	left_copy: darray[i32, row] = left
	right_copy: darray[i32, row] = right
	choice: darray[i32, row] = left if pick_left else right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_ternary_shape_extent.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_copy")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_copy")
	choiceExpr := requireOptimizationFactsVarInitExpr(t, fn, "choice")

	choiceFacts := requireExprOptimizationFacts(t, result, choiceExpr)

	if !choiceFacts.Contiguous || !choiceFacts.UnitStride {
		t.Fatalf("expected ternary over row-shaped darray values to retain contiguous unit-stride facts, got %#v", choiceFacts)
	}
	if !choiceFacts.HasExactExtent() {
		t.Fatalf("expected ternary over row-shaped darray values to retain exact shape extent, got %#v", choiceFacts)
	}
	if !result.ExprSupportsDenseWrite(choiceExpr) {
		t.Fatalf("expected ternary over writable row-shaped darray values to support dense write helpers")
	}
	if !result.ExprsHaveSameExtent(choiceExpr, leftExpr) || !result.ExprsHaveSameExtent(choiceExpr, rightExpr) {
		t.Fatalf("expected ternary over same-shape darray values to preserve exact extent identity")
	}
}

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedMoveAsDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	node: Expr = new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child)
	frozen: Expr.Store[Frozen] = freeze(move store)
	move node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_move_as.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	leftExpr := requireOptimizationFactsVarInitExpr(t, fn, "left_copy")
	rightExpr := requireOptimizationFactsVarInitExpr(t, fn, "right_copy")
	childExpr := requireOptimizationFactsVarInitExpr(t, fn, "child_copy")

	leftFacts := requireExprOptimizationFacts(t, result, leftExpr)
	rightFacts := requireExprOptimizationFacts(t, result, rightExpr)
	childFacts := requireExprOptimizationFacts(t, result, childExpr)
	childProvenance := requireExprPackedStoreProvenance(t, result, childExpr)

	if !leftFacts.HasExactExtent() {
		t.Fatalf("expected left packed move-as payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right packed move-as payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed move-as to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed move-as to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed move-as to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through frozen move-as to stay frozen-store-only, got %#v", childFacts)
	}
	if !childProvenance.DependsOnlyOnFrozenPackedStores() || childProvenance.HasMixedProvenance() {
		t.Fatalf("expected packed child payload recovered through frozen move-as to expose pure frozen packed-store provenance, got %#v", childProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(childExpr) || !result.ExprDependsOnFrozenPackedStores(childExpr) || result.ExprDependsOnNonFrozenPackedStores(childExpr) || result.ExprHasMixedPackedStoreProvenance(childExpr) {
		t.Fatalf("expected packed child payload recovered through frozen move-as to expose frozen-only packed-store helper results")
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through move-as")
	}
}

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	node: Expr = new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.HoldViews(left: left, right: right, child: child_alias):
			left_copy: view[i32] = left
			right_copy: view[i32] = right
			child_copy: Expr = child_alias
			_ = frozen
			_ = child_copy
			return 0
		Expr.Int(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_match.llcontext", src)
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
		t.Fatalf("expected left packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through match")
	}
}

func TestAnalyzePreservesMixedFrozenPackedStoreDepsThroughPackedMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: i32&)
	Wrap(child: Expr)

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.Wrap(child: child_alias):
			child_copy: Expr = child_alias
			_ = frozen
			_ = child_copy
			return child_copy.span
		Expr.Int(value: _):
			return 0
		Expr.Hold(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_match_mixed_child.llcontext", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	childExpr := requireOptimizationFactsVarInitExpr(t, fn, "child_copy")

	childFacts := requireExprOptimizationFacts(t, result, childExpr)
	childProvenance := requireExprPackedStoreProvenance(t, result, childExpr)

	if childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through frozen match to retain mixed non-store provenance, got %#v", childFacts)
	}
	if !childProvenance.HasPackedStoreDeps || !childProvenance.HasFrozenPackedStoreDeps || childProvenance.HasNonFrozenPackedStoreDeps {
		t.Fatalf("expected packed child payload recovered through frozen match to keep only frozen packed-store deps, got %#v", childProvenance)
	}
	if !childProvenance.HasMixedProvenance() {
		t.Fatalf("expected packed child payload recovered through frozen match to keep mixed non-store provenance, got %#v", childProvenance)
	}
	if !childProvenance.HasOnlyFrozenPackedStoreDeps() {
		t.Fatalf("expected packed child payload recovered through frozen match to keep frozen-only packed-store deps despite mixed provenance, got %#v", childProvenance)
	}
	if childProvenance.DependsOnlyOnFrozenPackedStores() {
		t.Fatalf("expected packed child payload recovered through frozen match to reject pure frozen-only provenance, got %#v", childProvenance)
	}
	if !result.ExprHasPackedStoreProvenance(childExpr) || !result.ExprDependsOnFrozenPackedStores(childExpr) || result.ExprDependsOnNonFrozenPackedStores(childExpr) {
		t.Fatalf("expected packed child payload recovered through frozen match to expose frozen-only packed-store helper results")
	}
	if !result.ExprHasMixedPackedStoreProvenance(childExpr) {
		t.Fatalf("expected packed child payload recovered through frozen match to report mixed packed-store provenance")
	}
	if !result.ExprHasOnlyFrozenPackedStoreDeps(childExpr) {
		t.Fatalf("expected packed child payload recovered through frozen match to report frozen-only packed-store deps")
	}
	if result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected packed child payload recovered through frozen match to reject strict frozen-only provenance query")
	}
}

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedFieldMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	boxed: Box = Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))
	frozen: Expr.Store[Frozen] = freeze(move store)
	match boxed.node in frozen:
		Expr.HoldViews(left: left, right: right, child: child_alias):
			left_copy: view[i32] = left
			right_copy: view[i32] = right
			child_copy: Expr = child_alias
			_ = frozen
			_ = child_copy
			return 0
		Expr.Int(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_field_match.llcontext", src)
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
		t.Fatalf("expected left wrapped packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right wrapped packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wrapped packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wrapped packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through wrapped packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through wrapped frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through wrapped match")
	}
}

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedHelperFieldMatchBinders(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	HoldViews(left: view[i32], right: view[i32], child: Expr)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def inspect(owner: Arena, buf: array[i32, 4]) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	child: Expr = new[store] Expr.Int(value: 1)
	node: Expr = new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child)
	boxed: Box = wrap_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match boxed.node in frozen:
		Expr.HoldViews(left: left, right: right, child: child_alias):
			left_copy: view[i32] = left
			right_copy: view[i32] = right
			child_copy: Expr = child_alias
			_ = boxed
			_ = frozen
			_ = child_copy
			return 0
		Expr.Int(value: _):
			return 1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_helper_field_match.llcontext", src)
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
		t.Fatalf("expected left helper-wrapped packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right helper-wrapped packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-wrapped packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-wrapped packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-wrapped packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through helper-wrapped frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through helper-wrapped match")
	}
}

func TestAnalyzePreservesOptimizationFactsThroughFrozenPackedHelperIndexedFieldMatchBinders(t *testing.T) {
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
	match wrapped.items[0].node in frozen:
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_helper_indexed_field_match.llcontext", src)
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
		t.Fatalf("expected left helper-indexed packed match payload to preserve exact extent facts, got %#v", leftFacts)
	}
	if !rightFacts.HasExactExtent() {
		t.Fatalf("expected right helper-indexed packed match payload to preserve exact extent facts, got %#v", rightFacts)
	}
	if !result.ExprsAreDisjoint(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed match to stay disjoint")
	}
	if !result.ExprsHaveEqualExtentSize(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed match to retain equal extent size")
	}
	if result.ExprsHaveSameExtent(leftExpr, rightExpr) {
		t.Fatalf("expected split view payloads recovered through helper-indexed packed match to retain distinct exact bounds")
	}
	if !childFacts.FrozenPackedStoreOnly {
		t.Fatalf("expected packed child payload recovered through helper-indexed frozen match to stay frozen-store-only, got %#v", childFacts)
	}
	if !result.ExprDependsOnlyOnFrozenPackedStores(childExpr) {
		t.Fatalf("expected result query to report frozen-store-only provenance for packed child payload recovered through helper-indexed match")
	}
}

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

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def inspect(owner: Arena) -> int:
	region scratch(1024)
	store: Expr.Store[Local] = Expr.Store(owner)
	local_ref: scratch i32& = new[scratch] 7
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_helper_indexed_field_match_mixed_child.llcontext", src)
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
	boxed: scratch Views& = new[scratch] Views(buf[0:2], buf[2:4])
	left_alloc: view[i32] = boxed.left
	right_alloc: view[i32] = boxed.right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_allocated_field_projection.llcontext", src)
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
	boxed: scratch Box& = new[scratch] Box(new[store] Expr.HoldViews(left: buf[0:2], right: buf[2:4], child: child))
	frozen: Expr.Store[Frozen] = freeze(move store)
	move boxed.node in frozen as Expr.HoldViews(left, right, child_alias)
	left_copy: view[i32] = left
	right_copy: view[i32] = right
	child_copy: Expr = child_alias
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_allocated_field_move_as.llcontext", src)
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
	items: array[Views, 1] = [Views(buf[0:2], buf[2:4])]
	left_indexed: view[i32] = items[0].left
	right_indexed: view[i32] = items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_indexed_field_projection.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_indexed_values.llcontext", src)
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
	local_ref: scratch i32& = new[scratch] 7
	held: Expr = new[store] Expr.Hold(value: local_ref)
	items: array[Expr, 2] = [node, held]
	frozen: Expr.Store[Frozen] = freeze(move store)
	pure_indexed: Expr = items[0]
	mixed_indexed: Expr = items[1]
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_direct_indexed_frozen_packed_provenance.llcontext", src)
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

@borrows_return_field(items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def inspect(buf: array[i32, 4]) -> int:
	wrapped: ViewHolder = wrap_indexed_views(buf[0:2], buf[2:4])
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_helper_indexed_field_projection.llcontext", src)
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

@borrows_return_field(holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def inspect(buf: array[i32, 4]) -> int:
	wrapped: NestedHolder = wrap_nested_indexed_views(buf[0:2], buf[2:4])
	left_indexed: view[i32] = wrapped.holder.items[0].left
	right_indexed: view[i32] = wrapped.holder.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_helper_indexed_field_projection.llcontext", src)
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

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 4]) -> int:
	items: array[Views, 2] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4])]
	wrapped: ViewWindow = wrap_sub(items[1:2], 0, 1)
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_rebased_helper_indexed_field_projection.llcontext", src)
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

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 4] = [Views(values[0:1], values[1:2]), Views(values[2:3], values[3:4]), Views(values[4:5], values[5:6]), Views(values[6:7], values[7:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[1:3], 0, 2)
	left_indexed: view[i32] = wrapped.items[0].left
	right_indexed: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
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

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def inspect(values: array[i32, 8]) -> int:
	items: array[Views, 2] = [Views(values[0:3], values[1:4]), Views(values[4:7], values[5:8])]
	wrapped: ViewWindow = wrap_sub_wild(items[0:1], 0, 1)
	left_overlap: view[i32] = wrapped.items[0].left
	right_overlap: view[i32] = wrapped.items[0].right
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_nested_rebased_helper_indexed_field_projection.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_rebased_helper_indexed_field_move_as.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_wildcard_rebased_helper_indexed_field_move_as.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_wildcard_rebased_helper_indexed_field_move_as.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_proof_carrying_view_helpers.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_rebased_helper_indexed_field_match.llcontext", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_frozen_packed_nested_wildcard_rebased_helper_indexed_field_match.llcontext", src)
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
