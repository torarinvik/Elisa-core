package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeRejectsReusingMoveBoundBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	move holder as PoolHolder(pool_ref)
	pool_shutdown(pool_ref)
	_ = pool_submit1(pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_movebind_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-bind alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingHelperReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = get_pool_ref(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_helper_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingHelperReturnedAggregateBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def keep_holder(holder: PoolHolder) -> PoolHolder:
	return holder

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	alias_holder: PoolHolder = keep_holder(holder)
	pool_shutdown(alias_holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_helper_return_aggregate_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHelperReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = keep_holder(holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_helper_returned_aggregate_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def apply_getter(fn: fn(PoolHolder) -> ThreadPool&, holder: PoolHolder) -> ThreadPool&:
	return fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(get_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected higher-order helper-returned alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperReturnedAggregateAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def apply_keeper(fn: fn(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	return fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_returned_aggregate_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterLocalCallbackBinding(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def apply_getter(fn: fn(PoolHolder) -> ThreadPool&, holder: PoolHolder) -> ThreadPool&:
	local_fn: fn(PoolHolder) -> ThreadPool& = fn
	return local_fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(get_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_local_callback_binding_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected local callback binding shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperLocalCallbackBinding(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def apply_keeper(fn: fn(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	local_fn: fn(GroupHolder) -> GroupHolder = fn
	return local_fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_local_callback_binding_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaAggregateHeldCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: fn(PoolHolder) -> ThreadPool&

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter(get_pool_ref)
	pool_ref: ThreadPool& = getter.fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_aggregate_held_callback_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate-held callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateHeldCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: fn(GroupHolder) -> GroupHolder

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper{fn: keep_holder}
	alias_holder: GroupHolder = keeper.fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_held_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaMoveAsDestructuredCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: fn(PoolHolder) -> ThreadPool&

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter(get_pool_ref)
	move getter as PoolGetter(callback_fn)
	pool_ref: ThreadPool& = callback_fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_move_as_destructured_callback_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-as destructured callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMoveAsDestructuredCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: fn(GroupHolder) -> GroupHolder

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper{fn: keep_holder}
	move keeper as GroupKeeper(callback_fn)
	alias_holder: GroupHolder = callback_fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_move_as_destructured_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaMoveAsVariantDestructuredCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

enum PoolGetter:
	Wrap(fn: fn(PoolHolder) -> ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter.Wrap(get_pool_ref)
	move getter as PoolGetter.Wrap(callback_fn)
	pool_ref: ThreadPool& = callback_fn(holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_move_as_variant_destructured_callback_return_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected move-as variant destructured callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMoveAsVariantDestructuredCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

enum GroupKeeper:
	Wrap(fn: fn(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper.Wrap(keep_holder)
	move keeper as GroupKeeper.Wrap(callback_fn)
	alias_holder: GroupHolder = callback_fn(holder)
	task_group_add(alias_holder.group_ref, move task)
	trusted Unsafe.PointerCast:
		wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_move_as_variant_destructured_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaEnumMatchBoundCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

enum PoolGetter:
	Wrap(fn: fn(PoolHolder) -> ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	getter: PoolGetter = PoolGetter.Wrap(get_pool_ref)
	match getter:
		PoolGetter.Wrap(callback_fn):
			pool_ref: ThreadPool& = callback_fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_enum_match_bound_callback_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected enum match bound callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaEnumMatchBoundCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

enum GroupKeeper:
	Wrap(fn: fn(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	keeper: GroupKeeper = GroupKeeper.Wrap(keep_holder)
	match keeper:
		GroupKeeper.Wrap(callback_fn):
			alias_holder: GroupHolder = callback_fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			trusted Unsafe.PointerCast:
				wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_enum_match_bound_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaPackedEnumMatchBoundCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

packed enum PoolGetter:
	Wrap(fn: fn(PoolHolder) -> ThreadPool&)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	region store_owner
	store: PoolGetter.Store[Local] = PoolGetter.Store(store_owner)
	getter: PoolGetter = new[store] PoolGetter.Wrap(fn: get_pool_ref)
	match getter in store:
		PoolGetter.Wrap(callback_fn):
			pool_ref: ThreadPool& = callback_fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_packed_enum_match_bound_callback_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected packed enum match bound callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaPackedEnumMatchBoundCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

packed enum GroupKeeper:
	Wrap(fn: fn(GroupHolder) -> GroupHolder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	region store_owner
	store: GroupKeeper.Store[Local] = GroupKeeper.Store(store_owner)
	keeper: GroupKeeper = new[store] GroupKeeper.Wrap(fn: keep_holder)
	match keeper in store:
		GroupKeeper.Wrap(callback_fn):
			alias_holder: GroupHolder = callback_fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			trusted Unsafe.PointerCast:
				wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_packed_enum_match_bound_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
