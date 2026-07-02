package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsGenericBuilderStructFunctionFields(t *testing.T) {
	src := `struct Box[T]:
    value: T

struct Builder[T]:
    make: fn(T) -> Box[T]

def make_i64_box(value: i64) -> Box[i64]:
    return Box[i64](value)

def wrap[T](builder: Builder[T], value: T) -> Box[T]:
    return builder.make(value)

def run() -> i64:
    builder: Builder[i64] = Builder[i64](make_i64_box)
    boxed: Box[i64] = wrap(builder, 7)
    return boxed.value
`
	result, errs := parseAndAnalyze(t, "generic_builder_struct_function_field.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "wrap", "Box[T]")
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeRejectsSpecializationOfNonGenericFunction(t *testing.T) {
	src := `def id(value: int) -> int:
    return value

def run() -> int:
    return id[int](7)
`
	_, errs := parseAndAnalyze(t, "specialize_non_generic_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "function \"id\" is not generic") {
		t.Fatalf("expected non-generic specialization diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsReusingConsumedThreadHandle(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern detach(thread: Thread[i64, Joinable]) -> void

def bad(thread: Thread[i64, Joinable]) -> void:
    value: i64 = join(thread)
    _ = value
    detach(thread)
`
	_, errs := parseAndAnalyze(t, "consumed_thread_handle_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread handle \"thread\" must be moved explicitly before argument to call \"join\"") {
		t.Fatalf("expected explicit-move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsAffineThreadMovesAcrossBranches(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def move_then_join(thread: Thread[i64, Joinable]) -> i64:
    moved: Thread[i64, Joinable] = move thread
    return join(move moved)

def branch_join(cond: bool, thread: Thread[i64, Joinable]) -> i64:
    if cond:
        return join(move thread)
    return join(move thread)
`
	result, errs := parseAndAnalyze(t, "affine_thread_moves.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "move_then_join", "i64")
	requireFunctionReturnTypeString(t, result, "branch_join", "i64")
}
func TestAnalyzeAcceptsMoveAsStructDestructure(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

struct Holder:
    thread: mutable Thread[i64, Joinable]
    count: mutable i64

def run(holder: Holder) -> i64:
    move holder as Holder(thread, count)
    _ = count
    return join(move thread)
`
	result, errs := parseAndAnalyze(t, "move_as_struct_destructure.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeAcceptsMoveAsRebind(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def run(thread: Thread[i64, Joinable]) -> i64:
    move thread as worker
    return join(move worker)
`
	result, errs := parseAndAnalyze(t, "move_as_rebind.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeRejectsMoveAsStructPatternArityMismatch(t *testing.T) {
	src := `struct Pair:
    left: mutable i64
    right: mutable i64

def bad(pair: Pair) -> void:
    move pair as Pair(left)
`
	_, errs := parseAndAnalyze(t, "move_as_arity_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic errors, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "move-as pattern \"Pair\" expects 2 bindings, got 1") {
		t.Fatalf("expected move-as arity diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsMoveAsEnumVariantDestructure(t *testing.T) {
	src := `enum MaybeInt:
	None
	Pair(left: int, right: int)

def sum(value: MaybeInt) -> int:
	move value as MaybeInt.Pair(left, right)
	return left + right
`
	result, errs := parseAndAnalyze(t, "move_as_enum_variant_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "sum", "int")
}
func TestAnalyzeRejectsAwaitAfterTaskGroupTransfer(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def bad(group: mutable TaskGroup, task: Task[i64, Pending]) -> i64:
	task_group_add((&group).cast[TaskGroup&], move task)
    return await task
`
	_, errs := parseAndAnalyze(t, "consumed_task_handle_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "task handle \"task\" cannot be used: usage facts were consumed by argument to call \"task_group_add\"") {
		t.Fatalf("expected consumed-task diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsAwaitSyntax(t *testing.T) {
	src := `extern pool_await(task: Task[i64, Pending]) -> i64

def ok(task: Task[i64, Pending]) -> i64:
    return await task
`
	result, errs := parseAndAnalyze(t, "await_task_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "i64")
}
func TestAnalyzeRejectsDroppedJoinableThreadAtScopeExit(t *testing.T) {
	src := `def bad(thread: Thread[i64, Joinable]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_joinable_thread_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "joinable thread handle \"thread\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed thread diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedPendingTaskAtScopeExit(t *testing.T) {
	src := `def bad(task: Task[i64, Pending]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_pending_task_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "pending task handle \"task\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed task diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedHeldMutexGuardAtScopeExit(t *testing.T) {
	src := `def bad(guard: MutexGuard[Held]) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_held_mutex_guard_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "held mutex guard \"guard\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed held-guard diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedJoinableThreadInsideAggregateAtScopeExit(t *testing.T) {
	src := `struct Holder:
    thread: mutable Thread[i64, Joinable]

def bad(holder: Holder) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_joinable_holder_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "joinable thread handle \"holder.thread\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-thread diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedHeldMutexGuardInsideAggregateAtScopeExit(t *testing.T) {
	src := `struct Holder:
    guard: mutable MutexGuard[Held]

def bad(holder: Holder) -> void:
    pass
`
	_, errs := parseAndAnalyze(t, "drop_held_mutex_guard_holder_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "held mutex guard \"holder.guard\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-guard diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedTaskGroupWithPendingTasksAtScopeExit(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void

def bad(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	task_group_add((&group).cast[TaskGroup&], move task)
`
	_, errs := parseAndAnalyze(t, "drop_task_group_with_pending_tasks_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group with pending tasks \"group\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed task-group diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedTaskGroupInsideAggregateWithPendingTasksAtScopeExit(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void

struct Holder:
    group: mutable TaskGroup

def bad(holder: mutable Holder, task: Task[i64, Pending]) -> void:
	task_group_add((&holder.group).cast[TaskGroup&], move task)
`
	_, errs := parseAndAnalyze(t, "drop_task_group_holder_with_pending_tasks_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "task group with pending tasks \"holder.group\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-task-group diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAdd(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	task_group_add((&group).cast[TaskGroup&], move task)
    wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaBorrowedAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	group_ref: TaskGroup& = (&group).cast[TaskGroup&]
	task_group_add(group_ref, move task)
	wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaProjectedBorrowedAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def ok(group: mutable TaskGroup, task: Task[i64, Pending]) -> void:
	holder: GroupHolder = GroupHolder((&group).cast[TaskGroup&])
	task_group_add(holder.group_ref, move task)
	wait all group
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_projected_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeAcceptsWaitAllAfterTaskGroupAddViaAggregateParamAlias(t *testing.T) {
	src := `extern task_group_add(group: TaskGroup&, task: Task[i64, Pending]) -> void
extern task_group_wait_all(group: TaskGroup&) -> void

struct GroupHolder:
	group_ref: TaskGroup&

def ok(holder: GroupHolder, task: Task[i64, Pending]) -> void:
	task_group_add(holder.group_ref, move task)
	wait all holder.group_ref
`
	result, errs := parseAndAnalyze(t, "wait_all_after_task_group_add_aggregate_param_alias_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsDroppedThreadPoolRequiringShutdownAtScopeExit(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool

def bad() -> void:
	pool: ThreadPool = pool_new(2)
`
	_, errs := parseAndAnalyze(t, "drop_thread_pool_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool requiring shutdown \"pool\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed thread-pool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsDroppedThreadPoolInsideAggregateAtScopeExit(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool

struct Holder:
	pool: mutable ThreadPool

def bad(holder: mutable Holder) -> void:
	holder.pool <- pool_new(2)
`
	_, errs := parseAndAnalyze(t, "drop_thread_pool_holder_scope_exit_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool requiring shutdown \"holder.pool\" must be consumed before scope exit") {
		t.Fatalf("expected unconsumed aggregate-thread-pool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPoolShutdownAfterPoolNew(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def ok() -> void:
	pool: ThreadPool = pool_new(2)
	pool_shutdown((&pool).cast[ThreadPool&])
`
	result, errs := parseAndAnalyze(t, "pool_shutdown_after_pool_new_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "void")
}
func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdown(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2)
	pool_shutdown((&pool).cast[ThreadPool&])
	_ = pool_submit1((&pool).cast[ThreadPool&], work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected closed-thread-pool submit diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdownViaBorrowedAlias(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2)
	pool_ref: ThreadPool& = (&pool).cast[ThreadPool&]
	pool_shutdown(pool_ref)
	_ = pool_submit1((&pool).cast[ThreadPool&], work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected underlying-owner shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingBorrowedThreadPoolParamAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

def work(value: i64) -> i64:
	return value + 1

def bad(pool: ThreadPool&) -> void:
	pool_shutdown(pool)
	_ = pool_submit1(pool, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_ref_param_reuse_after_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected borrowed-param shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsSubmittingToThreadPoolAfterShutdownViaProjectedBorrowedAlias(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: mutable ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad() -> void:
	pool: ThreadPool = pool_new(2)
	holder: PoolHolder = PoolHolder((&pool).cast[ThreadPool&])
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1((&pool).cast[ThreadPool&], work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_submit_after_projected_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"pool\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected projected-alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingReassignedProjectedBorrowedThreadPoolAliasAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: mutable ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(left: ThreadPool&, right: ThreadPool&) -> void:
	holder: mutable PoolHolder = PoolHolder(left)
	holder.pool_ref <- right
	_ = pool_submit1(left, work, 1)
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1(right, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_reassigned_projected_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"right\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected reassigned projected-alias shutdown diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReusingAggregateThreadPoolParamFieldAfterShutdown(t *testing.T) {
	src := `extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

struct PoolHolder:
	pool_ref: ThreadPool&

def work(value: i64) -> i64:
	return value + 1

def bad(holder: PoolHolder) -> void:
	pool_shutdown(holder.pool_ref)
	_ = pool_submit1(holder.pool_ref, work, 1)
`
	_, errs := parseAndAnalyze(t, "thread_pool_aggregate_param_alias_shutdown_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "thread pool owner \"holder.pool_ref\" cannot be used: usage facts were consumed by argument to call \"pool_shutdown\"") {
		t.Fatalf("expected aggregate-param alias shutdown diagnostic, got:\n%s", all)
	}
}
