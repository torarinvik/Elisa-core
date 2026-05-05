package semantic_test

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeAcceptsNamedArgsThroughBranchMergedCallbackWhenParamNamesAgree(t *testing.T) {
	src := `def add(x: i64, y: i64) -> i64:
	return x + y

def sub(x: i64, y: i64) -> i64:
	return x - y

def run(flag: bool) -> i64:
	local_fn: mutable func(i64, i64) -> i64 = sub
	if flag:
		local_fn <- add
	else:
		local_fn <- sub
	return local_fn(y: 7, x: do:
		seed = 3
		seed
	)
`
	result, errs := parseAndAnalyze(t, "named_args_branch_merged_callback_same_names_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeRejectsNamedArgsThroughBranchMergedCallbackWhenParamNamesDiffer(t *testing.T) {
	src := `def add(x: i64, y: i64) -> i64:
	return x + y

def mix(left: i64, right: i64) -> i64:
	return left + right

def bad(flag: bool) -> i64:
	local_fn: mutable func(i64, i64) -> i64 = mix
	if flag:
		local_fn <- add
	else:
		local_fn <- mix
	return local_fn(y: 7, x: 3)
`
	_, errs := parseAndAnalyze(t, "named_args_branch_merged_callback_different_names_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `function "func" does not expose parameter names for named argument calls`) {
		t.Fatalf("expected differing-name branch-merged callback named-arg diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPackedCommonFieldStorageAnnotations(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
		@storage(inline)
		kind: int
	Lit(value: int)
	End
`
	result, errs := parseAndAnalyze(t, "packed_common_field_storage_annotations.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	enumType, ok := result.NamedTypes["Expr"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Expr named type to resolve to semantic.EnumType, got %#v", result.NamedTypes["Expr"])
	}
	spanField, ok := enumType.Common["span"]
	if !ok {
		t.Fatalf("expected packed enum common field span to be present")
	}
	if spanField.PackedStorage != semantic.PackedFieldStorageSideTable {
		t.Fatalf("expected Expr.common.span to resolve to side-table storage, got %q", spanField.PackedStorage)
	}
	kindField, ok := enumType.Common["kind"]
	if !ok {
		t.Fatalf("expected packed enum common field kind to be present")
	}
	if kindField.PackedStorage != semantic.PackedFieldStorageInline {
		t.Fatalf("expected Expr.common.kind to resolve to inline storage, got %q", kindField.PackedStorage)
	}
}
func TestAnalyzeRejectsRemovedPackedABIAnnotation(t *testing.T) {
	src := `@packed_abi(dense_fixed)
packed enum Expr:
	Lit(value: int)
`
	_, errs := parseAndAnalyze(t, "packed_abi_removed.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "@packed_abi on enum \"Expr\" has been removed; use @packed_profile(canonical|retained_reads|build_heavy) instead") {
		t.Fatalf("expected removed packed_abi diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsRemovedPackedPrefixAnnotation(t *testing.T) {
	src := `@packed_prefix(common_only)
packed enum Expr:
	common:
		span: int
	Lit(value: int)
`
	_, errs := parseAndAnalyze(t, "packed_prefix_removed.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "@packed_prefix on enum \"Expr\" has been removed; use @packed_profile(canonical|retained_reads|build_heavy) instead") {
		t.Fatalf("expected removed packed_prefix diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsPackedFieldStorageAnnotationsOutsidePackedEnumCommonFields(t *testing.T) {
	src := `struct Bad:
	@storage(side_table)
	value: int
`
	_, errs := parseAndAnalyze(t, "packed_common_field_storage_annotation_target_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "field annotation @storage is only supported on packed enum common fields") {
		t.Fatalf("expected unsupported field-annotation target diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsInvalidPackedCommonFieldStorageMode(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(moonbeam)
		span: int
	Lit(value: int)
`
	_, errs := parseAndAnalyze(t, "packed_common_field_storage_mode_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expected inline or side_table") {
		t.Fatalf("expected invalid storage mode diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingExternReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

@borrows_return(holder.pool_ref)
extern get_pool_ref(holder: PoolHolder) -> ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = get_pool_ref(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern-returned alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingExternReturnedAggregateBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

@borrows_return(holder)
extern keep_holder(holder: PoolHolder) -> PoolHolder

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	alias_holder: PoolHolder = keep_holder(holder)
	pool_shutdown(alias_holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_aggregate_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern aggregate-returned alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingExternReturnedNestedBorrowedThreadPoolFieldAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolWrapper:
	inner: PoolHolder

@borrows_return_field(inner.pool_ref, holder.pool_ref)
extern wrap_holder(holder: PoolHolder) -> PoolWrapper

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	wrapped: PoolWrapper = wrap_holder(holder)
	pool_shutdown(wrapped.inner.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_extern_return_nested_field_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected extern nested-field alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

@borrows_return(holder)
extern keep_holder(holder: GroupHolder) -> GroupHolder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = keep_holder(holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_returned_aggregate_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternRebasedReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

@borrows_return_rebased(items)
extern sub_items(items: view[GroupHolder], start: usize, end: usize) -> view[GroupHolder]

def ok(items: view[GroupHolder], task: Task[i64, Pending]) -> void:
	sub: view[GroupHolder] = sub_items(items, 1, 2)
	task_group_add(sub[0].group_ref, move task)
	wait all items[1].group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_rebased_aggregate_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaExternFieldRebasedReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupWindow:
	items: view[GroupHolder]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[GroupHolder], start: usize, end: usize) -> GroupWindow

def ok(items: view[GroupHolder], task: Task[i64, Pending]) -> void:
	window: GroupWindow = wrap_sub(items, 1, 2)
	task_group_add(window.items[0].group_ref, move task)
	wait all items[1].group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_extern_field_rebased_aggregate_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsDoubleThreadPoolShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def bad() -> void:
	pool: ThreadPool = pool_new(2)
	pool_shutdown((&pool).cast[ThreadPool&])
	pool_shutdown((&pool).cast[ThreadPool&])
`
	_, errs := parseAndAnalyze(t, "thread_pool_double_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected double-shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsReinitializingThreadPoolAfterShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def ok() -> void:
	pool: mutable ThreadPool = pool_new(2)
	pool_shutdown((&pool).cast[ThreadPool&])
	pool <- pool_new(1)
	pool_shutdown((&pool).cast[ThreadPool&])
`
	result, errs := parseAndAnalyze(t, "thread_pool_reinitialize_after_shutdown_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsCopyingThreadPoolOwnerWithoutMove(t *testing.T) {
	src := `def bad(pool: ThreadPool) -> void:
	copy: ThreadPool = pool
`
	_, errs := parseAndAnalyze(t, "thread_pool_owner_copy_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" must be moved explicitly before move into local \"copy\"") {
		t.Fatalf("expected thread-pool owner move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingMovedTaskGroupOwner(t *testing.T) {
	src := `def bad(group: TaskGroup) -> void:
	moved: TaskGroup = move group
	_ = move group
`
	_, errs := parseAndAnalyze(t, "task_group_owner_reuse_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group owner \"group\" cannot be used: usage facts were consumed by move into local \"moved\"") {
		t.Fatalf("expected task-group owner reuse diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllSyntax(t *testing.T) {
	src := `extern task_group_wait_all(group: TaskGroup&) -> void

def ok(group: mutable TaskGroup) -> void:
    wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_group_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsNotifySyntax(t *testing.T) {
	src := `extern notify_one(cv: CondVar&) -> void
extern notify_all(cv: CondVar&) -> void

def ok(cv: mutable CondVar, broadcast: bool) -> void:
    if broadcast:
        notify all cv
    else:
        notify one cv
`
	result, errs := parseAndAnalyze(t, "notify_syntax_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsAtomicRmwBuiltins(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_add(slot: atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_sub(slot: atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_or(slot: atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_and(slot: atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_xor(slot: atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]

def ok(slot: mutable atomic[i64]) -> i64:
	can Atomics.Rmw:
		slot_ref: atomic[i64]& = (&slot).cast[atomic[i64]&]
		add: i64 = fetch_add(slot_ref, 1, MemoryOrder.AcqRel)
		sub: i64 = fetch_sub(slot_ref, 2, MemoryOrder.AcqRel)
		or_bits: i64 = fetch_or(slot_ref, 4, MemoryOrder.AcqRel)
		and_bits: i64 = fetch_and(slot_ref, 8, MemoryOrder.AcqRel)
		xor_bits: i64 = fetch_xor(slot_ref, 16, MemoryOrder.AcqRel)
		return add + sub + or_bits + and_bits + xor_bits
`
	result, errs := parseAndAnalyze(t, "atomic_rmw_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}
func TestAnalyzeRejectsAtomicRmwOnBoolPayload(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_or(slot: atomic[bool]&, value: bool, order: MemoryOrder) -> bool can[Atomics.Rmw]

def bad(slot: mutable atomic[bool]) -> bool can[Atomics.Rmw]:
	slot_ref: atomic[bool]& = (&slot).cast[atomic[bool]&]
	return fetch_or(slot_ref, true, MemoryOrder.AcqRel)
`
	_, errs := parseAndAnalyze(t, "atomic_rmw_bool_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"fetch_or\" requires atomic_numeric(T), got atomic[bool]") {
		t.Fatalf("expected atomic_numeric bool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAtomicRmwOnPointerPayload(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_xor(slot: atomic[u8&]&, value: u8&, order: MemoryOrder) -> u8& can[Atomics.Rmw]

def bad(slot: mutable atomic[u8&], value: u8&) -> u8& can[Atomics.Rmw]:
	slot_ref: atomic[u8&]& = (&slot).cast[atomic[u8&]&]
	return fetch_xor(slot_ref, value, MemoryOrder.AcqRel)
`
	_, errs := parseAndAnalyze(t, "atomic_rmw_pointer_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"fetch_xor\" requires atomic_numeric(T), got atomic[u8&]") {
		t.Fatalf("expected atomic_numeric pointer diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsLockSyntax(t *testing.T) {
	src := `extern mutex_lock(mu: Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void
extern cond_wait(cv: CondVar&, g: MutexGuard[Held]) -> MutexGuard[Held]

def ok(mu: mutable Mutex, cv: mutable CondVar, ready: bool) -> void:
	can Sync.Lock, Sync.Unlock, Sync.Wait:
		lock mu as g:
			while not ready:
				g <- cond_wait((&cv).cast[CondVar&], move g)
`
	result, errs := parseAndAnalyze(t, "lock_scope_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsPoolScopeSyntax(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def ok() -> bool:
	can Pool.Create, Pool.Shutdown:
		pool workers(2):
			return workers.handle != null
`
	result, errs := parseAndAnalyze(t, "pool_scope_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "bool")
}
func TestAnalyzeAcceptsSubmitSyntaxInsidePoolScope(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: mutable ThreadPool&) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def pool_submit1(pool: mutable ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def ok() -> i64:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.Await:
		pool workers(2):
			task: Task[i64, Pending] = submit work(7)
			return await task
`
	result, errs := parseAndAnalyze(t, "submit_syntax_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}
func TestAnalyzeAcceptsExplicitSubmitSyntaxOutsidePoolScope(t *testing.T) {
	src := `extern pool_await(task: Task[i64, Pending]) -> i64

def pool_submit1(pool: mutable ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def ok(pool: mutable ThreadPool&) -> i64 can[Pool.Submit, Pool.Await]:
	task: Task[i64, Pending] = submit[pool] work(7)
	return await task
`
	result, errs := parseAndAnalyze(t, "submit_explicit_pool_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}
func TestParseRejectsSubmitOutsidePoolScope(t *testing.T) {
	src := `def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	_ = submit work(7)
`
	_, errs := parseAndAnalyze(t, "submit_outside_pool_parse_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected parse error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "submit requires an active pool scope") {
		t.Fatalf("expected active-pool submit parse diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
