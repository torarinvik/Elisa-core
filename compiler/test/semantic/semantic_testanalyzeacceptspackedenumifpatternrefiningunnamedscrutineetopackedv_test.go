package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsPackedEnumIfPatternRefiningUnnamedScrutineeToPackedView(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(int)
	Add(Expr, Expr)

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.span

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value):
		return score(node) + value + node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_scrutinee_refined_to_unnamed_view_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumMatchArmRefiningScrutineeToPackedView(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def score_add(view_node: packedview[Expr.Add]) -> int:
	return view_node.left.span + view_node.right.span + view_node.span

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	match node in store:
		Expr.Int(value: value):
			return value
		Expr.Add(left: left, right: right):
			return score_add(node) + left.span + right.span
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_scrutinee_refined_to_view_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsMatchOnRefinedPackedViewScrutinee(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value: value):
		match node in store:
			Expr.Int(value: inner):
				return inner + value + node.span
			Expr.Add(left: left, right: right):
				return left.span + right.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_on_refined_view_scrutinee_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternBindingPackedViewAliasWithInferredStore(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(int)
	Add(Expr, Expr)

def read(node: Expr, store: Expr.Store[Local]) -> int:
	in store:
		if node as Expr.Int:
			lit: packedview[Expr.Int] = node
			return lit.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_view_alias_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternBindingPackedViewAliasWithActiveStoreParam(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Int:
		lit: packedview[Expr.Int] = node
		return lit.value + lit.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_view_alias_active_store_param_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithNestedPackedVariantPattern(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Add(Expr.Int(value), rhs):
		_ = rhs
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_nested_pattern_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithNamedPayloadDestructureAndRefinedScrutinee(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Int(value: value):
		return value + node.value + node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_named_destructure_refined_scrutinee_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithPackedViewParamWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(view_node: packedview[Expr.Int]) -> int:
	if view_node as Expr.Int(value: value):
		return value + view_node.value + view_node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_packedview_param_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithUnnamedPayloadDestructure(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Pair(int, int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Pair(left, right):
		return left + right + node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_unnamed_destructure_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithDirectFieldProjectionWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

struct RootBox:
	root: Expr

def read(store: Expr.Store[Frozen], index: usize) -> int:
	box: RootBox = RootBox(store[index])
	if box.root as Expr.Int(value: value):
		return value + box.root.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_direct_field_projection_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithActiveStoreParam(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Int(value: value):
		return value + node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_active_store_param_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithNestedOpenStylePackedVariantPattern(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Add(Expr.Int(value), rhs):
		_ = rhs
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_open_style_nested_pattern_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithPackedViewParamWithoutStoreClauseForOpenStyle(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(view_node: packedview[Expr.Int]) -> int:
	if view_node as Expr.Int(value: value):
		return value + view_node.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_open_style_packedview_param_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithFrozenIndexAliasWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(store: Expr.Store[Frozen], index: usize) -> int:
	node: Expr = store[index]
	alias: Expr = node
	if alias as Expr.Int(value: value):
		return value + alias.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_frozen_index_alias_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithHiddenStoreFieldIndexAliasWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct Box:
	store: Expr.Store[Local]

def make_box(owner: Arena) -> Box:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(value: 7)
	return Box(move store)

def read(owner: Arena) -> int:
	box: Box = make_box(owner)
	node: Expr = box.store[0]
	alias: Expr = node
	if alias as Expr.Int(value: value):
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_hidden_store_field_index_alias_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsIfPatternWithDirectFieldProjectionWithoutStoreClauseForOpenStyle(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

struct RootBox:
	root: Expr

def read(store: Expr.Store[Frozen], index: usize) -> int:
	box: RootBox = RootBox(store[index])
	if box.root as Expr.Int(value: value):
		return value + box.root.span
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_open_style_direct_field_projection_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsPackedEnumIfPatternBinderWithoutAs(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(flag: bool, node: Expr, store: Expr.Store[Local]) -> int:
	if node in store:
		return 1
	elif flag:
		return 2
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_binder_missing_as_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected missing `as` parse diagnostic")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "if pattern binder requires `as Enum.Variant(...)` after store expression") {
		t.Fatalf("expected missing `as` diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedEnumIfPatternBinderWithoutStoreClauseWithActiveStoreParam(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	if node as Expr.Int(value: value):
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_without_store_active_param_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumIfPatternBinderWithoutStoreClauseFromFieldProjection(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct RootBox:
	root: Expr

def read(store: Expr.Store[Frozen], index: usize) -> int:
	box: RootBox = RootBox(store[index])
	if box.root as Expr.Int(value: value):
		return value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_field_projection_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedStoreCountAndIndexTraversal(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	total: mutable int = 0
	index: mutable usize = 0
	while index < frozen.count:
		node: Expr = frozen[index]
		if node in frozen is Expr.Int(value: value):
			total <- total + value + node.span
		index <- index + 1
	return total
`
	result, errs := parseAndAnalyze(t, "packed_store_count_and_index_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "walk", "int")
}
func TestAnalyzeAcceptsDenseNodeKeysAndNodeTablesForFrozenPackedStores(t *testing.T) {
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
	again: Expr = frozen[key]
	values: view[i32] = table.values
	if values.len == frozen.count:
		return again.span
	return 0
`
	result, errs := parseAndAnalyze(t, "packed_store_dense_node_key_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "inspect", "i32")

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	keyExpr := requireOptimizationFactsVarInitExpr(t, fn, "key")
	tableExpr := requireOptimizationFactsVarInitExpr(t, fn, "table")
	againExpr := requireOptimizationFactsVarInitExpr(t, fn, "again")
	valuesExpr := requireOptimizationFactsVarInitExpr(t, fn, "values")

	requireExprTypeString(t, result, keyExpr, "NodeKey[Expr]")
	requireExprTypeString(t, result, tableExpr, "NodeTable[Expr, i32]")
	requireExprTypeString(t, result, againExpr, "Expr")
	requireExprTypeString(t, result, valuesExpr, "view[i32]")

	keyInfo, ok := result.DenseNodeKeys[keyExpr]
	if !ok {
		t.Fatal("expected dense_key expression provenance to be recorded")
	}
	if keyInfo.Enum == nil || keyInfo.Enum.Name != "Expr" {
		t.Fatalf("expected dense_key enum provenance for Expr, got %#v", keyInfo)
	}
	if keyInfo.StoreRoot == nil {
		t.Fatalf("expected dense_key exact frozen store root provenance, got %#v", keyInfo)
	}
	if keyInfo.StorePath != "" {
		t.Fatalf("expected dense_key direct-store provenance path to be empty, got %#v", keyInfo)
	}

	tableInfo, ok := result.NodeTables[tableExpr]
	if !ok {
		t.Fatal("expected node_table_fill expression metadata to be recorded")
	}
	if tableInfo.Enum == nil || tableInfo.Enum.Name != "Expr" {
		t.Fatalf("expected node table enum provenance for Expr, got %#v", tableInfo)
	}
	if tableInfo.Elem == nil || tableInfo.Elem.String() != "i32" {
		t.Fatalf("expected node table element type i32, got %#v", tableInfo)
	}
	if tableInfo.StoreRoot == nil {
		t.Fatalf("expected node table exact frozen store root provenance, got %#v", tableInfo)
	}
	if tableInfo.StorePath != "" {
		t.Fatalf("expected node table direct-store provenance path to be empty, got %#v", tableInfo)
	}
	if tableInfo.CountExpr == "" {
		t.Fatalf("expected node table count expression metadata, got %#v", tableInfo)
	}
}
func TestAnalyzeAcceptsDenseNodeKeysAndNodeTablesFromHiddenFrozenStoreFieldRoots(t *testing.T) {
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
	again: Expr = box.store[key]
	values: view[i32] = table.values
	if values.len == box.store.count:
		return again.span
	return 0
`
	result, errs := parseAndAnalyze(t, "packed_store_dense_node_key_hidden_frozen_field_root_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "inspect", "i32")

	fn := requireOptimizationFactsFunctionDecl(t, result, "inspect")
	keyExpr := requireOptimizationFactsVarInitExpr(t, fn, "key")
	tableExpr := requireOptimizationFactsVarInitExpr(t, fn, "table")

	keyInfo, ok := result.DenseNodeKeys[keyExpr]
	if !ok {
		t.Fatal("expected dense_key expression provenance to be recorded")
	}
	if keyInfo.StoreRoot == nil || keyInfo.StorePath != "store" {
		t.Fatalf("expected dense_key hidden frozen store-field provenance, got %#v", keyInfo)
	}

	tableInfo, ok := result.NodeTables[tableExpr]
	if !ok {
		t.Fatal("expected node_table_fill expression metadata to be recorded")
	}
	if tableInfo.StoreRoot == nil || tableInfo.StorePath != "store" {
		t.Fatalf("expected node_table hidden frozen store-field provenance, got %#v", tableInfo)
	}
}
func TestAnalyzeRejectsDenseNodeKeyIndexingAcrossDifferentFrozenStoreRoots(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)

def bad(owner: Arena) -> Expr:
	left_store: Expr.Store[Local] = Expr.Store(owner)
	in left_store:
		_ = new Expr.Lit(value: 1)
	left_frozen: Expr.Store[Frozen] = freeze(move left_store)

	right_store: Expr.Store[Local] = Expr.Store(owner)
	in right_store:
		_ = new Expr.Lit(value: 2)
	right_frozen: Expr.Store[Frozen] = freeze(move right_store)

	node: Expr = left_frozen[0]
	key: NodeKey[Expr] = dense_key(node, left_frozen)
	return right_frozen[key]
`
	_, errs := parseAndAnalyze(t, "packed_store_dense_node_key_cross_root_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "node key and frozen store must share the same exact frozen store root") {
		t.Fatalf("expected cross-store dense node key diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDenseNodeKeyIndexingAcrossDifferentHiddenFrozenStoreFieldRoots(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)

struct FrozenPair:
	left: Expr.Store[Frozen]
	right: Expr.Store[Frozen]

def make_pair(owner: Arena) -> FrozenPair:
	left_store: Expr.Store[Local] = Expr.Store(owner)
	in left_store:
		_ = new Expr.Lit(value: 1)
	left_frozen: Expr.Store[Frozen] = freeze(move left_store)

	right_store: Expr.Store[Local] = Expr.Store(owner)
	in right_store:
		_ = new Expr.Lit(value: 2)
	right_frozen: Expr.Store[Frozen] = freeze(move right_store)
	return FrozenPair(left_frozen, right_frozen)

def bad(owner: Arena) -> Expr:
	pair: FrozenPair = make_pair(owner)
	node: Expr = pair.left[0]
	key: NodeKey[Expr] = dense_key(node, pair.left)
	return pair.right[key]
`
	_, errs := parseAndAnalyze(t, "packed_store_dense_node_key_cross_hidden_field_root_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "node key and frozen store must share the same exact frozen store root") {
		t.Fatalf("expected hidden-field dense node key root diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAssigningPackedStoreIndexResult(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(store: Expr.Store[Frozen], node: Expr) -> void:
	store[0] <- node
`
	_, errs := parseAndAnalyze(t, "packed_store_index_assign_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign to packed store index result") {
		t.Fatalf("expected packed store index assignment diagnostic, got:\n%s", all)
	}
}
