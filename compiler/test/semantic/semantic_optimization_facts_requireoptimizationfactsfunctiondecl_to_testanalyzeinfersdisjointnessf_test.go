package semantic_test

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"testing"
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
	result, errs := parseAndAnalyze(t, "optimization_facts_shape_backed.elisa", src)
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
	result, errs := parseAndAnalyze(t, "optimization_facts_region_alloc_exclusive.elisa", src)
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

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, size_of(T))
	return DynArrayView(null, 0, size_of(T))

def arena_da_view_slice[T](view: view[T], start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	return view

def arena_da_from_view[T](a: Arena&, view: view[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def sview(value: u8&?, start: i64, end: i64) -> StringView:
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
	return sview(value, start, end)

def ctx_string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	return string_view_slice(view, start, end)

def ctx_string_from_view(view: StringView) -> cstr[shape_out]:
	return string_view_copy(view)

def ctx_string_slice(value: cstr[shape_in], start: i64, end: i64) -> cstr[shape_out]:
	_ = start
	_ = end
	return value

def inspect(a: Arena&, values: darray[i32, row]&, other: darray[i32, row]&, text: cstr[row]) -> int:
	whole_a: view[i32] = arena_da_view(values, 0, values.count)
	whole_b: view[i32] = arena_da_view(other, 0, other.count)
	sub_a: view[i32] = arena_da_view_slice(whole_a, 1, 3)
	sub_b: view[i32] = arena_da_view_slice(whole_b, 1, 3)
	copied: darray[i32] = arena_da_from_view(a, sub_a)
	text_view: StringView = ctx_string_view(text, 1, 3)
	text_sub: StringView = ctx_string_view_slice(text_view, 0, text_view.len)
	text_copy: cstr = ctx_string_from_view(text_sub)
	text_slice: cstr = ctx_string_slice(text, 1, 3)
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_runtime_view_helpers.elisa", src)
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
	values: view[i32] = table.values
	nodes: view[Expr] = frozen[0:frozen.count]
	if values.len == nodes.len:
		return values[0]
	return -1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_node_table_values.elisa", src)
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
	values: view[i32] = table.values
	nodes: view[Expr] = box.store[0:box.store.count]
	if values.len == nodes.len:
		return values[0]
	return -1
`
	result, errs := parseAndAnalyze(t, "optimization_facts_node_table_values_hidden_frozen_field_root.elisa", src)
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

def sview(value: u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	_ = end
	return StringView("", 0)

def ctx_string_view(value: cstr[shape_in], start: i64, end: i64) -> StringView:
	return sview(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return sview(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return sview(view.data, start, view.len)

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
	fresh_view_a: StringView = sview(new[scratch] 3, 0, 1)
	fresh_view_b: StringView = sview(new[scratch] 4, 0, 1)
	alloc_a: i32& @scratch = new[scratch] 1
	alloc_b: i32& @scratch = new[scratch] 2
	alloc_alias: i32& @scratch = alloc_a
	return 0
`
	result, errs := parseAndAnalyze(t, "optimization_facts_disjoint_views.elisa", src)
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
