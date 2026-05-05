package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaEnumMatchBoundAggregateProjectedCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: func(PoolHolder) -> ThreadPool&

enum GetterBox:
	Wrap(getter: PoolGetter)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	boxed: GetterBox = GetterBox.Wrap(getter: PoolGetter(get_pool_ref))
	match boxed:
		GetterBox.Wrap(wrapper):
			pool_ref: ThreadPool& = wrapper.fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_enum_match_bound_aggregate_projected_callback_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected enum match bound aggregate projected callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaEnumMatchBoundAggregateProjectedCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

enum KeeperBox:
	Wrap(keeper: GroupKeeper)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	boxed: KeeperBox = KeeperBox.Wrap(keeper: GroupKeeper(keep_holder))
	match boxed:
		KeeperBox.Wrap(wrapper):
			alias_holder: GroupHolder = wrapper.fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_enum_match_bound_aggregate_projected_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolAliasReturnedViaPackedEnumMatchBoundAggregateProjectedCallbackAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: func(PoolHolder) -> ThreadPool&

packed enum GetterBox:
	Wrap(getter: PoolGetter)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	region store_owner
	store: GetterBox.Store[Local] = GetterBox.Store(store_owner)
	boxed: GetterBox = new[store] GetterBox.Wrap(getter: PoolGetter(get_pool_ref))
	match boxed in store:
		GetterBox.Wrap(wrapper):
			pool_ref: ThreadPool& = wrapper.fn(holder)
			pool_shutdown(pool_ref)
			_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_packed_enum_match_bound_aggregate_projected_callback_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected packed enum match bound aggregate projected callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaPackedEnumMatchBoundAggregateProjectedCallback(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

packed enum KeeperBox:
	Wrap(keeper: GroupKeeper)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	region store_owner
	store: KeeperBox.Store[Local] = KeeperBox.Store(store_owner)
	boxed: KeeperBox = new[store] KeeperBox.Wrap(keeper: GroupKeeper(keep_holder))
	match boxed in store:
		KeeperBox.Wrap(wrapper):
			alias_holder: GroupHolder = wrapper.fn(holder)
			task_group_add(alias_holder.group_ref, move task)
			wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_packed_enum_match_bound_aggregate_projected_callback_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterAggregateCallbackParamProjection(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: func(PoolHolder) -> ThreadPool&

def apply_getter(wrapper: PoolGetter, holder: PoolHolder) -> ThreadPool&:
	return wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(PoolGetter(get_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_aggregate_callback_param_projection_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate callback param projection shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateCallbackParamProjection(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(wrapper: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	return wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_callback_param_projection_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterAggregateCallbackParamLocalAliasProjection(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: func(PoolHolder) -> ThreadPool&

def apply_getter(wrapper: PoolGetter, holder: PoolHolder) -> ThreadPool&:
	local_wrapper: PoolGetter = wrapper
	alias_wrapper: PoolGetter = local_wrapper
	return alias_wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(PoolGetter(get_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_aggregate_callback_param_local_alias_projection_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate callback param local alias projection shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateCallbackParamLocalAliasProjection(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(wrapper: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	local_wrapper: GroupKeeper = wrapper
	alias_wrapper: GroupKeeper = local_wrapper
	return alias_wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_callback_param_local_alias_projection_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterMutableAggregateCallbackWrapperRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

struct PoolGetter:
	fn: func(PoolHolder) -> ThreadPool&

def apply_getter(primary: PoolGetter, fallback: PoolGetter, holder: PoolHolder) -> ThreadPool&:
	local_wrapper: mutable PoolGetter = fallback
	local_wrapper <- primary
	return local_wrapper.fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def fallback_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(PoolGetter(get_pool_ref), PoolGetter(fallback_pool_ref), holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_mutable_aggregate_callback_wrapper_rebinding_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected mutable aggregate callback wrapper rebinding shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaMutableAggregateCallbackWrapperRebinding(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

struct GroupKeeper:
	fn: func(GroupHolder) -> GroupHolder

def apply_keeper(primary: GroupKeeper, fallback: GroupKeeper, holder: GroupHolder) -> GroupHolder:
	local_wrapper: mutable GroupKeeper = fallback
	local_wrapper <- primary
	return local_wrapper.fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def fallback_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(GroupKeeper(keep_holder), GroupKeeper(fallback_holder), holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_mutable_aggregate_callback_wrapper_rebinding_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterMutableCallbackRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def apply_getter(primary: func(PoolHolder) -> ThreadPool&, fallback: func(PoolHolder) -> ThreadPool&, holder: PoolHolder) -> ThreadPool&:
	local_fn: mutable func(PoolHolder) -> ThreadPool& = fallback
	local_fn <- primary
	return local_fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def get_pool_ref_fallback(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(get_pool_ref, get_pool_ref_fallback, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_mutable_callback_rebinding_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected mutable callback rebinding shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperMutableCallbackRebinding(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def apply_keeper(primary: func(GroupHolder) -> GroupHolder, fallback: func(GroupHolder) -> GroupHolder, holder: GroupHolder) -> GroupHolder:
	local_fn: mutable func(GroupHolder) -> GroupHolder = fallback
	local_fn <- primary
	return local_fn(holder)

def keep_holder(holder: GroupHolder) -> GroupHolder:
	return holder

def keep_holder_fallback(holder: GroupHolder) -> GroupHolder:
	return holder

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	alias_holder: GroupHolder = apply_keeper(keep_holder, keep_holder_fallback, holder)
	task_group_add(alias_holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_mutable_callback_rebinding_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterBranchMergedCallbackRebinding(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	left_pool_ref: ThreadPool&
	right_pool_ref: ThreadPool&

def apply_getter(flag: bool, primary: func(PoolHolder) -> ThreadPool&, fallback: func(PoolHolder) -> ThreadPool&, holder: PoolHolder) -> ThreadPool&:
	local_fn: mutable func(PoolHolder) -> ThreadPool& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- primary
	return local_fn(holder)

def get_left_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.left_pool_ref

def get_right_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.right_pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(true, get_left_pool_ref, get_right_pool_ref, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.left_pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_branch_merged_callback_rebinding_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.left_pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected branch-merged callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaHigherOrderHelperBranchMergedCallbackRebinding(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	primary_group_ref: TaskGroup&
	fallback_group_ref: TaskGroup&

def apply_getter(flag: bool, primary: func(GroupHolder) -> TaskGroup&, fallback: func(GroupHolder) -> TaskGroup&, holder: GroupHolder) -> TaskGroup&:
	local_fn: mutable func(GroupHolder) -> TaskGroup& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- primary
	return local_fn(holder)

def get_primary_group_ref(holder: GroupHolder) -> TaskGroup&:
	return holder.primary_group_ref

def get_fallback_group_ref(holder: GroupHolder) -> TaskGroup&:
	return holder.fallback_group_ref

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	group_ref: TaskGroup& = apply_getter(true, get_primary_group_ref, get_fallback_group_ref, holder)
	task_group_add(group_ref, move task)
	wait all holder.primary_group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_higher_order_helper_branch_merged_callback_rebinding_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsReusingHigherOrderHelperReturnedBorrowedThreadPoolAliasAfterBranchMergedCallbackWithDifferentParamNames(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def apply_getter(flag: bool, primary: func(PoolHolder) -> ThreadPool&, fallback: func(PoolHolder) -> ThreadPool&, holder: PoolHolder) -> ThreadPool&:
	local_fn: mutable func(PoolHolder) -> ThreadPool& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- fallback
	return local_fn(holder)

def get_pool_ref(holder: PoolHolder) -> ThreadPool&:
	return holder.pool_ref

def get_pool_ref_alias(box: PoolHolder) -> ThreadPool&:
	return box.pool_ref

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_ref: ThreadPool& = apply_getter(true, get_pool_ref, get_pool_ref_alias, holder)
	pool_shutdown(pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_higher_order_helper_branch_merged_callback_different_param_names_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected differing-param-name branch-merged callback shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaBranchMergedCallbacksWithDifferentParamNames(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def apply_getter(flag: bool, primary: func(GroupHolder) -> TaskGroup&, fallback: func(GroupHolder) -> TaskGroup&, holder: GroupHolder) -> TaskGroup&:
	local_fn: mutable func(GroupHolder) -> TaskGroup& = fallback
	if flag:
		local_fn <- primary
	else:
		local_fn <- fallback
	return local_fn(holder)

def get_group_ref(holder: GroupHolder) -> TaskGroup&:
	return holder.group_ref

def get_group_ref_alias(box: GroupHolder) -> TaskGroup&:
	return box.group_ref

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	group_ref: TaskGroup& = apply_getter(true, get_group_ref, get_group_ref_alias, holder)
	task_group_add(group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_branch_merged_callback_different_param_names_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
