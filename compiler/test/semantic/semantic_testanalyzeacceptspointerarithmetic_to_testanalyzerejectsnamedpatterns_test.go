package semantic_test

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestAnalyzeAcceptsPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: u8&, offset: usize) -> u8&:
	return ptr + offset

def advance_commutative(offset: usize, ptr: u8&) -> u8&:
	return offset + ptr

def rewind(ptr: u8&, offset: usize) -> u8&:
	return ptr - offset
`
	_, errs := parseAndAnalyze(t, "pointer_arithmetic.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsManualRegions(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024)
	value: i32& = new[scratch] seed + 1
	return value[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExplicitRegionQualifiedRefs(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024)
	value: i32& @scratch = new[scratch] seed + 1
	alias: i32& @scratch = value
	return value[0] + alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_explicit_ref_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExplicitRegionParamsOnFunctions(t *testing.T) {
	src := `def id[T, region r](value: T& @r) -> T& @r:
	alias: T& @r = value
	return alias

def use(seed: i32) -> i32:
	region scratch(1024)
	value: i32& @scratch = new[scratch] seed + 1
	alias: i32& @scratch = id(value)
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "function_region_params_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExplicitRegionParamsOnExternFunctions(t *testing.T) {
	src := `extern borrow[region r](value: i32& @r) -> i32& @r

def use(seed: i32) -> i32:
	region scratch(1024)
	value: i32& @scratch = new[scratch] seed + 1
	alias: i32& @scratch = borrow(value)
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "extern_function_region_params_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsExplicitAndInferredPermissions(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def explicit() -> void:
	emit(1) can Console.Write

def inferred() -> void:
	emit(2) can Console.Write

def scoped() -> void:
	can Console.Write:
		emit(3)
`
	result, errs := parseAndAnalyze(t, "permissions_explicit_and_inferred_ok.elisa", src)
	requireNoErrors(t, errs)

	for _, name := range []string{"explicit", "inferred", "scoped"} {
		sym, ok := result.GlobalScope.Lookup(name)
		if !ok {
			t.Fatalf("expected %s symbol", name)
		}
		fn, ok := sym.Type.(*semantic.FuncType)
		if !ok {
			t.Fatalf("expected %s to have function type, got %T", name, sym.Type)
		}
		if len(fn.Permissions) != 1 || fn.Permissions[0] != "Console" {
			t.Fatalf("expected %s to infer can[Console], got %#v", name, fn.Permissions)
		}
		if len(fn.PermissionRefs) == 0 {
			t.Fatalf("expected %s to preserve permission refs, got none", name)
		}
	}
	if warns := result.Warnings(); len(warns) != 0 {
		t.Fatalf("expected no inferred-signature warnings, got:\n%s", strings.Join(warns, "\n"))
	}
}
func TestAnalyzeAcceptsBuiltinConcurrencyPermissionFamilies(t *testing.T) {
	src := `def use() -> void can[Thread.Spawn, Thread.Join, Thread.Detach, Thread.Yield, Thread.Sleep, Pool.Create, Pool.Submit, Pool.Await, Pool.WaitAll, Pool.Shutdown, Sync.Lock, Sync.Unlock, Sync.Wait, Sync.Notify, Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange, Atomics.Rmw, Atomics.Fence]:
	pass
`
	result, errs := parseAndAnalyze(t, "builtin_concurrency_permissions_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireDeclaredFunctionPermissionRefs(t, result, "use",
		"Atomics.CompareExchange",
		"Atomics.Exchange",
		"Atomics.Fence",
		"Atomics.Load",
		"Atomics.Rmw",
		"Atomics.Store",
		"Pool.Await",
		"Pool.Create",
		"Pool.Shutdown",
		"Pool.Submit",
		"Pool.WaitAll",
		"Sync.Lock",
		"Sync.Notify",
		"Sync.Unlock",
		"Sync.Wait",
		"Thread.Detach",
		"Thread.Join",
		"Thread.Sleep",
		"Thread.Spawn",
		"Thread.Yield",
	)
}
func TestAnalyzeAcceptsBuiltinConcurrencyCarrierTypes(t *testing.T) {
	src := `extern detach(thread: Thread[i64, Joinable]) -> void
extern mutex_unlock(g: MutexGuard[Held]) -> void
extern pool_await(task: Task[i64, Pending]) -> i64

def touch(thread: Thread[i64, Joinable], task: Task[i64, Pending], pool: ThreadPool, group: TaskGroup, mu: Mutex, guard: MutexGuard[Held], cv: CondVar, slot: atomic[i64]) -> void:
	_ = thread.handle
	_ = task.handle
	_ = pool.handle
	_ = group.handle
	_ = mu.handle
	_ = guard.handle
	_ = cv.handle
	copy: atomic[i64] = slot
	_ = copy
	detach(move thread)
	mutex_unlock(move guard)
	_ = pool_await(move task)
`
	result, errs := parseAndAnalyze(t, "builtin_concurrency_carriers_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeAcceptsAtomicSafePayloadTypes(t *testing.T) {
	src := `def touch(counter: atomic[i64], ready: atomic[bool], ptrs: atomic[u8&]) -> void:
	_ = counter.value
	_ = ready.value
	_ = ptrs.value
`
	result, errs := parseAndAnalyze(t, "atomic_safe_payloads_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeRejectsAtomicPayloadOfAggregateStruct(t *testing.T) {
	src := `struct Pair:
	left: i64
	right: i64

def bad(slot: atomic[Pair]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "atomic_aggregate_payload_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "atomic payload type must satisfy atomic_safe(T), got Pair") {
		t.Fatalf("expected atomic_safe aggregate diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAtomicPayloadOfAffineHandle(t *testing.T) {
	src := `def bad(slot: atomic[Thread[i64, Joinable]]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "atomic_affine_payload_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "atomic payload type must satisfy atomic_safe(T), got Thread[i64, Joinable]") {
		t.Fatalf("expected atomic_safe affine diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsMissingProtocolStateTypeArguments(t *testing.T) {
	src := `def bad(thread: Thread[i64]) -> void:
	pass
`
	_, errs := parseAndAnalyze(t, "concurrency_protocol_arity_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "type \"Thread\" expects 2 type arguments, got 1") {
		t.Fatalf("expected protocol-state arity diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsProtocolStateMismatchAtCallSite(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

def bad(thread: Thread[i64, Pending]) -> i64:
	return join(move thread)
`
	_, errs := parseAndAnalyze(t, "concurrency_protocol_state_mismatch_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument 1 to \"join\" expects Thread[i64, Joinable], got Thread[i64, Pending]") {
		t.Fatalf("expected protocol-state mismatch diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsReferencesToUserDeclaredAffineStruct(t *testing.T) {
	src := `affine struct Handle:
	raw: mutable uintptr

def bad(handle: Handle) -> void:
	borrow: Handle& = &handle
	_ = borrow
`
	_, errs := parseAndAnalyze(t, "user_affine_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot take address of linear value") {
		t.Fatalf("expected user-affine address diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsGlobalUserDeclaredAffineStruct(t *testing.T) {
	src := `affine struct Handle:
	raw: mutable uintptr

global current: Handle = zeroed
`
	_, errs := parseAndAnalyze(t, "user_affine_global_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "global \"current\" cannot store linear handle values of type Handle") {
		t.Fatalf("expected user-affine global diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeWarnsOnMissingPermissionGrant(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def bad() -> void:
	emit(1)
`
	result, errs := parseAndAnalyze(t, "permissions_missing_grant_warn.elisa", src)
	requireNoErrors(t, errs)
	warns := strings.Join(result.Warnings(), "\n")
	if warns == "" {
		t.Fatal("expected semantic warning, got none")
	}
	if !strings.Contains(warns, "call to \"emit\" requires can[Console]") {
		t.Fatalf("expected missing-permission warning, got:\n%s", warns)
	}
}
func TestAnalyzeWarnsWithUnionOfBranchMergedCallbackPermissionRefs(t *testing.T) {
	src := `def do_submit() -> void can[Pool.Submit]:
	pass

def do_wait() -> void can[Pool.WaitAll]:
	pass

def bad(flag: bool) -> void:
	local_fn: mutable func() -> void can[Pool] = do_wait
	if flag:
		local_fn <- do_submit
	else:
		local_fn <- do_wait
	local_fn()
`
	result, errs := parseAndAnalyze(t, "permissions_branch_merged_callback_union_warn.elisa", src)
	requireNoErrors(t, errs)
	warns := strings.Join(result.Warnings(), "\n")
	if warns == "" {
		t.Fatal("expected semantic warning, got none")
	}
	if !strings.Contains(warns, `call to "func" requires can[Pool]`) {
		t.Fatalf("expected branch-merged callback warning, got:\n%s", warns)
	}
	if !strings.Contains(warns, `can[Pool.Submit, Pool.WaitAll]`) {
		t.Fatalf("expected branch-merged callback warning to include both permission refs, got:\n%s", warns)
	}
}
func TestAnalyzePropagatesForwardReferencedInferredPermissionCalls(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console.Write]

def caller() -> void:
	callee()

def callee() -> void:
	emit(1) can Console.Write
`
	result, errs := parseAndAnalyze(t, "permissions_forward_reference_propagates.elisa", src)
	requireNoErrors(t, errs)
	sym, ok := result.GlobalScope.Lookup("caller")
	if !ok {
		t.Fatal("expected caller symbol")
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected caller function type, got %T", sym.Type)
	}
	if len(fn.Permissions) != 1 || fn.Permissions[0] != "Console" {
		t.Fatalf("expected caller to infer can[Console], got %#v", fn.Permissions)
	}
	if !strings.Contains(strings.Join(result.Warnings(), "\n"), "call to \"callee\" requires can[Console]") {
		t.Fatalf("expected forward-reference permission warning, got:\n%s", strings.Join(result.Warnings(), "\n"))
	}
}
func TestAnalyzeRejectsUnknownPermissionMember(t *testing.T) {
	src := `extern emit(value: int) -> void can[Console]

def bad() -> void:
	emit(1) can Console.Read
`
	_, errs := parseAndAnalyze(t, "permissions_unknown_member_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "permission \"Console\" has no member \"Read\"") {
		t.Fatalf("expected unknown-member diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeInfersBuiltinAbortPermissionFromPanic(t *testing.T) {
	src := `def fail_fast() -> void:
	panic("boom")
`
	result, errs := parseAndAnalyze(t, "builtin_abort_from_panic.elisa", src)
	requireNoErrors(t, errs)
	sym, ok := result.GlobalScope.Lookup("fail_fast")
	if !ok {
		t.Fatal("expected fail_fast symbol")
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected fail_fast function type, got %T", sym.Type)
	}
	if len(fn.Permissions) != 1 || fn.Permissions[0] != "Abort" {
		t.Fatalf("expected fail_fast to infer can[Abort], got %#v", fn.Permissions)
	}
	warns := strings.Join(result.Warnings(), "\n")
	if strings.Contains(warns, "function \"fail_fast\" infers can[Abort]") {
		t.Fatalf("expected implicit signature warning to be removed, got:\n%s", warns)
	}
	if !strings.Contains(warns, "panic requires can[Abort]") {
		t.Fatalf("expected local grant warning to remain, got:\n%s", warns)
	}
}
// The canonical `@r` suffix on a reference (docs/68 §5) is equivalent to the legacy
// region prefix `r T&`: both bind the reference to region parameter r, so the same
// program analyzes cleanly written either way.
func TestAnalyzeAcceptsRegionSuffixOnReference(t *testing.T) {
	src := `def id[region r](value: i32& @r) -> i32& @r:
	return value
`
	_, errs := parseAndAnalyze(t, "function_region_suffix_ref_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected `i32& @r` suffix to analyze like `r i32&`, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestAnalyzeRejectsCallsWhenRegionParamCannotBeInferred(t *testing.T) {
	src := `def id[region r](value: i32& @r) -> i32& @r:
	return value

def use(value: i32&) -> i32&:
	return id(value)
`
	_, errs := parseAndAnalyze(t, "function_region_params_inference_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot infer region parameter \"r\" for call to \"id\"") {
		t.Fatalf("expected region inference diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsMismatchedRegionQualifiedRefs(t *testing.T) {
	src := `def bad() -> i32:
	region left(1024)
	region right(1024)
	value: i32& @left = new[left] 1
	other: i32& @right = value
	return other[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_mismatched_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "variable \"other\" expects right i32&, got left i32&") {
		t.Fatalf("expected region-qualified mismatch diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUnknownRegionQualifiedRef(t *testing.T) {
	src := `def bad() -> void:
	value: i32&? @scratch = null
`
	_, errs := parseAndAnalyze(t, "manual_regions_unknown_ref_qualifier_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unknown region qualifier \"scratch\"") {
		t.Fatalf("expected unknown-region qualifier diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsReturningReferenceAllocatedFromLocalRegion(t *testing.T) {
	src := `def bad() -> i32&:
	region scratch(1024)
	value: i32& = new[scratch] 1
	return value
`
	_, errs := parseAndAnalyze(t, "manual_regions_return_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsReturningCastedReferenceAllocatedFromLocalRegion(t *testing.T) {
	src := `def bad() -> i32&:
	region scratch(1024)
	value: i32& = new[scratch] 1
	return value.cast[i32&]
`
	_, errs := parseAndAnalyze(t, "manual_regions_return_cast_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape return diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsRegionCheckpoints(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024)
	mark scratch as cp
	temp: i32& = new[scratch] seed + 1
	restore scratch from cp
	reused: i32& = new[scratch] seed + 2
	value: i32 = reused[0]
	reset scratch
	final: i32& = new[scratch] seed + 3
	return value + final[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_checkpoints_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsNestedRegionCheckpoints(t *testing.T) {
	src := `def sum_region(seed: i32) -> i32:
	region scratch(1024)
	base: i32& = new[scratch] seed
	baseline: i32 = base[0]
	mark scratch as outer
	stable: i32& = new[scratch] seed + 1
	mark scratch as inner
	temp: i32& = new[scratch] seed + 2
	restore scratch from inner
	kept: i32 = stable[0]
	restore scratch from outer
	final: i32& = new[scratch] seed + 3
	return baseline + kept + final[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_nested_checkpoints_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsEnumConstructorsAndMatch(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	match value:
		MaybeInt.None:
			return fallback
		MaybeInt.Some(inner):
			return inner
		MaybeInt.Pair(left, right):
			return left + right

def make_pair() -> MaybeInt:
	return MaybeInt.Pair(3, 4)

def make_none() -> MaybeInt:
	return MaybeInt.None
`
	_, errs := parseAndAnalyze(t, "enum_match_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsStatementMatchesAndNestedPatterns(t *testing.T) {
	src := `enum Inner:
	A(int)
	B

enum Outer:
	Wrap(Inner)
	Empty

def score(value: Outer) -> int:
	match value:
		Outer.Wrap(Inner.A(inner)):
			return inner
		Outer.Wrap(Inner.B):
			return 0
		Outer.Empty:
			return -1
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_match_stmt_nested_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsNamedEnumPayloadFieldsAndPatterns(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(3, 4)

def score(value: PairOrInt) -> int:
	match value:
		PairOrInt.Just(value: inner):
			return inner
		PairOrInt.Pair(right: r, left: l):
			return l + r
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_named_payloads_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsNamedEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(right: 4, left: 3)
`
	_, errs := parseAndAnalyze(t, "enum_named_ctor_args_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsNamedPatternsForUnnamedEnumPayloads(t *testing.T) {
	src := `enum MaybeInt:
	Some(int)

def unwrap(value: MaybeInt) -> int:
	match value:
		MaybeInt.Some(value: inner):
			return inner
	return 0
`
	_, errs := parseAndAnalyze(t, "enum_named_payloads_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "match arm \"MaybeInt.Some\" does not declare named payload fields") {
		t.Fatalf("expected named-payload diagnostic, got:\n%s", all)
	}
}
