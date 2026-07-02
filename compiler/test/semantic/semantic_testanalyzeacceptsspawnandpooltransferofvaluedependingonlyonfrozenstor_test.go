package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsSpawnAndPoolTransferOfValueDependingOnlyOnFrozenStore(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern pool_await(task: Task[i64, Pending]) -> i64

packed enum Expr:
	Int(value: int)

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def ok(owner: Arena, pool: ThreadPool&) -> i64 can[Pool.Submit]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	frozen: Expr.Store[Frozen] = freeze(move store)
	task: Task[i64, Pending] = submit[pool] worker(node)
	_ = pool_await(move task)
	_ = frozen
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_frozen_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsThreadTransferOfBlessedRuntimeCarriers(t *testing.T) {
	src := `struct SharedGate:
	mu: Mutex
	cv: CondVar

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(gate: SharedGate) -> i64:
	return 1

def ok(pool_ref: ThreadPool&, mu: Mutex, cv: CondVar) -> i64 can[Pool.Submit]:
	_ = submit[pool_ref] worker(SharedGate(mu, cv))
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_runtime_carriers_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsSpawnTransferOfStaticRef(t *testing.T) {
	src := `extern shared_cell() -> static i32&

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(cell: static i32&) -> i64:
	return cell[0].i64()

def ok(pool: ThreadPool&) -> Task[i64, Pending] can[Pool.Submit]:
	return submit[pool] worker(shared_cell())
`
	result, errs := parseAndAnalyze(t, "spawn1_static_ref_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsThreadTransferOfNonStaticRef(t *testing.T) {
	src := `def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(cell: i32&) -> i64:
	return cell[0].i64()

def bad(pool: ThreadPool&, cell: i32&) -> i64:
	_ = spawn1(worker, cell)
	_ = pool_submit1(pool, worker, cell)
	return 0
`
	_, errs := parseAndAnalyze(t, "thread_transfer_non_static_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" is not structurally shareable across threads: i32&") {
		t.Fatalf("expected non-static-ref spawn diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "argument to \"pool_submit1\" is not structurally shareable across threads: i32&") {
		t.Fatalf("expected non-static-ref pool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsThreadTransferOfBlessedRuntimeCarrierResults(t *testing.T) {
	src := `struct SharedGate:
	mu: Mutex
	cv: CondVar

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def echo(gate: SharedGate) -> SharedGate:
	return gate

def ok(pool_ref: ThreadPool&, mu: Mutex, cv: CondVar) -> i64 can[Pool.Submit]:
	gate: SharedGate = SharedGate(mu, cv)
	_ = submit[pool_ref] echo(gate)
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_runtime_carrier_result_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsThreadTransferOfStaticRefResult(t *testing.T) {
	src := `extern shared_cell() -> static i32&

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(value: i64) -> static i32&:
	_ = value
	return shared_cell()

def ok(pool: ThreadPool&) -> i64 can[Pool.Submit]:
	_ = submit[pool] worker(0)
	return 0
`
	result, errs := parseAndAnalyze(t, "thread_transfer_static_ref_result_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsThreadTransferOfNonStaticRefResult(t *testing.T) {
	src := `extern borrowed_cell() -> i32&

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(value: i64) -> i32&:
	_ = value
	return borrowed_cell()

def bad(pool: ThreadPool&) -> i64:
	_ = spawn1(worker, 0)
	_ = pool_submit1(pool, worker, 0)
	return 0
`
	_, errs := parseAndAnalyze(t, "thread_transfer_non_static_ref_result_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "result of \"spawn1\" is not structurally shareable across threads: i32&") {
		t.Fatalf("expected non-static-ref spawn-result diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, "result of \"pool_submit1\" is not structurally shareable across threads: i32&") {
		t.Fatalf("expected non-static-ref pool-result diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsSpawnOfNestedValueDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(box: Box) -> i64:
	_ = box
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	box: Box = wrap_node(node)
	return spawn1(worker, box)
`
	_, errs := parseAndAnalyze(t, "spawn1_nested_unpublished_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot cross thread boundary: store dependency facts require rebase to frozen/public store, got \"Expr.Store[Local]\"") {
		t.Fatalf("expected nested unpublished-store spawn diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsSpawnOfNestedValueAfterFreezeRemapsPackedStoreRecursively(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(box: Box) -> i64:
	_ = box
	return 0

def ok(owner: Arena, pool: ThreadPool&) -> Task[i64, Pending] can[Pool.Submit]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	box: Box = wrap_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return submit[pool] worker(box)
`
	result, errs := parseAndAnalyze(t, "spawn1_nested_frozen_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsSpawnOfNestedViewDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(items: view[Box]) -> i64:
	_ = items
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 1] = [wrap_node(node)]
	return spawn1(worker, items[0:1])
`
	_, errs := parseAndAnalyze(t, "spawn1_nested_view_unpublished_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot cross thread boundary: store dependency facts require rebase to frozen/public store, got \"Expr.Store[Local]\"") {
		t.Fatalf("expected nested view unpublished-store spawn diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsSpawnOfNestedViewAfterFreezeRemapsPackedStoreRecursively(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

struct Box:
	node: Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(items: view[Box]) -> i64:
	_ = items
	return 0

def ok(owner: Arena, pool: ThreadPool&) -> Task[i64, Pending] can[Pool.Submit]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	items: array[Box, 1] = [wrap_node(node)]
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return submit[pool] worker(items[0:1])
`
	result, errs := parseAndAnalyze(t, "spawn1_nested_view_frozen_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsPoolTransferOfValueDependingOnLocalRegion(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: i32&)

def pool_submit1[A, R](pool: ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def bad(owner: Arena, pool: ThreadPool&) -> Task[i64, Pending]:
	region scratch
	store: Expr.Store[Local] = Expr.Store(owner)
	cell: i32& = new[scratch] 1
	node: Expr = new[store] Expr.Hold(value: cell)
	frozen: Expr.Store[Frozen] = freeze(move store)
	_ = frozen
	return pool_submit1(pool, worker, node)
`
	_, errs := parseAndAnalyze(t, "pool_submit_local_region_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"pool_submit1\" cannot cross thread boundary: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected local-region pool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsDictSurfaceTypesAndRuntimeBridge(t *testing.T) {
	src := `extern take_runtime(values: DynDict[cstr[row], i32]) -> void
extern make_runtime() -> DynDict[cstr[row], i32]

def id[V](values: dict[cstr, V]) -> dict[cstr, V]:
	return values

def keep(values: dict[cstr, i32]) -> dict[cstr, i32]:
	return id(values)

def use(values: dict[cstr[row], i32]) -> dict[cstr[row], i32]:
	take_runtime(values)
	return make_runtime()
`
	_, errs := parseAndAnalyze(t, "dict_surface_and_bridge_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsGenericDictKeyTypes(t *testing.T) {
	src := `struct Pair:
	left: i32
	right: i32

def ok(values: dict[i32, i32], keyed: dict[Pair, i32]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "dict_generic_keys_ok.elisa", src)
	requireNoErrors(t, errs)
}

// Integral/bool/enum/cstr keys are runtime-backed, but a float key is not (== is unsafe on
// floats), so the `.get` runtime sugar is rejected on a `dict[f64, V]`.
func TestAnalyzeRejectsFloatKeyRuntimeBackedDictSugar(t *testing.T) {
	src := `def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> mutable T&?:
	return null

def use(values: dict[f64, i32], key: f64) -> mutable i32&?:
	return values.get(key)
`
	_, errs := parseAndAnalyze(t, "dict_float_key_runtime_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "runtime-backed dict keys must be cstr, an integer type, bool, or a const enum") {
		t.Fatalf("expected float-key runtime-backed dict diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAllocatingFromDestroyedRegion(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	destroy scratch
	value: i32& = new[scratch] 1
`
	_, errs := parseAndAnalyze(t, "manual_regions_destroyed_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot allocate from destroyed region \"scratch\"") {
		t.Fatalf("expected destroyed-region diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingReferenceInvalidatedByRestore(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	mark scratch as cp
	value: i32& = new[scratch] 1
	restore scratch from cp
	return value[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_ref.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"value\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingMoveBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	mark scratch as cp
	value: i32& = new[scratch] 1
	move value as alias
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-bound alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingStructFieldAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	alias: i32& = holder.value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_struct_field_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for struct field alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingMoveAsStructBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	move holder as Holder(alias, count)
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_struct_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-as struct alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsMoveAsStructScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	move holder as Holder(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_struct_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsMoveAsPackedVariantDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left(node: Expr, store: Expr.Store[Frozen]) -> Expr:
	move node in store as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result, errs := parseAndAnalyze(t, "move_as_packed_variant_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left", "Expr")
}
func TestAnalyzeAcceptsMoveAsPackedVariantNestedPattern(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Frozen]) -> int:
	move node in store as Expr.Add(Expr.Int(value), rhs)
	_ = rhs
	return value
`
	result, errs := parseAndAnalyze(t, "move_as_packed_variant_nested_pattern_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left_value", "int")
}
func TestAnalyzeAcceptsMoveAsPackedVariantDestructureWithInferredStore(t *testing.T) {
	src := `packed enum Expr:
	Int(int)
	Add(Expr, Expr)

def left(node: Expr, store: Expr.Store[Frozen]) -> Expr:
	in store:
		move node as Expr.Add(lhs, rhs)
		_ = rhs
		return lhs
`
	result, errs := parseAndAnalyze(t, "move_as_packed_variant_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left", "Expr")
}
func TestAnalyzeAcceptsMoveAsPackedVariantDestructureWithActiveStoreParam(t *testing.T) {
	src := `packed enum Expr:
	Int(int)
	Add(Expr, Expr)

def left(node: Expr, store: Expr.Store[Frozen]) -> Expr:
	move node as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result, errs := parseAndAnalyze(t, "move_as_packed_variant_active_store_param_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left", "Expr")
}
func TestAnalyzeAcceptsMoveAsPackedViewParamWithoutStoreClause(t *testing.T) {
	src := `packed enum Expr:
	Int(int)
	Add(Expr, Expr)

def left(view_node: packedview[Expr.Add]) -> Expr:
	move view_node as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result, errs := parseAndAnalyze(t, "move_as_packedview_param_inferred_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "left", "Expr")
}
