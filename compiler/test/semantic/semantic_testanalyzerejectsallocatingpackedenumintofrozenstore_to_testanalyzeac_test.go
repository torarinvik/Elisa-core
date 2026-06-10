package semantic_test

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestAnalyzeRejectsAllocatingPackedEnumIntoFrozenStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(owner)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return new[frozen] Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_frozen_alloc_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum allocation requires local store type \"Expr.Store[Local]\", got Expr.Store[Frozen]") {
		t.Fatalf("expected frozen-store allocation diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedMatchWithFrozenStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	match node in store:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_frozen_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsBarePackedEnumConstructorCall(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad() -> Expr:
	return Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_ctor_without_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum constructor \"Expr.Int\" requires an active in Expr.Store: scope or explicit new[Expr.Store]") {
		t.Fatalf("expected packed constructor diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsBarePackedEnumConstructorCallWithActiveStoreLocal(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def build(owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(owner)
	return Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_ctor_with_active_store_local_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsBarePackedAllocOutsideInStoreScope(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad() -> Expr:
	return new Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_alloc_without_scope_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum constructor \"Expr.Int\" requires an active in Expr.Store: scope or explicit new[Expr.Store]") {
		t.Fatalf("expected in-store allocation diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsBarePackedAllocWithActiveStoreLocal(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def build(owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(owner)
	return new Expr.Int(value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_alloc_with_active_store_local_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsPackedMatchWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(node: Expr) -> int:
	match node:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_missing_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum match over \"Expr\" requires an in Expr.Store clause") {
		t.Fatalf("expected packed match-store diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedMatchWithoutStoreClauseWithActiveStoreParam(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def read(node: Expr, store: Expr.Store[Frozen]) -> int:
	match node:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_with_active_store_param_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedMatchWithoutStoreClauseFromFrozenIndexExpr(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def read(store: Expr.Store[Frozen], index: usize) -> int:
	match store[index]:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_frozen_index_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedMatchWithoutStoreClauseFromHiddenStoreFieldIndexExpr(t *testing.T) {
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
	match box.store[0]:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_hidden_store_field_index_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedMatchWithoutStoreClauseFromImmutableFieldProjection(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct RootBox:
	root: Expr

def read(store: Expr.Store[Frozen], index: usize) -> int:
	box: RootBox = RootBox(store[index])
	match box.root:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "packed_enum_match_field_projection_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsOrdinaryEnumMatchWithStoreClause(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

packed enum PackedExpr:
	Int(value: int)

def bad(node: Expr, store: PackedExpr.Store[Local]) -> int:
	match node in store:
		Expr.Int(value: value):
			return value
`
	_, errs := parseAndAnalyze(t, "ordinary_enum_match_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "ordinary enum match over \"Expr\" does not take an in-store clause") {
		t.Fatalf("expected ordinary enum in-store rejection, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedCommonFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr) -> int:
	return node.span
`
	_, errs := parseAndAnalyze(t, "packed_enum_common_field_access_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedCommonFieldInitializationWithNamedArgs(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def build(store_owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(store_owner)
	return new[store] Expr.Int(span: 7, value: 1)
`
	_, errs := parseAndAnalyze(t, "packed_enum_common_field_init_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPackedEnumIfPatternBinder(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value: value):
		return value + node.span
	else:
		return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_binder_ok.elisa", src)
	requireNoErrors(t, errs)
}

// docs/80 Phase D: `if value in store is Pattern` is the sole form; `as` is hard-rejected.
func TestAnalyzeAcceptsPackedEnumIfStorePatternBinderIsForm(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value: value):
		return value
	return 0
`
	result, errs := parseAndAnalyze(t, "packed_enum_if_store_pattern_binder_is_form.elisa", src)
	requireNoErrors(t, errs)
	if deprecations := strings.Join(result.Deprecations(), "\n"); strings.Contains(deprecations, "is deprecated") {
		t.Fatalf("expected the `is` store form to carry no deprecation, got:\n%s", deprecations)
	}
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsPackedEnumIfPatternBinderWithElif(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def classify(flag: bool, node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value: value):
		return value + node.span
	elif flag:
		return 7
	else:
		return 9
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_binder_elif_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsEnumVariantIsCondition(t *testing.T) {
	src := `enum Expr:
	Int(value: int)
	Add(left: int, right: int)

def is_int(node: Expr) -> bool:
	if node is Expr.Int:
		return true
	return false
`
	result, errs := parseAndAnalyze(t, "enum_variant_is_condition_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	decl := requireFuncDecl(t, result, "is_int")
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected first statement to be if, got %T", decl.Body[0])
	}
	requireExprTypeString(t, result, ifStmt.Cond, "bool")
}
func TestAnalyzeAcceptsEnumVariantIsConditionWithPositionalLiteralPayloadPattern(t *testing.T) {
	src := `enum Expr:
	Float(PI: f64)
	Int(value: int)

def is_pi(node: Expr) -> bool:
	return node is Expr.Float(3.14)
`
	result, errs := parseAndAnalyze(t, "enum_variant_is_condition_positional_literal_payload_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsEnumVariantIsConditionWithNamedLiteralPayloadPattern(t *testing.T) {
	src := `enum Expr:
	Float(PI: int)
	Int(value: int)

def is_pi(node: Expr) -> bool:
	return node is Expr.Float(PI: 314)
`
	result, errs := parseAndAnalyze(t, "enum_variant_is_condition_named_literal_payload_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsEnumIfPatternWithPositionalLiteralPayloadPattern(t *testing.T) {
	src := `enum Expr:
	Float(PI: int)
	Int(value: int)

def classify(node: Expr) -> int:
	if node as Expr.Float(314):
		return 1
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_if_pattern_positional_literal_payload_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectPatternStatement(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def check(node: Expr) -> void:
	can Abort.Panic:
		expect node as Expr.Int(value)
		assert value != 0
`
	_, errs := parseAndAnalyze(t, "expect_pattern_statement_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectLetPatternStatement(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def check(node: Expr) -> int:
	can Abort.Panic:
		expect let Expr.Int(value) = node
		return value
`
	_, errs := parseAndAnalyze(t, "expect_let_pattern_statement_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsAssertPatternCondition(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def check(node: Expr) -> void:
	assert node is Expr.Int(_)
`
	_, errs := parseAndAnalyze(t, "assert_pattern_condition_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectPatternBlockCaptures(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def check(node: Expr) -> int:
	can Abort.Panic:
		expect node as Expr.Int(value):
			return value
	return 0
`
	_, errs := parseAndAnalyze(t, "expect_pattern_block_captures_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectPatternPinnedValue(t *testing.T) {
	src := `enum Expr:
	Infix(op: int, left: int, right: int)

def check(node: Expr, expected_op: int) -> void:
	can Abort.Panic:
		expect node as Expr.Infix(^expected_op, _, _)
`
	_, errs := parseAndAnalyze(t, "expect_pattern_pinned_value_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectListPatternWithPredicate(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def nonzero(value: int) -> bool:
	return value != 0

def check(values: darray[Expr]) -> void:
	can Abort.Panic:
		expect values as [
			Expr.Int(nonzero),
			_
		]
`
	_, errs := parseAndAnalyze(t, "expect_list_pattern_predicate_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExpectCountPatternForSequencePayloads(t *testing.T) {
	src := `enum Stmt:
	Call(name: int, args: darray[int])

def check(stmts: darray[Stmt]) -> void:
	can Abort.Panic:
		expect stmts as [
			Stmt.Call(1, count(2)),
			Stmt.Call(2, count(0))
		]
`
	_, errs := parseAndAnalyze(t, "expect_count_pattern_sequence_payload_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsEnumVariantIsConditionPayloadBindPattern(t *testing.T) {
	src := `enum Expr:
	Float(value: int)

def unwrap(node: Expr) -> int:
	if node is Expr.Float(value):
		return value
	return 0
`
	result, errs := parseAndAnalyze(t, "enum_variant_is_condition_payload_bind_pattern_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsPackedEnumIsConditionRefiningScrutineeToPackedView(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.value + view_node.span

def fold(node: Expr) -> int:
	if node is Expr.Int:
		return score(node)
	return 0
`
	result, errs := parseAndAnalyze(t, "packed_enum_is_condition_refines_scrutinee_to_view_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsPackedEnumBareVariantTypeSugar(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def keep(view_node: Expr.Int) -> Expr.Int:
	return view_node

def score(view_node: Expr.Int) -> int:
	kept: Expr.Int = keep(view_node)
	return kept.value + kept.span

def fold(node: Expr) -> int:
	if node is Expr.Int:
		return score(node)
	return 0
`
	result, errs := parseAndAnalyze(t, "packed_enum_bare_variant_type_surface_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsOrdinaryEnumBareVariantTypeSugar(t *testing.T) {
	src := `enum Expr:
	Int(value: int)

def bad(node: Expr.Int) -> int:
	return 0
`
	_, errs := parseAndAnalyze(t, "ordinary_enum_bare_variant_type_surface_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "bare variant type \"Expr.Int\" requires a packed enum or tree category") {
		t.Fatalf("expected bare ordinary-enum variant-type diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedEnumIsConditionFallthroughRefiningScrutineeToPackedView(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.value + view_node.span

def fold(node: Expr) -> int:
	if not (node is Expr.Int):
		return 0
	return score(node)
`
	result, errs := parseAndAnalyze(t, "packed_enum_is_condition_fallthrough_refines_scrutinee_to_view_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsGuardNonnullConditionRefiningReference(t *testing.T) {
	src := `struct Box:
	value: int

@guard_nonnull(box)
def has_box(box: Box&?) -> bool:
	return box != null

def read(box: Box&?) -> int:
	if not has_box(box):
		return 0
	return box.value
`
	result, errs := parseAndAnalyze(t, "guard_nonnull_condition_refines_reference_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsGuardVariantConditionRefiningPackedScrutinee(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

@guard_variant(node, Expr.Int)
def is_int(node: Expr) -> bool:
	return node is Expr.Int

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.value + view_node.span

def fold(node: Expr) -> int:
	if is_int(node):
		return score(node)
	return 0
`
	result, errs := parseAndAnalyze(t, "guard_variant_condition_refines_packed_scrutinee_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsPackedEnumIfPatternRefiningScrutineeToPackedView(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.value + view_node.span

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store is Expr.Int(value: value):
		return score(node) + value
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_if_pattern_scrutinee_refined_to_view_ok.elisa", src)
	requireNoErrors(t, errs)
}
