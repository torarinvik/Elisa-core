package semantic_test

import "testing"

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
