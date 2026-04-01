package backend_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"llcontext/src/backend"
	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyze(t *testing.T, filename string, src string) *semantic.Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

func loadFixtureSource(t *testing.T, relParts ...string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	parts := append([]string{filepath.Dir(thisFile), "..", "..", ".."}, relParts...)
	path := filepath.Join(parts...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(raw)
}

func generateLLVMIRWithPackedABIForTest(t *testing.T, result *semantic.Result, abi backend.PackedEnumABI) string {
	t.Helper()
	output, err := backend.GenerateLLVMIRWithOptAndPackedABI(result, backend.OptimizationLevel0, abi)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedABI returned error: %v", err)
	}
	return output
}

func functionIR(output string, name string) string {
	marker := "@" + name + "("
	idx := strings.Index(output, marker)
	if idx < 0 {
		return ""
	}
	start := strings.LastIndex(output[:idx], "define ")
	if start < 0 {
		start = idx
	}
	rest := output[idx:]
	endOffset := strings.Index(rest, "\ndefine ")
	if endOffset < 0 {
		return output[start:]
	}
	return output[start : idx+endOffset]
}

func requireInstructionLineContainsAll(t *testing.T, output string, needle string, want ...string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		for _, item := range want {
			if !strings.Contains(line, item) {
				t.Fatalf("expected line containing %q to also contain %q, got:\n%s\n\nFull IR:\n%s", needle, item, line, output)
			}
		}
		return
	}
	t.Fatalf("expected instruction line containing %q, got:\n%s", needle, output)
}

func requireTinyExactDViewCopyBody(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "call ptr @arena_memcpy(") {
		t.Fatalf("expected tiny exact dview copy to avoid arena_memcpy, got:\n%s", body)
	}
	requireInstructionLineContainsAll(t, body, "load i32, ptr %dview.copy.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, body, "store i32 %dview.copy.elem, ptr %dview.copy.dst.elem.ptr", "!alias.scope", "!noalias")
}

func requireTinyExactDViewEqBody(t *testing.T, body string, expectAliasMetadata bool) {
	t.Helper()
	if strings.Contains(body, "call i64 @memcmp(") || strings.Contains(body, "call i32 @memcmp(") {
		t.Fatalf("expected tiny exact dview equality to avoid memcmp, got:\n%s", body)
	}
	if !strings.Contains(body, "dview.eq.byte.eq = icmp eq i8") {
		t.Fatalf("expected tiny exact dview equality to compare bytes directly, got:\n%s", body)
	}
	if expectAliasMetadata {
		requireInstructionLineContainsAll(t, body, "dview.eq.left.byte = load i8", "!alias.scope", "!noalias")
		requireInstructionLineContainsAll(t, body, "dview.eq.right.byte = load i8", "!alias.scope", "!noalias")
	}
}

func requireTinyExactDViewMaterializeBody(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "call ptr @arena_alloc(") {
		t.Fatalf("expected tiny exact arena_da_from_view to still allocate the destination buffer, got:\n%s", body)
	}
	if strings.Contains(body, "call ptr @arena_memcpy(") {
		t.Fatalf("expected tiny exact arena_da_from_view to avoid arena_memcpy, got:\n%s", body)
	}
	requireInstructionLineContainsAll(t, body, "load i32, ptr %dview.materialize.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, body, "store i32 %dview.materialize.elem, ptr %dview.materialize.dst.elem.ptr", "!alias.scope", "!noalias")
	if !strings.Contains(body, "dview.materialize.items") {
		t.Fatalf("expected tiny exact arena_da_from_view to still materialize the darray result, got:\n%s", body)
	}
}

func TestGenerateLLVMIRDefinesSimpleFunctionBody(t *testing.T) {
	src := `repr(c) struct Box:
    value: i32

extern alloc_box() -> any Box&
extern take_box(box: Box) -> void
extern errno_value: i32

def read_box(box: any Box&) -> i32:
    return box.value
`
	result := parseAndAnalyze(t, "backend_box.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Box = type { i32 }",
		"@errno_value = external global i32",
		"declare ptr @alloc_box()",
		"declare void @take_box(%Box)",
		"define i32 @read_box(ptr",
		"getelementptr inbounds",
		"%Box, ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedStructLiterals(t *testing.T) {
	src := `repr(c) struct ScratchPair:
	left: mutable int
	right: mutable int


repr(c) struct ScratchHolder:
	pair: mutable ScratchPair


def make_holder() -> ScratchHolder:
		return ScratchHolder(ScratchPair(8, 9))
`
	result := parseAndAnalyze(t, "backend_nested_struct_literals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ScratchPair = type { i64, i64 }",
		"%ScratchHolder = type { %ScratchPair }",
		"define %ScratchHolder @make_holder()",
		"ret %ScratchHolder { %ScratchPair { i64 8, i64 9 } }",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRErasesAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?]:
    value: i32

def read(value: Holder[&]) -> i32:
    return value.value
`
	result := parseAndAnalyze(t, "backend_aggregate_state_struct.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Holder = type { i32 }",
		"define i32 @read(%Holder",
		"getelementptr inbounds nuw %Holder",
		"load i32, ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "Holder____") {
		t.Fatalf("expected aggregate state wrapper to erase in LLVM lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRErasesMultiAggregateStateStructTypes(t *testing.T) {
	src := `struct Holder[?, ?]:
    value: i32

def read(value: Holder[!, &]) -> i32:
    return value.value
`
	result := parseAndAnalyze(t, "backend_aggregate_state_struct_multi.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Holder = type { i32 }",
		"define i32 @read(%Holder",
		"getelementptr inbounds nuw %Holder",
		"load i32, ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "Holder____") {
		t.Fatalf("expected aggregate state wrapper to erase in LLVM lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersMoveAsStructDestructure(t *testing.T) {
	src := `repr(c) struct Pair:
    left: mutable i64
    right: mutable i64

def sum(pair: Pair) -> i64:
    move pair as Pair(left, right)
    return left + right
`
	result := parseAndAnalyze(t, "backend_move_as_struct.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i64, i64 }",
		"define i64 @sum(%Pair",
		"extractvalue %Pair",
		"store i64",
		"load i64",
		"add i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedMoveAsVariantDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left(node: Expr, store: Expr.Store[Local]) -> Expr:
	move node in store as Expr.Add(lhs, rhs)
	_ = rhs
	return lhs
`
	result := parseAndAnalyze(t, "backend_move_as_packed_variant.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Expr__Store = type { ptr, i64, ptr }",
		"%Expr = type { i32, [2 x i64] }",
		"define ptr @left(ptr",
		"load i32, ptr",
		"call void @llvm.trap()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed move-as destructure to decode through store-backed loads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedMoveAsNestedVariantDestructure(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def left_value(node: Expr, store: Expr.Store[Local]) -> int:
	move node in store as Expr.Add(Expr.Int(value), rhs)
	_ = rhs
	return value
`
	result := parseAndAnalyze(t, "backend_move_as_packed_nested_variant.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	for _, check := range []string{"%Expr__Store = type { ptr, i64, ptr }", "%Expr = type { i32, [2 x i64] }", "define i64 @left_value(", "call void @llvm.trap()", "load i32, ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "load i32, ptr") < 2 {
		t.Fatalf("expected nested packed variant pattern lowering to load at least two tags, got:\n%s", output)
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected nested packed move-as destructure to decode through store-backed loads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersLocalsCallsAndControlFlow(t *testing.T) {
	src := `extern add_one(value: i32) -> i32

def countdown(start: i32) -> i32:
    value: mutable i32 = start
    total: mutable i32 = 0
    while value > 0:
        if value == 2:
            total <- add_one(total)
        else:
            total <- total + 1
        value <- value - 1
    return total
`
	result := parseAndAnalyze(t, "backend_control_flow.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i32 @add_one(i32)",
		"define i32 @countdown(i32",
		"call i32 @add_one(i32",
		"icmp sgt i32",
		"icmp eq i32",
		"br i1",
		"store i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersLockScopeCleanupOnReturnAndFallthrough(t *testing.T) {
	src := `extern mutex_lock(mu: any Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void

def lock_then_return(mu: mutable Mutex) -> i64:
    lock mu as g:
        return 7

def lock_then_fallthrough(mu: mutable Mutex) -> void:
    lock mu as g:
        pass
`
	result := parseAndAnalyze(t, "backend_lock_scope.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %MutexGuard__Held @mutex_lock(ptr)",
		"declare void @mutex_unlock(%MutexGuard__Held)",
		"define i64 @lock_then_return(%Mutex",
		"define void @lock_then_fallthrough(%Mutex",
		"call %MutexGuard__Held @mutex_lock(ptr",
		"call void @mutex_unlock(%MutexGuard__Held",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	returnIR := functionIR(output, "lock_then_return")
	if unlockIdx := strings.Index(returnIR, "call void @mutex_unlock(%MutexGuard__Held"); unlockIdx < 0 || unlockIdx > strings.Index(returnIR, "ret i64 7") {
		t.Fatalf("expected lock_then_return to unlock before returning, got:\n%s", returnIR)
	}
	fallthroughIR := functionIR(output, "lock_then_fallthrough")
	if unlockIdx := strings.Index(fallthroughIR, "call void @mutex_unlock(%MutexGuard__Held"); unlockIdx < 0 || unlockIdx > strings.LastIndex(fallthroughIR, "ret void") {
		t.Fatalf("expected lock_then_fallthrough to unlock before fallthrough return, got:\n%s", fallthroughIR)
	}
}

func TestGenerateLLVMIRLowersPoolScopeCleanupInReverseNestingOrder(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void
extern mutex_lock(mu: any Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void

def pool_then_return(mu: mutable Mutex) -> i64:
	pool workers(4u):
		lock mu as g:
			return 7

def pool_then_fallthrough(mu: mutable Mutex) -> void:
	pool workers(2u):
		lock mu as g:
			pass
`
	result := parseAndAnalyze(t, "backend_pool_scope.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %ThreadPool @pool_new(",
		"declare void @pool_shutdown(ptr)",
		"declare %MutexGuard__Held @mutex_lock(ptr)",
		"declare void @mutex_unlock(%MutexGuard__Held)",
		"define i64 @pool_then_return(%Mutex",
		"define void @pool_then_fallthrough(%Mutex",
		"call %ThreadPool @pool_new(",
		"call void @pool_shutdown(ptr",
		"call void @mutex_unlock(%MutexGuard__Held",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	returnIR := functionIR(output, "pool_then_return")
	unlockIdx := strings.Index(returnIR, "call void @mutex_unlock(%MutexGuard__Held")
	shutdownIdx := strings.Index(returnIR, "call void @pool_shutdown(ptr")
	retIdx := strings.Index(returnIR, "ret i64 7")
	if unlockIdx < 0 || shutdownIdx < 0 || retIdx < 0 || unlockIdx > shutdownIdx || shutdownIdx > retIdx {
		t.Fatalf("expected pool_then_return to unlock, then shutdown the pool, then return, got:\n%s", returnIR)
	}
	fallthroughIR := functionIR(output, "pool_then_fallthrough")
	unlockIdx = strings.Index(fallthroughIR, "call void @mutex_unlock(%MutexGuard__Held")
	shutdownIdx = strings.Index(fallthroughIR, "call void @pool_shutdown(ptr")
	retIdx = strings.LastIndex(fallthroughIR, "ret void")
	if unlockIdx < 0 || shutdownIdx < 0 || retIdx < 0 || unlockIdx > shutdownIdx || shutdownIdx > retIdx {
		t.Fatalf("expected pool_then_fallthrough to unlock, then shutdown the pool, then return, got:\n%s", fallthroughIR)
	}
}

func TestGenerateLLVMIRLowersNotifySyntax(t *testing.T) {
	src := `extern notify_one(cv: any CondVar&) -> void
extern notify_all(cv: any CondVar&) -> void

def wake(cv: mutable CondVar, broadcast: bool) -> void:
	if broadcast:
		notify all cv
	else:
		notify one cv
`
	result := parseAndAnalyze(t, "backend_notify_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare void @notify_one(ptr)",
		"declare void @notify_all(ptr)",
		"define void @wake(%CondVar",
		"call void @notify_one(ptr",
		"call void @notify_all(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersAtomicRmwCalls(t *testing.T) {
	src := `enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

extern fetch_add(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_sub(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_or(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_and(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]
extern fetch_xor(slot: any atomic[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Rmw]

def bump(slot: mutable atomic[i64]) -> i64 can[Atomics.Rmw]:
	slot_ref: any atomic[i64]& = (&slot).cast[any atomic[i64]&]
	add: i64 = fetch_add(slot_ref, 1, MemoryOrder.AcqRel)
	sub: i64 = fetch_sub(slot_ref, 2, MemoryOrder.AcqRel)
	or_bits: i64 = fetch_or(slot_ref, 4, MemoryOrder.AcqRel)
	and_bits: i64 = fetch_and(slot_ref, 8, MemoryOrder.AcqRel)
	xor_bits: i64 = fetch_xor(slot_ref, 16, MemoryOrder.AcqRel)
	return add + sub + or_bits + and_bits + xor_bits
`
	result := parseAndAnalyze(t, "backend_atomic_rmw.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @fetch_add(ptr, i64, i32)",
		"declare i64 @fetch_sub(ptr, i64, i32)",
		"declare i64 @fetch_or(ptr, i64, i32)",
		"declare i64 @fetch_and(ptr, i64, i32)",
		"declare i64 @fetch_xor(ptr, i64, i32)",
		"define i64 @bump(%atomic__i64",
		"call i64 @fetch_add(ptr",
		"call i64 @fetch_sub(ptr",
		"call i64 @fetch_or(ptr",
		"call i64 @fetch_and(ptr",
		"call i64 @fetch_xor(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersSubmitSyntaxInsidePoolScope(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: any ThreadPool&) -> void

def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

extern pool_await(task: Task[i64, Pending]) -> i64

def work(value: i64) -> i64:
	return value + 1

def submit_then_await() -> i64:
	pool workers(2u):
		task: Task[i64, Pending] = submit work(7)
		return await task
`
	result := parseAndAnalyze(t, "backend_submit_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %ThreadPool @pool_new(",
		"declare void @pool_shutdown(ptr)",
		"define i64 @submit_then_await()",
		"call %Task__i64__Pending @pool_submit1(ptr",
		"call i64 @pool_await(%Task__i64__Pending",
		"call void @pool_shutdown(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	body := functionIR(output, "submit_then_await")
	if submitIdx := strings.Index(body, "call %Task__i64__Pending @pool_submit1(ptr"); submitIdx < 0 {
		t.Fatalf("expected submit_then_await to lower submit syntax through pool_submit1, got:\n%s", body)
	}
}

func TestGenerateLLVMIRLowersExplicitSubmitSyntax(t *testing.T) {
	src := `def pool_submit1(pool: any ThreadPool&, fn: func(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

extern pool_await(task: Task[i64, Pending]) -> i64

def work(value: i64) -> i64:
	return value + 1

def submit_then_await(pool: any ThreadPool&) -> i64:
	task: Task[i64, Pending] = submit[pool] work(7)
	return await task
`
	result := parseAndAnalyze(t, "backend_submit_explicit_pool_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @submit_then_await(ptr",
		"call %Task__i64__Pending @pool_submit1(ptr",
		"call i64 @pool_await(%Task__i64__Pending",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	body := functionIR(output, "submit_then_await")
	if submitIdx := strings.Index(body, "call %Task__i64__Pending @pool_submit1(ptr"); submitIdx < 0 {
		t.Fatalf("expected submit_then_await to lower explicit submit syntax through pool_submit1, got:\n%s", body)
	}
}

func TestGenerateLLVMIRLowersVoidReturnCalls(t *testing.T) {
	src := `extern touch(value: i32)

def call_touch(value: i32) -> i32:
	touch(value)
	return value
`
	result := parseAndAnalyze(t, "backend_void_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare void @touch(i32)",
		"define i32 @call_touch(i32",
		"call void @touch(i32",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersHigherOrderFunctionCalls(t *testing.T) {
	src := `def apply_twice(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(fn(value))

def inc(value: i64) -> i64:
    return value + 1

def run() -> i64:
    return apply_twice(inc, 40)
`
	result := parseAndAnalyze(t, "backend_higher_order_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @apply_twice(ptr",
		"call i64 %",
		"define i64 @inc(i64",
		"define i64 @run()",
		"call i64 @apply_twice(ptr @inc, i64 40)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "cannot call non-function") {
		t.Fatalf("expected function-typed parameter lowering, got:\n%s", output)
	}
	if count := strings.Count(output, "call i64 %"); count < 2 {
		t.Fatalf("expected at least two indirect calls through the function parameter, got %d:\n%s", count, output)
	}
}

func TestGenerateLLVMIRLowersFunctionValueErasureCasts(t *testing.T) {
	src := `def inc(value: i64) -> i64:
    return value + 1

def call_bits(bits: uintptr, value: i64) -> i64:
	fn: func(i64) -> i64 = bits.cast[func(i64) -> i64]
    return fn(value)

def run() -> i64:
	bits: uintptr = inc.cast[uintptr]
    return call_bits(bits, 41)
`
	result := parseAndAnalyze(t, "backend_function_value_erasure_casts.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @call_bits(i64",
		"inttoptr i64",
		"call i64 %",
		"ptrtoint (ptr @inc to i64)",
		"call i64 @call_bits(i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExplicitGenericFunctionSpecializationValues(t *testing.T) {
	src := `def id[T](value: T) -> T:
    return value

def apply_i64(fn: func(i64) -> i64, value: i64) -> i64:
    return fn(value)

def run() -> i64:
    fn: func(i64) -> i64 = id.specialize[i64]()
    return apply_i64(fn, 7)
`
	result := parseAndAnalyze(t, "backend_explicit_generic_function_specialization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @id__i64(i64",
		"define i64 @apply_i64(ptr",
		"store ptr @id__i64",
		"call i64 @apply_i64(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPanicViaBacktraceAwareAbort(t *testing.T) {
	src := `def fail() -> void:
	panic("boom")
`
	result := parseAndAnalyze(t, "backend_panic_stmt.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define void @fail()",
		"declare i64 @printf(ptr, ...)",
		"declare i64 @backtrace(ptr, i64)",
		"declare void @backtrace_symbols_fd(ptr, i64, i64)",
		"declare void @abort()",
		"call i64 (ptr, ...) @printf(",
		"call i64 @backtrace(ptr",
		"call void @backtrace_symbols_fd(ptr",
		"call void @abort()",
		"unreachable",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBuiltinStringAndSViewSyntax(t *testing.T) {
	src := `def first_char(text: str[4]) -> char:
	return text[1]

def first_code(text: str[4]) -> i64:
	return text[1].i64()

def slice_text(text: str[4]) -> sview[1, 3]:
    return text[1:3]

def view_char(text: sview[0, 4]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_builtin_string_surface.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i64 @first_char([4 x i8]",
		"define i64 @first_code([4 x i8]",
		"zext i8",
		"define %StringView @slice_text([4 x i8]",
		"insertvalue %StringView",
		"define i64 @view_char(%StringView",
		"declare i64 @ctx_string_view_index(%StringView, i64)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@ctx_string_view(ptr") {
		t.Fatalf("expected fixed string slice lowering to avoid runtime string view helper, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersEscapedStringLiteralBytes(t *testing.T) {
	src := `def newline_text() -> any u8&:
	return "line\nbreak".cast[any u8&]

def quoted_text() -> any u8&:
	return "quote: \" slash: \\ hex: \x41".cast[any u8&]

def unicode_text() -> any u8&:
	return "\u263A".cast[any u8&]
`
	result := parseAndAnalyze(t, "backend_string_escapes.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @newline_text()",
		"define ptr @quoted_text()",
		"define ptr @unicode_text()",
		"c\"line\\0Abreak\\00\"",
		"c\"quote: \\22 slash: \\\\ hex: A\\00\"",
		"c\"\\E2\\98\\BA\\00\"",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersStandaloneCharValues(t *testing.T) {
	src := `def normalize(code: i64) -> char:
	ch: char = code.char()
	if ch == 0.char():
		return 65.char()
	return ch

def bump(ch: char) -> i64:
	return (ch + 1).i64()
`
	result := parseAndAnalyze(t, "backend_standalone_char_values.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @normalize(i64",
		"icmp eq i64",
		"ret i64 65",
		"define i64 @bump(i64",
		"add i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersReferenceComparisons(t *testing.T) {
	src := `repr(c) struct Box:
    value: i32

extern maybe_box() -> any Box&?

def is_missing() -> bool:
    return maybe_box() == null

def is_present() -> bool:
    return maybe_box() != null

def same_box(left: any Box&, right: any Box&) -> bool:
    return left == right
`
	result := parseAndAnalyze(t, "backend_reference_comparisons.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare ptr @maybe_box()",
		"define i1 @is_missing()",
		"define i1 @is_present()",
		"define i1 @same_box(ptr",
		"icmp eq ptr",
		"icmp ne ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRTernaryUsesPhi(t *testing.T) {
	src := `def choose(flag: bool, left: i32, right: i32) -> i32:
    return left if flag else right
`
	result := parseAndAnalyze(t, "backend_ternary.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @choose(i1",
		"phi i32",
		"br i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRDefinesGlobalsWithInitializers(t *testing.T) {
	src := `repr(c) struct Pair:
    left: i32
    right: i32

const ANSWER = 42

global seed: i32 = ANSWER
global offset: i32 = ANSWER + 8
global choice: i32 = 7 if ANSWER > 0 else 9
global negated: i32 = -(ANSWER / 21)
global pair: Pair = Pair(ANSWER - 41, 1 + 1)
global flags: i32[4] = zeroed
`
	result := parseAndAnalyze(t, "backend_globals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i32, i32 }",
		"@seed = global i32 42",
		"@offset = global i32 50",
		"@choice = global i32 7",
		"@negated = global i32 -2",
		"@pair = global %Pair { i32 1, i32 2 }",
		"@flags = global [4 x i32] zeroinitializer",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRAllowsAggregateGlobalReferencesInInitializers(t *testing.T) {
	src := `repr(c) struct Pair:
	left: i32
	right: i32

repr(c) struct Holder:
	pair: Pair

global base: Pair = Pair(1, 2)
global table: Pair[2] = [base, Pair(3, 4)]
global picked: Pair = table[1u]
global wrapped: Holder = Holder(table[0u])
global first_left: i32 = table[0u].left
`
	result := parseAndAnalyze(t, "backend_global_aggregate_refs.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Pair = type { i32, i32 }",
		"%Holder = type { %Pair }",
		"@base = global %Pair { i32 1, i32 2 }",
		"@table = global [2 x %Pair]",
		"%Pair { i32 1, i32 2 }",
		"%Pair { i32 3, i32 4 }",
		"@picked = global %Pair { i32 3, i32 4 }",
		"@wrapped = global %Holder { %Pair { i32 1, i32 2 } }",
		"@first_left = global i32 1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesGenericFunctions(t *testing.T) {
	src := `def identity[T](value: T) -> T:
    return value

def use_identity(value: i32) -> i32:
    return identity(value)
`
	result := parseAndAnalyze(t, "backend_generic_specialization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @use_identity(i32",
		"define i32 @identity__i32(i32",
		"call i32 @identity__i32(i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesRefQualifierGenericFunctions(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32

struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

def id_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

def use_handle(value: Handle[heap, &]) -> heap Node&:
	kept: Handle[heap, &] = id_handle(value)
	return kept.ptr
`
	result := parseAndAnalyze(t, "backend_ref_qualifier_specialization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Handle__heap__anon = type { ptr }",
		"define %Handle__heap__anon @id_handle__heap__anon(%Handle__heap__anon",
		"define ptr @use_handle(%Handle__heap__anon",
		"call %Handle__heap__anon @id_handle__heap__anon(%Handle__heap__anon",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExportWrappers(t *testing.T) {
	src := `struct Vec[T]:
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export func vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_wrappers.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Vec__i32 = type { i32, i32 }",
		"@seed = global i32 7",
		"@ctx_seed = alias i32, ptr @seed",
		"define %Vec__i32 @vec_add_i32(%Vec__i32",
		"define %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
		"define i64 @vec2i_add(i64",
		"define i64 @vec2i_keep_left(i64",
		"call %Vec__i32 @vec_add_i32(%Vec__i32",
		"call %Vec__i32 @keep_left__Vec_i32(%Vec__i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateCHeaderForExportedVec2i(t *testing.T) {
	src := `struct Vec[T]:
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export func vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export func vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_header.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"#ifndef BACKEND_EXPORT_HEADER_H",
		"#include <stdint.h>",
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
		"Vec2i vec2i_keep_left(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
	if strings.Contains(header, "Vec__i32 vec2i_add") {
		t.Fatalf("expected public header not to leak backend mangled aggregate names, got:\n%s", header)
	}
}

func TestGenerateCHeaderForConcreteRefQualifierExports(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32

repr(c) struct Handle[refstorage Store, refstate State]:
	ptr: Store Node&[State]

export type Node as CtxNode
export type Handle[heap, &] as HeapHandle

def keep_handle[refstorage Store, refstate State](value: Handle[Store, State]) -> Handle[Store, State]:
	return value

export func keep_heap_handle(value: HeapHandle) -> HeapHandle = keep_handle[heap, &]
`
	result := parseAndAnalyze(t, "backend_ref_qualifier_export_header.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"typedef struct CtxNode CtxNode;",
		"typedef struct HeapHandle HeapHandle;",
		"struct CtxNode {",
		"struct HeapHandle {",
		"CtxNode *ptr;",
		"HeapHandle keep_heap_handle(HeapHandle arg0);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
	if strings.Contains(header, "Handle__heap__anon") {
		t.Fatalf("expected public header not to leak qualifier-specialized backend names, got:\n%s", header)
	}
}

func TestGenerateLLVMIRLowersFloatArithmeticComparisonsAndCasts(t *testing.T) {
	src := `global tau: f64 = 6.25

def mix(left: f32, right: f64) -> f64:
	total: f64 = left.f64() + right
	return total * tau

def negate(value: f64) -> f64:
	return -value

def same(left: f64, right: f64) -> bool:
	return left == right

def widen_bits(value: i32) -> f64:
	return value.f64()

def narrow(value: f64) -> f32:
	return value.f32()
`
	result := parseAndAnalyze(t, "backend_float_ops.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@tau = global double",
		"define double @mix(float",
		"fpext float",
		"fadd double",
		"fmul double",
		"define double @negate(double",
		"fneg double",
		"define i1 @same(double",
		"fcmp oeq double",
		"define double @widen_bits(i32",
		"sitofp i32",
		"define float @narrow(double",
		"fptrunc double",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateCHeaderUsesFloatBuiltinMappings(t *testing.T) {
	src := `repr(c) struct Metrics:
	ratio: f32
	total: f64

export type Metrics as MetricsFFI

global tau: f64 = 6.25
export global tau as ctx_tau

def scale_sum_impl(left: f32, right: f64) -> f64:
	return left.f64() + right

export func scale_sum(left: f32, right: f64) -> f64 = scale_sum_impl
`
	result := parseAndAnalyze(t, "backend_float_header.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"typedef struct MetricsFFI MetricsFFI;",
		"struct MetricsFFI {",
		"float ratio;",
		"double total;",
		"extern double ctx_tau;",
		"double scale_sum(float arg0, double arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
}

func TestGenerateLLVMIRLowersConstFloatCastsForGlobals(t *testing.T) {
	src := `const SMALL: i32 = 3.75.i32()
const RATIO32: f32 = 1.5.f32()
const WIDE64: f64 = 7.i32().f64()

global g_small: i32 = SMALL
global g_ratio32: f32 = RATIO32
global g_wide64: f64 = WIDE64
`
	result := parseAndAnalyze(t, "backend_const_float_cast_globals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@g_small = global i32 3",
		"@g_ratio32 = global float",
		"@g_wide64 = global double 7.000000e+00",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"@g_small = global i32 3.750000e+00",
		"@g_wide64 = global double 3.750000e+00",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected output to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersExtendedFloatCastMatrix(t *testing.T) {
	src := `def f64_to_i64(value: f64) -> i64:
	return value.i64()

def f64_to_u32(value: f64) -> u32:
	return value.u32()

def f64_to_u64(value: f64) -> u64:
	return value.u64()

def f32_to_f64(value: f32) -> f64:
	return value.f64()

def i64_to_f64(value: i64) -> f64:
	return value.f64()

def u32_to_f32(value: u32) -> f32:
	return value.f32()

def u64_to_f64(value: u64) -> f64:
	return value.f64()
`
	result := parseAndAnalyze(t, "backend_float_cast_matrix.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @f64_to_i64(double",
		"fptosi double",
		"define i32 @f64_to_u32(double",
		"fptoui double",
		"define i64 @f64_to_u64(double",
		"define double @f32_to_f64(float",
		"fpext float",
		"define double @i64_to_f64(i64",
		"sitofp i64",
		"define float @u32_to_f32(i32",
		"uitofp i32",
		"define double @u64_to_f64(i64",
		"uitofp i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if got := strings.Count(output, "fptoui double"); got < 2 {
		t.Fatalf("expected at least two unsigned float-to-int casts, got %d:\n%s", got, output)
	}
}

func TestGenerateLLVMIRLowersExtendedConstFloatCastMatrixForGlobals(t *testing.T) {
	src := `const I64_FROM_F64: i64 = 8.75.i64()
const U32_FROM_F64: u32 = 5.5.u32()
const U64_FROM_F64: u64 = 6.5.u64()
const F64_FROM_U32: f64 = 7.i32().u32().f64()
const F32_FROM_U64: f32 = 9.i32().u64().f32()

global g_i64: i64 = I64_FROM_F64
global g_u32: u32 = U32_FROM_F64
global g_u64: u64 = U64_FROM_F64
global g_f64: f64 = F64_FROM_U32
global g_f32: f32 = F32_FROM_U64
`
	result := parseAndAnalyze(t, "backend_const_float_cast_matrix_globals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@g_i64 = global i64 8",
		"@g_u32 = global i32 5",
		"@g_u64 = global i64 6",
		"@g_f64 = global double 7.000000e+00",
		"@g_f32 = global float 9.000000e+00",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"@g_u32 = global i32 5.500000e+00",
		"@g_u64 = global i64 6.500000e+00",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected output to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersContextualFloatLiteralSitesAndGlobals(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

repr(c) struct FloatPair:
	left: f32
	right: f32

global g_ratio: f32 = 1.25
global g_pair: FloatPair = FloatPair(2.5, 3.5)
global g_values: f32[2] = [4.5, 5.5]

def choose(flag: bool) -> f32:
	return (6.5 if flag else 7.5)

def local_and_call() -> f32:
	local: f32 = 8.5
	return passthrough(local) + passthrough(9.5)
`
	result := parseAndAnalyze(t, "backend_contextual_float_literals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FloatPair = type { float, float }",
		"@g_ratio = global float",
		"@g_pair = global %FloatPair",
		"@g_values = global [2 x float]",
		"declare float @passthrough(float)",
		"define float @choose(i1",
		"define float @local_and_call()",
		"call float @passthrough(float",
		"fadd float",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	chooseBody := functionIR(output, "choose")
	if chooseBody == "" {
		t.Fatalf("expected to find choose body, got:\n%s", output)
	}
	if strings.Contains(chooseBody, "fptrunc double") {
		t.Fatalf("expected choose to avoid redundant double-to-float truncation, got:\n%s", chooseBody)
	}

	localBody := functionIR(output, "local_and_call")
	if localBody == "" {
		t.Fatalf("expected to find local_and_call body, got:\n%s", output)
	}
	if strings.Contains(localBody, "fptrunc double") {
		t.Fatalf("expected local_and_call to avoid redundant double-to-float truncation, got:\n%s", localBody)
	}
}

func TestGenerateLLVMIRLowersContextualFloatLiteralArithmeticSites(t *testing.T) {
	src := `extern passthrough(value: f32) -> f32

def choose(flag: bool) -> f32:
	return ((1.25 + 2.25) if flag else (3.25 + 4.25))

def local_and_call() -> f32:
	local: f32 = 5.25 + 6.25
	return passthrough(local) + passthrough(7.25 + 8.25)
`
	result := parseAndAnalyze(t, "backend_contextual_float_literal_arithmetic.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare float @passthrough(float)",
		"define float @choose(i1",
		"define float @local_and_call()",
		"fadd float",
		"call float @passthrough(float",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	for _, name := range []string{"choose", "local_and_call"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		if strings.Contains(body, "fptrunc double") {
			t.Fatalf("expected %s to avoid redundant double-to-float truncation, got:\n%s", name, body)
		}
		if strings.Contains(body, "fadd double") {
			t.Fatalf("expected %s to stay in float arithmetic, got:\n%s", name, body)
		}
	}
}

func TestGenerateLLVMIRLowersContextualFloatLiteralArithmeticTopLevelSites(t *testing.T) {
	src := `repr(c) struct FloatPair:
	left: f32
	right: f32

const F32_TOTAL: f32 = 1.25 + 2.25
const F64_TOTAL: f64 = 3.25 + 4.25

global g_f32: f32 = 5.25 + 6.25
global g_f64: f64 = 7.25 + 8.25
global g_pair: FloatPair = FloatPair(9.25 + 10.25, 11.25 + 12.25)
global g_values: f32[2] = [13.25 + 14.25, 15.25 + 16.25]

def total() -> f64:
	return F32_TOTAL.f64() + F64_TOTAL + g_f32.f64() + g_f64
`
	result := parseAndAnalyze(t, "backend_contextual_float_literal_arithmetic_toplevel.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FloatPair = type { float, float }",
		"@g_f32 = global float 1.150000e+01",
		"@g_f64 = global double 1.550000e+01",
		"@g_pair = global %FloatPair { float 1.950000e+01, float 2.350000e+01 }",
		"@g_values = global [2 x float] [float 2.750000e+01, float 3.150000e+01]",
		"define double @total()",
		"fadd double",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	totalBody := functionIR(output, "total")
	if totalBody == "" {
		t.Fatalf("expected to find total body, got:\n%s", output)
	}
	if strings.Contains(totalBody, "fptrunc double") {
		t.Fatalf("expected total to avoid redundant double-to-float truncation, got:\n%s", totalBody)
	}
}

func TestGenerateCHeaderOrdersAggregateDefinitionsByValueDependencies(t *testing.T) {
	src := `repr(c) struct Node:
	value: mutable i32
	next: mutable any Node&?

repr(c) struct Wrapper:
	node: mutable Node
	next_ref: mutable any Node&?

export type Wrapper as CtxWrapper
export type Node as CtxNode

global root: Wrapper = zeroed
export global root as ctx_root
`
	result := parseAndAnalyze(t, "backend_export_header_order.llcontext", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	nodeIndex := strings.Index(header, "struct CtxNode {")
	wrapperIndex := strings.Index(header, "struct CtxWrapper {")
	if nodeIndex == -1 || wrapperIndex == -1 {
		t.Fatalf("expected both exported structs in header, got:\n%s", header)
	}
	if nodeIndex > wrapperIndex {
		t.Fatalf("expected Node definition before Wrapper definition, got:\n%s", header)
	}
	if !strings.Contains(header, "CtxNode *next;") {
		t.Fatalf("expected pointer field to use forward-declared public name, got:\n%s", header)
	}
	if !strings.Contains(header, "extern CtxWrapper ctx_root;") {
		t.Fatalf("expected exported global declaration, got:\n%s", header)
	}
}

func TestGenerateLLVMIRLowersVariadicExternCalls(t *testing.T) {
	src := `extern snprintf(buffer: any u8&?, buffer_size: usize, format: any u8&, ...) -> int

def format_len(format: any u8&) -> int:
	return snprintf(null, 0u, format, 7, 9)
`
	result := parseAndAnalyze(t, "backend_variadic_call.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i64 @format_len(ptr",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"ptr null, i64 0",
		"i64 7, i64 9)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPointerIntegerCasts(t *testing.T) {
	src := `def ptr_bits(ptr: any u8&) -> uintptr:
	return ptr.uintptr()

def bits_ptr(bits: uintptr) -> any u8&:
	return bits.cast[any u8&]
`
	result := parseAndAnalyze(t, "backend_pointer_integer_casts.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @ptr_bits(ptr",
		"ptrtoint ptr",
		"define ptr @bits_ptr(i64",
		"inttoptr i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedFieldAccessOnReturnedStructValues(t *testing.T) {
	src := `repr(c) struct Inner:
	value: i32

repr(c) struct Outer:
	inner: Inner

extern make_outer() -> Outer

def read_nested_return() -> i32:
	return make_outer().inner.value
`
	result := parseAndAnalyze(t, "backend_nested_return_field_chain.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Inner = type { i32 }",
		"%Outer = type { %Inner }",
		"declare %Outer @make_outer()",
		"call %Outer @make_outer()",
		"extractvalue %Outer",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowerRuntimeBackedTypes(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
    count: mutable usize

extern take_array(values: darray[i32, row]) -> void
extern take_array_view(view: dview[i32]) -> usize
extern take_str(text: dstr[row]) -> void
`
	result := parseAndAnalyze(t, "backend_runtime_types.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"declare void @take_array(%DynArray__i32)",
		"declare i64 @take_array_view(%DynArrayView)",
		"declare void @take_str(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRAcceptsShapeErasingDArrayShorthand(t *testing.T) {
	src := `def keep(values: darray[i32]) -> darray[i32]:
    return values

def erase(values: darray[i32, row]) -> darray[i32]:
    return values
`
	result := parseAndAnalyze(t, "backend_darray_shorthand.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"define %DynArray__i32 @keep(%DynArray__i32",
		"define %DynArray__i32 @erase(%DynArray__i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestWriteLLVMBitcodeFile(t *testing.T) {
	src := `def increment(value: i32) -> i32:
    return value + 1
`
	result := parseAndAnalyze(t, "backend_bitcode.llcontext", src)
	outputPath := filepath.Join(t.TempDir(), "module.bc")

	if err := backend.WriteLLVMBitcodeFile(result, outputPath); err != nil {
		t.Fatalf("WriteLLVMBitcodeFile returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected bitcode file to exist: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected non-empty bitcode output, got %d bytes", len(data))
	}
	if !looksLikeBitcodeFile(data) {
		t.Fatalf("expected bitcode magic prefix, got % x", data[:min(len(data), 4)])
	}
}

func TestWriteLLVMObjectFile(t *testing.T) {
	src := `def increment(value: i32) -> i32:
    return value + 1
`
	result := parseAndAnalyze(t, "backend_object.llcontext", src)
	outputPath := filepath.Join(t.TempDir(), "module.o")

	if err := backend.WriteLLVMObjectFile(result, outputPath); err != nil {
		t.Fatalf("WriteLLVMObjectFile returned error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected object file to exist: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected non-empty object output, got %d bytes", len(data))
	}
	if !looksLikeObjectFile(data) {
		t.Fatalf("expected native object file magic, got % x", data[:min(len(data), 4)])
	}
}

func TestGenerateLLVMIRUsesABISizeofForPaddedStructs(t *testing.T) {
	src := `repr(c) struct Padded:
    tag: i8
    value: i32

def padded_size() -> usize:
    return sizeof(Padded)

def array_view_size() -> usize:
	return sizeof(view[i32])
`
	result := parseAndAnalyze(t, "backend_sizeof.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"target datalayout =",
		"target triple =",
		"define i64 @padded_size()",
		"ret i64 8",
		"define i64 @array_view_size()",
		"ret i64 24",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersModuloAndModuloAssign(t *testing.T) {
	src := `global folded_mod: i32 = 20 % 6

def rem_signed(left: i32, right: i32) -> i32:
    return left % right

def rem_unsigned() -> u32:
    value: mutable u32 = 10u32
    value %= 4u32
    return value
`
	result := parseAndAnalyze(t, "backend_modulo.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"@folded_mod = global i32 2",
		"define i32 @rem_signed(i32",
		"srem i32",
		"define i32 @rem_unsigned()",
		"urem i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "urem i32") < 1 {
		t.Fatalf("expected modulo assignment to lower via urem, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPointerArithmetic(t *testing.T) {
	src := `def advance(ptr: any u8&, offset: usize) -> any u8&:
    return ptr + offset

def advance_commutative(offset: usize, ptr: any u8&) -> any u8&:
    return offset + ptr

def rewind(ptr: any u8&, offset: usize) -> any u8&:
    return ptr - offset
`
	result := parseAndAnalyze(t, "backend_pointer_arithmetic.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @advance(ptr",
		"define ptr @advance_commutative(i64",
		"define ptr @rewind(ptr",
		"getelementptr i8, ptr",
		"sub i64 0,",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "getelementptr i8, ptr") < 3 {
		t.Fatalf("expected pointer arithmetic to lower via GEP in all functions, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersManualRegions(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	value: any i32& = new[scratch] seed + 1
	return value[0u]
`
	result := parseAndAnalyze(t, "backend_manual_regions.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Arena = type { ptr, ptr, i64 }",
		"declare ptr @new_region(i64)",
		"declare ptr @arena_alloc(ptr, i64)",
		"declare void @arena_free(ptr)",
		"call ptr @new_region(i64 1024)",
		"call ptr @arena_alloc(ptr",
		"call void @arena_free(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRegionCheckpoints(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	mark scratch as cp
	temp: any i32& = new[scratch] seed + 1
	restore scratch from cp
	reused: any i32& = new[scratch] seed + 2
	value: i32 = reused[0u]
	reset scratch
	final: any i32& = new[scratch] seed + 3
	return value + final[0u]
`
	result := parseAndAnalyze(t, "backend_region_checkpoints.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ArenaMark = type { ptr, i64 }",
		"declare %ArenaMark @arena_snapshot(ptr)",
		"declare void @arena_rewind(ptr, %ArenaMark)",
		"declare void @arena_reset(ptr)",
		"call %ArenaMark @arena_snapshot(ptr",
		"call void @arena_rewind(ptr",
		"call void @arena_reset(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedRegionCheckpoints(t *testing.T) {
	src := `def region_value(seed: i32) -> i32:
	region scratch(1024u)
	mark scratch as outer
	stable: any i32& = new[scratch] seed + 1
	mark scratch as inner
	temp: any i32& = new[scratch] seed + 2
	restore scratch from inner
	kept: i32 = stable[0u]
	restore scratch from outer
	reset scratch
	fresh: any i32& = new[scratch] seed + 3
	return kept + fresh[0u]
`
	result := parseAndAnalyze(t, "backend_nested_region_checkpoints.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	if got := strings.Count(output, "call %ArenaMark @arena_snapshot(ptr"); got != 2 {
		t.Fatalf("expected 2 arena_snapshot calls, got %d\n%s", got, output)
	}
	if got := strings.Count(output, "call void @arena_rewind(ptr"); got != 2 {
		t.Fatalf("expected 2 arena_rewind calls, got %d\n%s", got, output)
	}
	if got := strings.Count(output, "call void @arena_reset(ptr"); got != 1 {
		t.Fatalf("expected 1 arena_reset call, got %d\n%s", got, output)
	}
}

func TestGenerateLLVMIRLowersEnumConstructorsAndMatch(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def make_pair() -> MaybeInt:
	return MaybeInt.Pair(3, 4)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	match value:
		MaybeInt.None:
			return fallback
		MaybeInt.Some(inner):
			return inner
		MaybeInt.Pair(left, right):
			return left + right
`
	result := parseAndAnalyze(t, "backend_enum_match.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define %MaybeInt @make_pair()",
		"define i64 @unwrap_or(%MaybeInt",
		"switch i32 %match.tag.value",
		"store i32 2",
		"extractvalue { i64, i64 }",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMatchExpressionsViaPhi(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def unwrap_or(value: MaybeInt, fallback: int) -> int:
	return match value:
		MaybeInt.None:
			fallback
		MaybeInt.Some(inner):
			inner
		MaybeInt.Pair(left, right):
			left + right
`
	result := parseAndAnalyze(t, "backend_enum_match_expr.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i64 @unwrap_or(%MaybeInt",
		"phi i64",
		"switch i32 %match.tag.value",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersStringMatchExpressionsViaPhi(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	return match text:
		"if":
			1
		"local":
			2
		_:
			0
`
	result := parseAndAnalyze(t, "backend_string_match_expr.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "classify")
	if body == "" {
		t.Fatalf("expected to find classify body, got:\n%s", output)
	}
	checks := []string{
		"define i64 @classify(%StringView",
		"phi i64",
		"extractvalue %StringView",
		"load i8, ptr",
		"getelementptr i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ctx_string_view_eq", "ctx_string_views_eq", "@memcmp("} {
		if strings.Contains(body, bad) {
			t.Fatalf("expected tiny string match literals to avoid %q, got:\n%s", bad, body)
		}
	}
	if strings.Count(body, "icmp eq i8") < 2 {
		t.Fatalf("expected classify to compare literal bytes directly for both string arms, got:\n%s", body)
	}
}

func TestGenerateLLVMIRLowersTreeMembersAndFieldAccess(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	expr Expr:
		Nil
		Binary(left: Expr, right: Expr)
	block Block:
		stmts: darray[i64]
	struct ElseIf:
		condition: Expr
		body: Block

def make_nil() -> Lua.Expr:
	return Lua.Expr.Nil(span: 7)

def span_of(node: Lua.Expr) -> i64:
	return node.span

def stmt_count(block: Lua.Block) -> usize:
	return block.stmts.count

def cond_span(branch: Lua.ElseIf) -> i64:
	return branch.condition.span
`
	result := parseAndAnalyze(t, "backend_tree_members.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Lua_Expr__Node = type { i32, i64, [2 x i64] }",
		"declare ptr @alloc_perm(i64)",
		"define ptr @make_nil()",
		"define i64 @span_of(ptr",
		"define i64 @stmt_count(",
		"define i64 @cond_span(",
		"tree.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersStringMatchStatementsWithoutPhi(t *testing.T) {
	src := `def classify(text: StringView) -> int:
	match text:
		"do":
			return 1
		"end":
			return 2
		_:
			return 0
`
	result := parseAndAnalyze(t, "backend_string_match_stmt.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "classify")
	if body == "" {
		t.Fatalf("expected to find classify body, got:\n%s", output)
	}
	checks := []string{
		"define i64 @classify(%StringView",
		"extractvalue %StringView",
		"load i8, ptr",
		"getelementptr i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"phi i64", "ctx_string_view_eq", "ctx_string_views_eq", "@memcmp("} {
		if strings.Contains(body, bad) {
			t.Fatalf("expected statement-form tiny string match to avoid %q, got:\n%s", bad, body)
		}
	}
	if strings.Count(body, "icmp eq i8") < 2 {
		t.Fatalf("expected classify to compare literal bytes directly for both string arms, got:\n%s", body)
	}
}

func TestGenerateLLVMIRLowersNestedMatchPatterns(t *testing.T) {
	src := `enum Inner:
	A(int)
	B

enum Outer:
	Wrap(Inner)
	Empty

def nested_value(value: Outer) -> int:
	return match value:
		Outer.Wrap(Inner.A(inner)):
			inner
		Outer.Wrap(Inner.B):
			0
		Outer.Empty:
			-1
`
	result := parseAndAnalyze(t, "backend_nested_match_patterns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Inner = type { i32, [1 x i64] }",
		"%Outer = type { i32, [2 x i64] }",
		"define i64 @nested_value(%Outer",
		"extractvalue %Outer",
		"extractvalue %Inner",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "icmp eq i32") < 3 {
		t.Fatalf("expected nested match lowering to compare multiple enum tags, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersNamedEnumPayloadPatterns(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def score(value: PairOrInt) -> int:
	return match value:
		PairOrInt.Just(value: inner):
			inner
		PairOrInt.Pair(right: r, left: l):
			l + r
`
	result := parseAndAnalyze(t, "backend_enum_named_payload_patterns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define i64 @score(%PairOrInt",
		"extractvalue %PairOrInt",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "extractvalue { i64, i64 }") < 1 {
		t.Fatalf("expected named payload pattern lowering to unpack pair payloads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersNamedEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(right: 4, left: 3)
`
	result := parseAndAnalyze(t, "backend_enum_named_ctor_args.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define %PairOrInt @make_pair()",
		"store { i64, i64 } { i64 3, i64 4 }, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersEnumEqualityViaTagAndPayloadWords(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def same_none(left: MaybeInt, right: MaybeInt) -> bool:
	return left == right

def differs(left: MaybeInt, right: MaybeInt) -> bool:
	return left != right

def compare_payload() -> bool:
	return MaybeInt.Pair(3, 4) == MaybeInt.Pair(3, 4)
`
	result := parseAndAnalyze(t, "backend_enum_equality.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i1 @same_none(%MaybeInt",
		"define i1 @differs(%MaybeInt",
		"define i1 @compare_payload()",
		"extractvalue %MaybeInt",
		"extractvalue [2 x i64]",
		"icmp eq i32",
		"and i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"icmp eq %MaybeInt", "icmp ne %MaybeInt"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected enum comparisons to avoid aggregate icmp %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadlessEnumsAsPlainTags(t *testing.T) {
	src := `enum TokenKind:
	Ident
	Region
	Destroy
	New

def is_region(kind: TokenKind) -> bool:
	return kind == TokenKind.Region

def next_kind() -> TokenKind:
	return TokenKind.Destroy
`
	result := parseAndAnalyze(t, "backend_payloadless_enum_tags.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @is_region(i32",
		"define i32 @next_kind()",
		"icmp eq i32",
		"ret i32 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"%TokenKind = type", "[0 x i64]", "extractvalue %TokenKind"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected payloadless enum lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedEnumStoresAllocationsAndMatches(t *testing.T) {
	src := `packed enum Expr:
	Lit(int)
	Add(Expr, Expr)

def fold() -> int:
	region scratch(1024u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	left: Expr = new[store] Expr.Lit(3)
	right: Expr = new[store] Expr.Lit(4)
	node: Expr = new[store] Expr.Add(left, right)
	return match node in store:
		Expr.Lit(value):
			value
		Expr.Add(lhs, rhs):
			1 if lhs != rhs else 0
`
	result := parseAndAnalyze(t, "backend_packed_enum_match.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Expr__Store = type { ptr, i64, ptr }",
		"%Expr = type { i32, [2 x i64] }",
		"define i64 @fold()",
		"call ptr @new_region(i64 1024)",
		"call ptr @arena_alloc(ptr",
		"extractvalue %Expr__Store",
		"load i32, ptr",
		"load { ptr, ptr }, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed enum matching to load through handles rather than extract aggregate enum values, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedIfPatternWithoutExplicitStore(t *testing.T) {
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
	if box.store[0u] as Expr.Int(value: value):
		return value
	return 0
`
	result := parseAndAnalyze(t, "backend_packed_if_pattern_inferred_store.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"define i64 @read(",
		"ctx_packed_store_row_ptr_at",
		"load i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "missing active packed enum store") {
		t.Fatalf("expected inferred packed store lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDenseNodeTablesDirectly(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: i32
	Lit(value: i32)
	Add(left: Expr, right: Expr)

def inspect(owner: Arena) -> i32:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1i32, value: 3i32)
		right: Expr = new Expr.Lit(span: 2i32, value: 4i32)
		_ = new Expr.Add(span: 5i32, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[2u]
	key: NodeKey[Expr] = dense_key(node, frozen)
	table: NodeTable[Expr, i32] = node_table_fill.specialize[Expr, i32]()(owner, frozen, -1i32)
	table[key] <- 0i32
	values: dview[i32] = table.values
	if values.len == frozen.count:
		return frozen[key].span
	return 0i32
`
	result := parseAndAnalyze(t, "backend_dense_node_table.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{
		"%NodeKey__Expr = type { i32 }",
		"%NodeTable__Expr__i32 = type { %DynArrayView }",
		"call ptr @arena_alloc(",
		"call void @arena_da_fill(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@node_table_fill(") {
		t.Fatalf("expected node_table_fill to lower directly in the backend without a runtime wrapper, got:\n%s", output)
	}

	body := functionIR(output, "inspect")
	if body == "" {
		t.Fatalf("expected to find inspect body, got:\n%s", output)
	}
	if !strings.Contains(body, "getelementptr inbounds nuw %NodeTable__Expr__i32") {
		t.Fatalf("expected inspect to address through the NodeTable carrier struct, got:\n%s", body)
	}
	if !strings.Contains(body, "extractvalue %NodeKey__Expr") {
		t.Fatalf("expected inspect to read the dense node key carrier index field, got:\n%s", body)
	}
}

func TestGenerateLLVMIRLowersDenseNodeTablesFromHiddenFrozenStoreFieldRoots(t *testing.T) {
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
		left: Expr = new Expr.Lit(span: 1i32, value: 3i32)
		right: Expr = new Expr.Lit(span: 2i32, value: 4i32)
		_ = new Expr.Add(span: 5i32, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return FrozenBox(frozen)

def inspect(owner: Arena) -> i32:
	box: FrozenBox = make_box(owner)
	node: Expr = box.store[2u]
	key: NodeKey[Expr] = dense_key(node, box.store)
	table: NodeTable[Expr, i32] = node_table_fill.specialize[Expr, i32]()(owner, box.store, -1i32)
	table[key] <- 7i32
	return table[key]
`
	result := parseAndAnalyze(t, "backend_dense_node_table_hidden_frozen_field_root.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{
		"%NodeKey__Expr = type { i32 }",
		"%NodeTable__Expr__i32 = type { %DynArrayView }",
		"call ptr @arena_alloc(",
		"call void @arena_da_fill(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@node_table_fill(") {
		t.Fatalf("expected hidden-field node_table_fill to lower directly in the backend without a runtime wrapper, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersMatchOnRefinedPackedViewScrutinee(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def fold(node: Expr, store: Expr.Store[Local]) -> int:
	if node in store as Expr.Int(value: value):
		return match node in store:
			Expr.Int(value: inner):
				inner + value + node.span
			Expr.Add(left: left, right: right):
				left.span + right.span
	return 0
`
	result := parseAndAnalyze(t, "backend_packed_match_refined_view_scrutinee.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @fold(", "%PackedView__Expr__Int = type { ptr, %Expr__Store }", "load i32, ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersUnnamedPackedViewSurfaceType(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(int)

def score(view_node: packedview[Expr.Int]) -> int:
	return view_node.span
`
	result := parseAndAnalyze(t, "backend_packed_unnamed_view_type.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @score(", "%PackedView__Expr__Int = type { ptr, %Expr__Store }", "load i64, ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBarePackedVariantSurfaceType(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(int)

def score(view_node: Expr.Int) -> int:
	return view_node.span
`
	result := parseAndAnalyze(t, "backend_packed_bare_variant_type.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @score(", "%PackedView__Expr__Int = type { ptr, %Expr__Store }", "load i64, ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedEnumWithAffinePayloadMatch(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

packed enum Job:
	Run(thread: Thread[i64, Joinable])

def consume(owner: Arena, thread: Thread[i64, Joinable]) -> i64:
	store: Job.Store[Local] = Job.Store(owner)
	job: Job = new[store] Job.Run(thread: move thread)
	match job in store:
		Job.Run(thread: taken):
			return join(move taken)
	return 0
`
	result := parseAndAnalyze(t, "backend_packed_affine_payload_match.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @consume(", "declare i64 @join(", "call i64 @join("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersViewStmtPayloadDestructureAndRefinedScrutinee(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr, store: Expr.Store[Local]) -> int:
	view node as Expr.Int(value: value):
		return value + node.value + node.span
	return 0
`
	result := parseAndAnalyze(t, "backend_packed_view_destructure_refined_scrutinee.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @read(", "%Expr = type { i32, i64, [1 x i64] }", "load i64, ptr %value, align 8"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "getelementptr inbounds nuw %Expr, ptr %node1, i32 0, i32 2") != 1 {
		t.Fatalf("expected refined view scrutinee lowering to address the payload once for destructuring and reuse that value for node.value field access, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersViewStmtWithDirectFieldProjectionWithoutExplicitStore(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

struct RootBox:
	root: Expr

def read(store: Expr.Store[Frozen], index: usize) -> int:
	box: RootBox = RootBox(store[index])
	view box.root as Expr.Int(value: value):
		return value + box.root.span
	return 0
`
	result := parseAndAnalyze(t, "backend_packed_view_direct_field_projection_inferred_store.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define i64 @read(", "%RootBox = type { ptr }", "load i64, ptr %value, align 8"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "missing active packed enum store") {
		t.Fatalf("expected direct field projection packed store inference during lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedInStoreBlockSugar(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

def fold() -> int:
	region scratch(1024u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		node: Expr = new Expr.Add(span: 3, left: left, right: right)
		return match node:
			Expr.Lit(value: value):
				value + node.span
			Expr.Add(left: lhs, right: rhs):
				node.span + lhs.span + rhs.span
`
	result := parseAndAnalyze(t, "backend_packed_in_store_block.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Expr = type { i32, i64, [2 x i64] }",
		"define i64 @fold()",
		"call ptr @new_region(i64 1024)",
		"call ptr @ctx_packed_store_state_new(ptr",
		"call ptr @arena_alloc(ptr",
		"call void @ctx_packed_store_record_row_ptr(ptr",
		"store %Expr { i32 1, i64 3, [2 x i64] zeroinitializer }, ptr %packed.alloc",
		"load i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected in-store packed sugar to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedEnumsAsHandles(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def differs(left: Token, right: Token) -> bool:
	return left != right
`
	result := parseAndAnalyze(t, "backend_packed_payloadless_enum.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"define i1 @differs(ptr",
		"icmp ne ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ret i32", "icmp ne i32"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected payloadless packed enums to stay handle-backed and avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersBarePackedConstructorCallWithActiveStore(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def build(owner: Arena) -> Expr:
	store: Expr.Store[Local] = Expr.Store(owner)
	return Expr.Int(span: 7, value: 1)
`
	result := parseAndAnalyze(t, "backend_packed_ctor_active_store.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)
	for _, check := range []string{"define ptr @build(", "call ptr @arena_alloc", "store %Expr { i32 0, i64 7, [1 x i64] zeroinitializer }, ptr %packed.alloc"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedCommonFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def read(node: Expr) -> int:
	return node.span
`
	result := parseAndAnalyze(t, "backend_packed_common_field.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Expr = type { i32, i64, [1 x i64] }",
		"define i64 @read(ptr",
		"getelementptr inbounds",
		"%Expr, ptr",
		"load i64, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "extractvalue %Expr,") {
		t.Fatalf("expected packed common field access to lower through the row handle, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)

def build() -> Expr:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	return new[store] Expr.Int(span: 9, value: 5)
`
	result := parseAndAnalyze(t, "backend_packed_common_field_init.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Expr = type { i32, i64, [1 x i64] }",
		"define ptr @build()",
		"store %Expr { i32 0, i64 9, [1 x i64] zeroinitializer }, ptr %packed.alloc",
		"store i64 5, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected packed common-field initialization to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Token:
	common:
		span: int
	Region

def build() -> Token:
	region scratch(256u)
	store: Token.Store[Local] = Token.Store(scratch)
	return new[store] Token.Region(span: 4)
`
	result := parseAndAnalyze(t, "backend_payloadless_packed_common_init.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Token = type { i32, i64 }",
		"define ptr @build()",
		"store %Token { i32 0, i64 4 }, ptr %packed.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadlessPackedAllocationFromQualifiedConstructor(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build() -> Token:
	region scratch(256u)
	store: Token.Store[Local] = Token.Store(scratch)
	return new[store] Token.Region
`
	result := parseAndAnalyze(t, "backend_payloadless_packed_alloc.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Token = type { i32 }",
		"define ptr @build()",
		"call ptr @new_region(i64 256)",
		"call ptr @arena_alloc(ptr",
		"store %Token { i32 1 }, ptr %packed.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected payloadless packed allocation to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersBarePayloadlessPackedConstructorWithActiveStore(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build() -> Token:
	region scratch(256u)
	store: Token.Store[Local] = Token.Store(scratch)
	return Token.Region
`
	result := parseAndAnalyze(t, "backend_payloadless_packed_ctor_active_store.llcontext", src)
	output := generateLLVMIRWithPackedABIForTest(t, result, backend.PackedEnumABIRowHandle)

	checks := []string{
		"%Token = type { i32 }",
		"define ptr @build()",
		"call ptr @arena_alloc(ptr",
		"store %Token { i32 1 }, ptr %packed.alloc",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "unknown enum constructor") {
		t.Fatalf("expected bare payloadless packed constructor sugar to lower successfully, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDictSurfaceTypesViaDynDictCarrier(t *testing.T) {
	src := `extern take_runtime(values: DynDict[i32]) -> void
extern make_runtime() -> DynDict[i32]

def id[V](values: dict[dstr, V]) -> dict[dstr, V]:
	return values

def keep(values: dict[dstr, i32]) -> dict[dstr, i32]:
	return id(values)

def pass_runtime(values: dict[dstr, i32]) -> void:
	take_runtime(values)

def from_runtime() -> dict[dstr, i32]:
	return make_runtime()
`
	result := parseAndAnalyze(t, "backend_dict_runtime_bridge.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynDict__i32 = type { ptr, i64, i64, i64, ptr }",
		"declare void @take_runtime(%DynDict__i32)",
		"declare %DynDict__i32 @make_runtime()",
		"define %DynDict__i32 @id__i32(%DynDict__i32",
		"define %DynDict__i32 @keep(%DynDict__i32",
		"call %DynDict__i32 @id__i32(%DynDict__i32",
		"define void @pass_runtime(%DynDict__i32",
		"call void @take_runtime(%DynDict__i32",
		"define %DynDict__i32 @from_runtime()",
		"call %DynDict__i32 @make_runtime()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesDictHelperStyleFunctions(t *testing.T) {
	src := `error RuntimeError:
	OutOfMemory

def arena_dict_get[V](m: any dict[dstr, V]&, key: dstr) -> any V&?:
	return null

def arena_dict_put[V](a: any Arena&, m: any dict[dstr, V]&, key: dstr, value: V) -> any V&? error[RuntimeError]:
	raise RuntimeError.OutOfMemory

def touch(a: any Arena&, m: any dict[dstr, i32]&, key: dstr) -> bool:
	slot: any i32&? = try arena_dict_put(a, m, key, 7) else null
	if slot == null:
		return false
	maybe_slot: any i32&? = arena_dict_get(m, key)
	return maybe_slot != null
`
	result := parseAndAnalyze(t, "backend_dict_helper_calls.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__RuntimeError__any_i32 = type { i32, ptr }",
		"define ptr @arena_dict_get__i32(ptr",
		"define i32 @arena_dict_put__i32(ptr",
		"define i1 @touch(ptr %0, ptr %1, ptr %2)",
		"call i32 @arena_dict_put__i32(ptr",
		"call ptr @arena_dict_get__i32(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrontendStressFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "frontend_stress.llcontext")
	result := parseAndAnalyze(t, "backend_frontend_stress.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%SourceSpan = type { i64, i64 }",
		"%Token = type { i32, %SourceSpan, ptr }",
		"%DynDict__Symbol = type { ptr, i64, i64, i64, ptr }",
		"%Scope = type { ptr, %DynDict__Symbol, i64 }",
		"%ParserState = type { %DynArrayView, i64, ptr }",
		"define %DynArrayView @make_tokens()",
		"define i32 @frontend_scope_stress(ptr",
		"define i64 @frontend_region_token(i64",
		"define i32 @frontend_smoke(ptr",
		"define %DynDict__Symbol @arena_dict_new__Symbol(ptr",
		"define i32 @arena_dict_put__Symbol(ptr",
		"define i1 @arena_dict_contains__Symbol(ptr",
		"call ptr @new_region(i64 2048)",
		"call ptr @arena_alloc(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersAllocatorOwnershipFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "allocator_ownership.llcontext")
	result := parseAndAnalyze(t, "backend_allocator_ownership.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FuzzPair = type { i64, i64 }",
		"%HeapPairNode = type { %FuzzPair, ptr }",
		"declare ptr @alloc_heap_pair_node()",
		"declare ptr @sfree_heap_pair_node(ptr)",
		"declare ptr @alloc_bytes(i64)",
		"declare ptr @sfree_bytes(ptr)",
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i32 @recursive_pair_node_sum(ptr ",
		"define void @recursive_free_pair_chain(ptr ",
		"define i32 @build_pair_chain_sum(ptr ",
		"call i32 @recursive_pair_node_sum(ptr ",
		"call void @recursive_free_pair_chain(ptr ",
		"define i32 @alloc_and_format_heap_buffer(ptr ",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"@recursive_format_or_fallback(",
		"@allocator_ownership_combo(",
		"alloca [32 x i8]",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "call ptr @sfree_heap_pair_node(ptr") < 2 {
		t.Fatalf("expected multiple heap-pair frees in ownership fixture lowering, got:\n%s", output)
	}
	if strings.Count(output, "call i32 @require_heap_pair_node(ptr") < 3 {
		t.Fatalf("expected repeated heap-pair acquisition helper lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersErrorUnionsTryAndRaise(t *testing.T) {
	src := `error MemoryError:
	OutOfMemory

error IoError:
	NotFound

extern alloc(size: usize) -> heap void&?
extern read_file(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]

def checked_alloc(size: usize) -> heap void& error[MemoryError.OutOfMemory, ...]:
	ptr: heap void& = alloc(size) else raise MemoryError.OutOfMemory
	return ptr

def load_text(path: any u8&) -> dstr[file_text] error[IoError.NotFound, ...]:
	text: dstr[file_text] = try read_file(path)
	return text

def load_with_fallback(path: any u8&) -> any u8&:
	text: any u8& = try read_file(path) else "".cast[any u8&]
	return text
`
	result := parseAndAnalyze(t, "backend_error_handling.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__dstr_file_text = type { i32, ptr }",
		"%ErrUnion__MemoryError__heap_void = type { i32, ptr }",
		"declare ptr @alloc(i64)",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @checked_alloc(ptr ",
		"define i32 @load_text(ptr ",
		"define ptr @load_with_fallback(ptr ",
		"extractvalue %ErrUnion__IoError__dstr_file_text",
		"insertvalue %ErrUnion__IoError__dstr_file_text",
		"icmp eq i32",
		"phi ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRRemapsErrorCodesWhenWideningErrorSets(t *testing.T) {
	src := `error SourceError:
	NotFound
	PermissionDenied

error BroadError:
	PermissionDenied
	Busy
	NotFound

extern read_value() -> int error[SourceError.NotFound, ...]

def bubble() -> int error[BroadError.NotFound, ...]:
	return try read_value()

def fail_now() -> int error[BroadError.NotFound, ...]:
	raise SourceError.NotFound
`
	result := parseAndAnalyze(t, "backend_error_set_widening.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__SourceError__int = type { i32, i64 }",
		"%ErrUnion__BroadError__int = type { i32, i64 }",
		"declare i32 @read_value(ptr)",
		"define i32 @bubble(ptr ",
		"define i32 @fail_now(ptr ",
		"errmap_is_SourceError_NotFound",
		"errmap_is_SourceError_PermissionDenied",
		"ret i32 3",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRComposesMultipleErrorFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError, NetworkError]:
	return try read_disk()

def bubble_network() -> int error[FileError, NetworkError]:
	return try read_network()
`
	result := parseAndAnalyze(t, "backend_error_multi_family.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__FileError__int = type { i32, i64 }",
		"declare i32 @read_disk(ptr)",
		"declare i32 @read_network(ptr)",
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRExpandsMixedRowStyleFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	return try read_disk()

def bubble_network() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	return try read_network()

def fail_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	raise FileError.PermissionDenied
`
	result := parseAndAnalyze(t, "backend_error_mixed_row_style.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"define i32 @fail_disk(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
		"ret i32 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRCanonicalizesErrorUnionNames(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_value() -> int error[NetworkError, FileError]

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return try read_value()
`
	result := parseAndAnalyze(t, "backend_error_canonicalization.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	if !strings.Contains(output, "%ErrUnion__error_FileError__NetworkError__int = type { i32, i64 }") {
		t.Fatalf("expected canonical error union struct name, got:\n%s", output)
	}
}

func TestGenerateLLVMIRAcceptsBareFamilyErrorSetShorthand(t *testing.T) {
	src := `error IoError:
	NotFound

extern read_file(path: any u8&) -> dstr[file_text] error[IoError]

def load_text(path: any u8&) -> dstr[file_text] error[IoError, ...]:
	text: dstr[file_text] = try read_file(path)
	return text
`
	result := parseAndAnalyze(t, "backend_error_set_wildcard.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__dstr_file_text = type { i32, ptr }",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @load_text(ptr ",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersValueOptionalsAndTryElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def fallback_value(flag: bool) -> int:
	return try maybe_value(flag) else 11
`
	result := parseAndAnalyze(t, "backend_value_optionals.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Optional__int = type { i1, i64 }",
		"define %Optional__int @maybe_value(i1",
		"define i64 @fallback_value(i1",
		"extractvalue %Optional__int",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersOptionalNullChecksAndSmartCastUse(t *testing.T) {
	src := `repr(c) struct Box:
	value: i32


def maybe_box(flag: bool) -> Box?:
	if flag:
		return Box(7)
	return null


def unwrap_or(flag: bool) -> i32:
	value: Box? = maybe_box(flag)
	if value == null:
		return 11
	return value.value
`
	result := parseAndAnalyze(t, "backend_value_optionals_smart_cast.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Optional__Box = type { i1, %Box }",
		"define i32 @unwrap_or(i1",
		"extractvalue %Optional__Box",
		"getelementptr inbounds nuw %Optional__Box",
		"getelementptr inbounds nuw %Box",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesRuntimeBackedArraysAndViews(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	items: mutable any void&?
    count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
    return values[1]

def read_view(view: dview[i32]) -> i32:
    return view[2]
`
	result := parseAndAnalyze(t, "backend_runtime_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i32 @read_array(%DynArray__i32",
		"define i32 @read_view(%DynArrayView",
		"getelementptr inbounds nuw %DynArray__i32",
		"getelementptr inbounds nuw %DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesDStrViaRuntimeHelper(t *testing.T) {
	src := `def read_codepoint(text: dstr[row]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_runtime_dstr_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @read_codepoint(ptr",
		"declare i64 @ctx_string_index(ptr, i64)",
		"call i64 @ctx_string_index(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep(text: dstr) -> dstr:
    return text

def erase(text: dstr[row]) -> dstr:
    return text
`
	result := parseAndAnalyze(t, "backend_dstr_shorthand.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @keep(ptr",
		"define ptr @erase(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIndexesStringViewViaRuntimeHelper(t *testing.T) {
	src := `def read_view(view: StringView) -> char:
    return view[1]
`
	result := parseAndAnalyze(t, "backend_runtime_string_view_index.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i64 @read_view(%StringView",
		"declare i64 @ctx_string_view_index(%StringView, i64)",
		"call i64 @ctx_string_view_index(%StringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersRuntimeStringEqualityHelpers(t *testing.T) {
	src := `def same_text(left: dstr[row], right: dstr[col]) -> bool:
	return left == right

def same_view_text(view: StringView, text: dstr[row]) -> bool:
	return view == text

def same_text_view(text: dstr[row], view: StringView) -> bool:
	return text == view

def different_views(left: StringView, right: StringView) -> bool:
	return left != right
`
	result := parseAndAnalyze(t, "backend_runtime_string_equality.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @ctx_streq(ptr, ptr)",
		"declare i64 @ctx_string_view_eq(%StringView, ptr)",
		"declare i64 @ctx_string_views_eq(%StringView, %StringView)",
		"call i64 @ctx_streq(ptr",
		"call i64 @ctx_string_view_eq(%StringView",
		"call i64 @ctx_string_views_eq(%StringView",
		"icmp ne i64",
		"icmp eq i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesSameExtentRuntimeStringEquality(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	return StringView("".cast[any u8&], end - start)

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return string_view(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return string_view(view.data, start, view.len)

def same_shape_text(left: dstr[row], right: dstr[row]) -> bool:
	return left == right

def same_bounds_view(left: dstr[row], right: dstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 2)
	return left_view == right_view

def fresh_disjoint_raw_views() -> bool:
	region scratch(1024u)
	return string_view(new[scratch] 1u8, 0, 1) == string_view(new[scratch] 2u8, 0, 1)

def split_disjoint_views(text: dstr[row]) -> bool:
	base: StringView = ctx_string_view(text, 0, 4)
	return ctx_string_view_prefix(base, 2) == ctx_string_view_suffix(base, 2)

def different_bounds_view(left: dstr[row], right: dstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 3)
	return left_view == right_view
`
	result := parseAndAnalyze(t, "backend_runtime_string_same_extent.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @memcmp(ptr, ptr, i64)",
		"declare i64 @ctx_strlen(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	sameShapeBody := functionIR(output, "same_shape_text")
	if sameShapeBody == "" {
		t.Fatalf("expected to find same_shape_text body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(sameShapeBody, want) {
			t.Fatalf("expected same_shape_text to contain %q, got:\n%s", want, sameShapeBody)
		}
	}
	if strings.Contains(sameShapeBody, "call i64 @ctx_streq") {
		t.Fatalf("expected same_shape_text to avoid ctx_streq helper, got:\n%s", sameShapeBody)
	}

	sameBoundsBody := functionIR(output, "same_bounds_view")
	if sameBoundsBody == "" {
		t.Fatalf("expected to find same_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(sameBoundsBody, "call i64 @memcmp(ptr") {
		t.Fatalf("expected same_bounds_view to use memcmp fast path, got:\n%s", sameBoundsBody)
	}
	if strings.Contains(sameBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected same_bounds_view to avoid ctx_string_views_eq helper, got:\n%s", sameBoundsBody)
	}

	disjointBoundsBody := functionIR(output, "fresh_disjoint_raw_views")
	if disjointBoundsBody == "" {
		t.Fatalf("expected to find fresh_disjoint_raw_views body, got:\n%s", output)
	}
	if !strings.Contains(disjointBoundsBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected fresh_disjoint_raw_views to mark memcmp operands noalias, got:\n%s", disjointBoundsBody)
	}

	splitBoundsBody := functionIR(output, "split_disjoint_views")
	if splitBoundsBody == "" {
		t.Fatalf("expected to find split_disjoint_views body, got:\n%s", output)
	}
	if !strings.Contains(splitBoundsBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected split_disjoint_views to use disjoint memcmp fast path, got:\n%s", splitBoundsBody)
	}
	if strings.Contains(splitBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected split_disjoint_views to avoid ctx_string_views_eq helper, got:\n%s", splitBoundsBody)
	}

	differentBoundsBody := functionIR(output, "different_bounds_view")
	if differentBoundsBody == "" {
		t.Fatalf("expected to find different_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(differentBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected different_bounds_view to keep helper fallback, got:\n%s", differentBoundsBody)
	}
}

func TestGenerateLLVMIRSpecializesDirectRuntimeStringEqualityHelpers(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

extern ctx_string_view(value: dstr[row], start: i64, end: i64) -> StringView
extern ctx_string_view_prefix(view: StringView, end: i64) -> StringView
extern ctx_string_view_suffix(view: StringView, start: i64) -> StringView
extern ctx_string_slice(value: dstr[row], start: i64, end: i64) -> dstr[shape_out]
extern ctx_streq(left: dstr[row], right: dstr[row]) -> int
extern ctx_string_view_eq(view: StringView, text: dstr[shape_other]) -> int
extern ctx_string_views_eq(left: StringView, right: StringView) -> int

def direct_same_shape_text(left: dstr[row], right: dstr[row]) -> bool:
	return ctx_streq(left, right) != 0

def direct_same_bounds_view_text(left: dstr[row], right: dstr[row]) -> bool:
	view: StringView = ctx_string_view(left, 0, 4)
	return ctx_string_view_eq(view, ctx_string_slice(right, 0, 4)) != 0

def direct_split_disjoint_views(text: dstr[row]) -> bool:
	base: StringView = ctx_string_view(text, 0, 4)
	return ctx_string_views_eq(ctx_string_view_prefix(base, 2), ctx_string_view_suffix(base, 2)) != 0

def direct_different_bounds_view(left: dstr[row], right: dstr[row]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 3)
	return ctx_string_views_eq(left_view, right_view) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_direct_helper_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	directTextBody := functionIR(output, "direct_same_shape_text")
	if directTextBody == "" {
		t.Fatalf("expected to find direct_same_shape_text body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(directTextBody, want) {
			t.Fatalf("expected direct_same_shape_text to contain %q, got:\n%s", want, directTextBody)
		}
	}
	if strings.Contains(directTextBody, "call i64 @ctx_streq") {
		t.Fatalf("expected direct_same_shape_text to avoid ctx_streq helper fallback, got:\n%s", directTextBody)
	}

	directViewTextBody := functionIR(output, "direct_same_bounds_view_text")
	if directViewTextBody == "" {
		t.Fatalf("expected to find direct_same_bounds_view_text body, got:\n%s", output)
	}
	if !strings.Contains(directViewTextBody, "call i64 @memcmp(ptr") {
		t.Fatalf("expected direct_same_bounds_view_text to use memcmp fast path, got:\n%s", directViewTextBody)
	}
	if strings.Contains(directViewTextBody, "call i64 @ctx_string_view_eq") {
		t.Fatalf("expected direct_same_bounds_view_text to avoid ctx_string_view_eq helper fallback, got:\n%s", directViewTextBody)
	}

	directSplitViewsBody := functionIR(output, "direct_split_disjoint_views")
	if directSplitViewsBody == "" {
		t.Fatalf("expected to find direct_split_disjoint_views body, got:\n%s", output)
	}
	if !strings.Contains(directSplitViewsBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected direct_split_disjoint_views to use disjoint memcmp fast path, got:\n%s", directSplitViewsBody)
	}
	if strings.Contains(directSplitViewsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected direct_split_disjoint_views to avoid ctx_string_views_eq helper fallback, got:\n%s", directSplitViewsBody)
	}

	differentBoundsBody := functionIR(output, "direct_different_bounds_view")
	if differentBoundsBody == "" {
		t.Fatalf("expected to find direct_different_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(differentBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected direct_different_bounds_view to keep helper fallback, got:\n%s", differentBoundsBody)
	}
}

func TestGenerateLLVMIRSpecializesDStrLiteralEquality(t *testing.T) {
	src := `extern ctx_streq(left: dstr[row], right: dstr[shape_other]) -> int

def literal_right(text: dstr[row]) -> bool:
	return text == "alphabet soup"

def literal_left(text: dstr[row]) -> bool:
	return "alphabet soup" == text

def direct_literal_right(text: dstr[row]) -> bool:
	return ctx_streq(text, "alphabet soup") != 0

def direct_literal_left(text: dstr[row]) -> bool:
	return ctx_streq("alphabet soup", text) != 0

def direct_empty_literal(text: dstr[row]) -> bool:
	return ctx_streq(text, "") != 0
`
	result := parseAndAnalyze(t, "backend_runtime_dstr_literal_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, name := range []string{"literal_right", "literal_left", "direct_literal_right", "direct_literal_left"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected %s to contain %q, got:\n%s", name, want, body)
			}
		}
		if strings.Contains(body, "call i64 @ctx_streq") {
			t.Fatalf("expected %s to avoid ctx_streq helper fallback, got:\n%s", name, body)
		}
	}

	emptyBody := functionIR(output, "direct_empty_literal")
	if emptyBody == "" {
		t.Fatalf("expected to find direct_empty_literal body, got:\n%s", output)
	}
	if !strings.Contains(emptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected direct_empty_literal to contain ctx_strlen length check, got:\n%s", emptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_streq", "call i64 @memcmp("} {
		if strings.Contains(emptyBody, bad) {
			t.Fatalf("expected direct_empty_literal to avoid %q, got:\n%s", bad, emptyBody)
		}
	}
}

func TestGenerateLLVMIRSpecializesConstantStringSliceLiteralEquality(t *testing.T) {
	src := `extern ctx_string_slice(value: dstr[row], start: i64, end: i64) -> dstr[shape_out]
extern ctx_streq(left: dstr[shape_left], right: dstr[shape_right]) -> int

def slice_literal(text: dstr[row]) -> bool:
	return ctx_string_slice(text, 1, 10) == "lphabet s"

def direct_slice_literal(text: dstr[row]) -> bool:
	return ctx_streq(ctx_string_slice(text, 1, 10), "lphabet s") != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slice_literal_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, name := range []string{"slice_literal", "direct_slice_literal"} {
		body := functionIR(output, name)
		if body == "" {
			t.Fatalf("expected to find %s body, got:\n%s", name, output)
		}
		for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected %s to contain %q, got:\n%s", name, want, body)
			}
		}
		for _, bad := range []string{"call ptr @intern_small_string", "call ptr @alloc_perm", "call i64 @ctx_streq"} {
			if strings.Contains(body, bad) {
				t.Fatalf("expected %s to avoid %q, got:\n%s", name, bad, body)
			}
		}
	}
}

func TestGenerateLLVMIRSpecializesConstantStringSliceEquality(t *testing.T) {
	src := `extern ctx_string_slice_eq(value: dstr[row], start: i64, end: i64, other: dstr[col]) -> int

def slice_eq_const(text: dstr[row], other: dstr[col]) -> bool:
	return ctx_string_slice_eq(text, 1, 3, other) != 0

def slice_eq_empty(text: dstr[row], other: dstr[col]) -> bool:
	return ctx_string_slice_eq(text, 2, 2, other) != 0

def slice_eq_unknown(text: dstr[row], start: i64, end: i64, other: dstr[col]) -> bool:
	return ctx_string_slice_eq(text, start, end, other) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slice_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	sliceEqConstBody := functionIR(output, "slice_eq_const")
	if sliceEqConstBody == "" {
		t.Fatalf("expected to find slice_eq_const body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(sliceEqConstBody, want) {
			t.Fatalf("expected slice_eq_const to contain %q, got:\n%s", want, sliceEqConstBody)
		}
	}
	if strings.Contains(sliceEqConstBody, "call i64 @ctx_string_slice_eq") {
		t.Fatalf("expected slice_eq_const to avoid ctx_string_slice_eq helper fallback, got:\n%s", sliceEqConstBody)
	}

	sliceEqEmptyBody := functionIR(output, "slice_eq_empty")
	if sliceEqEmptyBody == "" {
		t.Fatalf("expected to find slice_eq_empty body, got:\n%s", output)
	}
	if !strings.Contains(sliceEqEmptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected slice_eq_empty to contain ctx_strlen length check, got:\n%s", sliceEqEmptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_string_slice_eq"} {
		if strings.Contains(sliceEqEmptyBody, bad) {
			t.Fatalf("expected slice_eq_empty to avoid %q, got:\n%s", bad, sliceEqEmptyBody)
		}
	}

	sliceEqUnknownBody := functionIR(output, "slice_eq_unknown")
	if sliceEqUnknownBody == "" {
		t.Fatalf("expected to find slice_eq_unknown body, got:\n%s", output)
	}
	if !strings.Contains(sliceEqUnknownBody, "call i64 @ctx_string_slice_eq(ptr") {
		t.Fatalf("expected slice_eq_unknown to keep helper fallback when bounds are not constant, got:\n%s", sliceEqUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesConstantStringSlicesEquality(t *testing.T) {
	src := `extern ctx_string_slices_eq(lhs: dstr[row], lhs_start: i64, lhs_end: i64, rhs: dstr[col], rhs_start: i64, rhs_end: i64) -> int

def slices_eq_const(left: dstr[row], right: dstr[col]) -> bool:
	return ctx_string_slices_eq(left, 1, 4, right, 2, 5) != 0

def slices_eq_empty(left: dstr[row], right: dstr[col]) -> bool:
	return ctx_string_slices_eq(left, 3, 3, right, 7, 7) != 0

def slices_eq_unknown(left: dstr[row], left_start: i64, left_end: i64, right: dstr[col], right_start: i64, right_end: i64) -> bool:
	return ctx_string_slices_eq(left, left_start, left_end, right, right_start, right_end) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_slices_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	slicesEqConstBody := functionIR(output, "slices_eq_const")
	if slicesEqConstBody == "" {
		t.Fatalf("expected to find slices_eq_const body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i64 @memcmp(ptr"} {
		if !strings.Contains(slicesEqConstBody, want) {
			t.Fatalf("expected slices_eq_const to contain %q, got:\n%s", want, slicesEqConstBody)
		}
	}
	if strings.Contains(slicesEqConstBody, "call i64 @ctx_string_slices_eq") {
		t.Fatalf("expected slices_eq_const to avoid ctx_string_slices_eq helper fallback, got:\n%s", slicesEqConstBody)
	}

	slicesEqEmptyBody := functionIR(output, "slices_eq_empty")
	if slicesEqEmptyBody == "" {
		t.Fatalf("expected to find slices_eq_empty body, got:\n%s", output)
	}
	if !strings.Contains(slicesEqEmptyBody, "call i64 @ctx_strlen(ptr") {
		t.Fatalf("expected slices_eq_empty to contain ctx_strlen length checks, got:\n%s", slicesEqEmptyBody)
	}
	for _, bad := range []string{"call i64 @ctx_string_slices_eq"} {
		if strings.Contains(slicesEqEmptyBody, bad) {
			t.Fatalf("expected slices_eq_empty to avoid %q, got:\n%s", bad, slicesEqEmptyBody)
		}
	}

	slicesEqUnknownBody := functionIR(output, "slices_eq_unknown")
	if slicesEqUnknownBody == "" {
		t.Fatalf("expected to find slices_eq_unknown body, got:\n%s", output)
	}
	if !strings.Contains(slicesEqUnknownBody, "call i64 @ctx_string_slices_eq(ptr") {
		t.Fatalf("expected slices_eq_unknown to keep helper fallback when bounds are not constant, got:\n%s", slicesEqUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesTinyStringViewLiteralEquality(t *testing.T) {
	src := `def same_empty(view: StringView) -> bool:
	return view == ""

def same_short(view: StringView) -> bool:
	return view == "def"

def differs_short(view: StringView) -> bool:
	return view != "region"
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_tiny.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i1 @same_empty(%StringView",
		"define i1 @same_short(%StringView",
		"define i1 @differs_short(%StringView",
		"extractvalue %StringView",
		"getelementptr i8, ptr",
		"load i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"ctx_string_view_eq", "@memcmp("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected tiny StringView literal equality to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesLongStringViewLiteralHelperCalls(t *testing.T) {
	src := `extern string_view_eq(view: StringView, other: any u8&?) -> int

def same_long(view: StringView) -> bool:
	return string_view_eq(view, "destroy_region".cast[any u8&]) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_eq_long.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i1 @same_long(%StringView",
		"declare i64 @memcmp(ptr, ptr, i64)",
		"call i64 @memcmp(ptr",
		"zext i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call i64 @string_view_eq") {
		t.Fatalf("expected literal helper call lowering to avoid calling string_view_eq, got:\n%s", output)
	}
}

func TestGenerateLLVMIRMarksDisjointDViewMemcpyCallsNoAlias(t *testing.T) {
	src := `repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

extern arena_memcpy(dest: any void&?, src: any void&?, n: usize) -> any void&?

def arena_da_view_prefix[T](view: dview[T], end: usize) -> dview[T]:
	_ = end
	return view

def arena_da_view_suffix[T](view: dview[T], start: usize) -> dview[T]:
	_ = start
	return view

def split_copy(view: dview[i32]) -> any void&?:
	prefix: dview[i32] = arena_da_view_prefix(view, 2u)
	suffix: dview[i32] = arena_da_view_suffix(view, 2u)
	return arena_memcpy(prefix.data, suffix.data, prefix.len * prefix.elem_size)
`
	result := parseAndAnalyze(t, "backend_dview_split_memcpy.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "split_copy")
	if body == "" {
		t.Fatalf("expected to find split_copy body, got:\n%s", output)
	}
	if !strings.Contains(body, "call ptr @arena_memcpy(ptr noalias") {
		t.Fatalf("expected split_copy to mark arena_memcpy operands noalias, got:\n%s", body)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExact(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

extern arena_memcpy(dest: any void&?, src: any void&?, n: usize) -> any void&?

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_copy_exact[T](dst: dview[T], src: dview[T]):
	if dst.len != src.len:
		return
	_ = dst
	_ = src

def copy_split(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	right: dview[i32] = base[2u:4u]
	arena_da_copy_exact(left, right)

def copy_overlap(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:3u]
	right: dview[i32] = base[1u:4u]
	arena_da_copy_exact(left, right)

def copy_overlap_backward(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[1u:4u]
	right: dview[i32] = base[0u:3u]
	arena_da_copy_exact(left, right)

def copy_overlap_unknown(values: any darray[i32, shape_in]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, values.count)
	left: dview[i32] = base[0u:values.count - 1u]
	right: dview[i32] = base[1u:values.count]
	arena_da_copy_exact(left, right)
`
	result := parseAndAnalyze(t, "backend_dview_copy_exact.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySplitBody := functionIR(output, "copy_split")
	if copySplitBody == "" {
		t.Fatalf("expected to find copy_split body, got:\n%s", output)
	}
	if strings.Contains(copySplitBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_split to avoid helper fallback, got:\n%s", copySplitBody)
	}
	requireTinyExactDViewCopyBody(t, copySplitBody)

	copyOverlapBody := functionIR(output, "copy_overlap")
	if copyOverlapBody == "" {
		t.Fatalf("expected to find copy_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap to avoid helper fallback, got:\n%s", copyOverlapBody)
	}
	if strings.Contains(copyOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapBody)
	}
	if !strings.Contains(copyOverlapBody, "load i32, ptr") || !strings.Contains(copyOverlapBody, "store i32") {
		t.Fatalf("expected copy_overlap to lower through direct element loads/stores, got:\n%s", copyOverlapBody)
	}

	copyOverlapBackwardBody := functionIR(output, "copy_overlap_backward")
	if copyOverlapBackwardBody == "" {
		t.Fatalf("expected to find copy_overlap_backward body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapBackwardBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap_backward to avoid helper fallback, got:\n%s", copyOverlapBackwardBody)
	}
	if strings.Contains(copyOverlapBackwardBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap_backward to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapBackwardBody)
	}
	if !strings.Contains(copyOverlapBackwardBody, "load i32, ptr") || !strings.Contains(copyOverlapBackwardBody, "store i32") {
		t.Fatalf("expected copy_overlap_backward to lower through direct element loads/stores, got:\n%s", copyOverlapBackwardBody)
	}

	copyOverlapUnknownBody := functionIR(output, "copy_overlap_unknown")
	if copyOverlapUnknownBody == "" {
		t.Fatalf("expected to find copy_overlap_unknown body, got:\n%s", output)
	}
	if strings.Contains(copyOverlapUnknownBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_overlap_unknown to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyOverlapUnknownBody)
	}
	if !strings.Contains(copyOverlapUnknownBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_overlap_unknown to keep helper fallback when extent is not exact, got:\n%s", copyOverlapUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return_field(left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_struct(values: array[i32, 4]) -> void:
	boxed: Views = Views(values[0u:2u], values[2u:4u])
	arena_da_copy_exact(boxed.left, boxed.right)

def copy_helper(values: array[i32, 4]) -> void:
	boxed: Views = wrap_views(values[0u:2u], values[2u:4u])
	arena_da_copy_exact(boxed.left, boxed.right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyStructBody := functionIR(output, "copy_struct")
	if copyStructBody == "" {
		t.Fatalf("expected to find copy_struct body, got:\n%s", output)
	}
	if strings.Contains(copyStructBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_struct to avoid helper fallback, got:\n%s", copyStructBody)
	}
	requireTinyExactDViewCopyBody(t, copyStructBody)

	copyHelperBody := functionIR(output, "copy_helper")
	if copyHelperBody == "" {
		t.Fatalf("expected to find copy_helper body, got:\n%s", output)
	}
	if strings.Contains(copyHelperBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper to avoid helper fallback, got:\n%s", copyHelperBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperBody)

}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 1] = [Views(values[0u:2u], values[2u:4u])]
	arena_da_copy_exact(items[0u].left, items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyIndexedBody := functionIR(output, "copy_indexed")
	if copyIndexedBody == "" {
		t.Fatalf("expected to find copy_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_indexed to avoid helper fallback, got:\n%s", copyIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughStandardViewSliceHelperFieldProjection(t *testing.T) {
	src := `repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

repr(c) struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_view_slice(input: view[Views], start: usize, end: usize) -> view[Views]:
	_ = start
	_ = end
	return input

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_helper_view_slice(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0u:2u], values[2u:4u]), Views(values[4u:6u], values[6u:8u])]
	window: view[Views] = arena_da_view_slice(items[1u:2u], 0u, 1u)
	arena_da_copy_exact(window[0u].left, window[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_standard_view_slice_helper_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	body := functionIR(output, "copy_helper_view_slice")
	if body == "" {
		t.Fatalf("expected to find copy_helper_view_slice body, got:\n%s", output)
	}
	if strings.Contains(body, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper_view_slice to avoid helper fallback, got:\n%s", body)
	}
	requireTinyExactDViewCopyBody(t, body)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewHolder:
	items: array[Views, 1]

@borrows_return_field(items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_helper_indexed(values: array[i32, 4]) -> void:
	wrapped: ViewHolder = wrap_indexed_views(values[0u:2u], values[2u:4u])
	arena_da_copy_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyHelperIndexedBody := functionIR(output, "copy_helper_indexed")
	if copyHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_helper_indexed to avoid helper fallback, got:\n%s", copyHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewHolder:
	items: array[Views, 1]

repr(c) struct NestedHolder:
	holder: ViewHolder

@borrows_return_field(holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_helper_indexed(values: array[i32, 4]) -> void:
	wrapped: NestedHolder = wrap_nested_indexed_views(values[0u:2u], values[2u:4u])
	arena_da_copy_exact(wrapped.holder.items[0u].left, wrapped.holder.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedHelperIndexedBody := functionIR(output, "copy_nested_helper_indexed")
	if copyNestedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_helper_indexed to avoid helper fallback, got:\n%s", copyNestedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedHelperIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_rebased_helper_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 2] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u])]
	wrapped: ViewWindow = wrap_sub(items[1u:2u], 0u, 1u)
	arena_da_copy_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyRebasedHelperIndexedBody := functionIR(output, "copy_rebased_helper_indexed")
	if copyRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyRebasedHelperIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> void:
	items: array[Views, 4] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u]), Views(values[4u:5u], values[5u:6u]), Views(values[6u:7u], values[7u:8u])]
	wrapped: ViewWindow = wrap_sub_wild(items[1u:3u], 0u, 2u)
	arena_da_copy_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyWildcardRebasedHelperIndexedBody := functionIR(output, "copy_wildcard_rebased_helper_indexed")
	if copyWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyWildcardRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyWildcardRebasedHelperIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> void:
	items: array[Views, 4] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u]), Views(values[4u:5u], values[5u:6u]), Views(values[6u:7u], values[7u:8u])]
	wrapped: Wrapper = wrap_submeta_wild(items[1u:3u], 0u, 2u)
	arena_da_copy_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedWildcardRebasedHelperIndexedBody := functionIR(output, "copy_nested_wildcard_rebased_helper_indexed")
	if copyNestedWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedWildcardRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyNestedWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedWildcardRebasedHelperIndexedBody)
}

func TestGenerateLLVMIRKeepsCopyOverlapGuardrailsThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_wildcard_rebased_overlap(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0u:3u], values[1u:4u]), Views(values[4u:7u], values[5u:8u])]
	wrapped: ViewWindow = wrap_sub_wild(items[0u:1u], 0u, 1u)
	arena_da_copy_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyWildcardRebasedOverlapBody := functionIR(output, "copy_wildcard_rebased_overlap")
	if copyWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find copy_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyWildcardRebasedOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", copyWildcardRebasedOverlapBody)
	}
	if strings.Contains(copyWildcardRebasedOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyWildcardRebasedOverlapBody)
	}
	if !strings.Contains(copyWildcardRebasedOverlapBody, "load i32, ptr") || !strings.Contains(copyWildcardRebasedOverlapBody, "store i32") {
		t.Fatalf("expected copy_wildcard_rebased_overlap to lower through direct element loads/stores, got:\n%s", copyWildcardRebasedOverlapBody)
	}
}

func TestGenerateLLVMIRKeepsCopyOverlapGuardrailsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_wildcard_rebased_overlap(values: array[i32, 8]) -> void:
	items: array[Views, 2] = [Views(values[0u:3u], values[1u:4u]), Views(values[4u:7u], values[5u:8u])]
	wrapped: Wrapper = wrap_submeta_wild(items[0u:1u], 0u, 1u)
	arena_da_copy_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedWildcardRebasedOverlapBody := functionIR(output, "copy_nested_wildcard_rebased_overlap")
	if copyNestedWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find copy_nested_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(copyNestedWildcardRebasedOverlapBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
	if strings.Contains(copyNestedWildcardRebasedOverlapBody, "call ptr @arena_memcpy") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to preserve overlap semantics instead of arena_memcpy, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
	if !strings.Contains(copyNestedWildcardRebasedOverlapBody, "load i32, ptr") || !strings.Contains(copyNestedWildcardRebasedOverlapBody, "store i32") {
		t.Fatalf("expected copy_nested_wildcard_rebased_overlap to lower through direct element loads/stores, got:\n%s", copyNestedWildcardRebasedOverlapBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_rebased_helper_indexed(values: array[i32, 4]) -> void:
	items: array[Views, 2] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u])]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	arena_da_copy_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyNestedRebasedHelperIndexedBody := functionIR(output, "copy_nested_rebased_helper_indexed")
	if copyNestedRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find copy_nested_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(copyNestedRebasedHelperIndexedBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_rebased_helper_indexed to avoid helper fallback, got:\n%s", copyNestedRebasedHelperIndexedBody)
	}
	requireTinyExactDViewCopyBody(t, copyNestedRebasedHelperIndexedBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewCopyExactThroughNestedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct NestedViews:
	inner: Views

@borrows_return_field(inner.left, left, inner.right, right)
extern wrap_nested_views(left: view[i32], right: view[i32]) -> NestedViews

def arena_da_copy_exact[T](dst: view[T], src: view[T]):
	_ = dst
	_ = src

def copy_nested_struct(values: array[i32, 4]) -> void:
	boxed: NestedViews = NestedViews(Views(values[0u:2u], values[2u:4u]))
	arena_da_copy_exact(boxed.inner.left, boxed.inner.right)

def copy_nested_helper(values: array[i32, 4]) -> void:
	boxed: NestedViews = wrap_nested_views(values[0u:2u], values[2u:4u])
	arena_da_copy_exact(boxed.inner.left, boxed.inner.right)
	`
	result := parseAndAnalyze(t, "backend_dview_copy_exact_nested_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copyStructBody := functionIR(output, "copy_nested_struct")
	if copyStructBody == "" {
		t.Fatalf("expected to find copy_nested_struct body, got:\n%s", output)
	}
	if strings.Contains(copyStructBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_struct to avoid helper fallback, got:\n%s", copyStructBody)
	}
	requireTinyExactDViewCopyBody(t, copyStructBody)

	copyHelperBody := functionIR(output, "copy_nested_helper")
	if copyHelperBody == "" {
		t.Fatalf("expected to find copy_nested_helper body, got:\n%s", output)
	}
	if strings.Contains(copyHelperBody, "call void @arena_da_copy_exact") {
		t.Fatalf("expected copy_nested_helper to avoid helper fallback, got:\n%s", copyHelperBody)
	}
	requireTinyExactDViewCopyBody(t, copyHelperBody)
}

func TestGenerateLLVMIRSpecializesArenaDViewZeroFill(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def zero_split(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	arena_da_fill(left, 0)

def fill_split(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	arena_da_fill(left, 7)

def fill_unknown(view: dview[i32]) -> void:
	arena_da_fill(view, 7)
`
	result := parseAndAnalyze(t, "backend_dview_fill_zero.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	zeroBody := functionIR(output, "zero_split")
	if zeroBody == "" {
		t.Fatalf("expected to find zero_split body, got:\n%s", output)
	}
	if strings.Contains(zeroBody, "call void @arena_da_fill") {
		t.Fatalf("expected zero_split to avoid generic helper fallback, got:\n%s", zeroBody)
	}
	if strings.Contains(zeroBody, "call ptr @memset(ptr") {
		t.Fatalf("expected zero_split to use direct zero stores on the tiny exact fast path, got:\n%s", zeroBody)
	}
	if strings.Count(zeroBody, "store i32 0, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected zero_split to lower through direct zero stores, got:\n%s", zeroBody)
	}

	fillBody := functionIR(output, "fill_split")
	if fillBody == "" {
		t.Fatalf("expected to find fill_split body, got:\n%s", output)
	}
	if strings.Contains(fillBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_split to avoid helper fallback for small exact extents, got:\n%s", fillBody)
	}
	if strings.Contains(fillBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_split to avoid memset specialization, got:\n%s", fillBody)
	}
	if !strings.Contains(fillBody, "store i32 7") {
		t.Fatalf("expected fill_split to lower through direct stores, got:\n%s", fillBody)
	}

	fillUnknownBody := functionIR(output, "fill_unknown")
	if fillUnknownBody == "" {
		t.Fatalf("expected to find fill_unknown body, got:\n%s", output)
	}
	if !strings.Contains(fillUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_unknown to keep helper fallback, got:\n%s", fillUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewRepeatedByteFill(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_bytes(values: any darray[u8, 4]&) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, 7u8)

def fill_all_ones(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	arena_da_fill(left, -1)

def fill_nonuniform(values: any darray[i32, 4]&) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	arena_da_fill(left, 7)

def fill_nonuniform_unknown(view: dview[i32]) -> void:
	arena_da_fill(view, 7)
`
	result := parseAndAnalyze(t, "backend_dview_fill_repeated_byte.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	byteBody := functionIR(output, "fill_bytes")
	if byteBody == "" {
		t.Fatalf("expected to find fill_bytes body, got:\n%s", output)
	}
	if strings.Contains(byteBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_bytes to avoid generic helper fallback, got:\n%s", byteBody)
	}
	if strings.Contains(byteBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", byteBody)
	}
	if strings.Count(byteBody, "store i8 7, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_bytes to lower through direct byte stores, got:\n%s", byteBody)
	}

	onesBody := functionIR(output, "fill_all_ones")
	if onesBody == "" {
		t.Fatalf("expected to find fill_all_ones body, got:\n%s", output)
	}
	if strings.Contains(onesBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_all_ones to avoid generic helper fallback, got:\n%s", onesBody)
	}
	if strings.Contains(onesBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_all_ones to use direct stores on the tiny exact fast path, got:\n%s", onesBody)
	}
	if strings.Count(onesBody, "store i32 -1, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_all_ones to lower through direct stores, got:\n%s", onesBody)
	}

	nonUniformBody := functionIR(output, "fill_nonuniform")
	if nonUniformBody == "" {
		t.Fatalf("expected to find fill_nonuniform body, got:\n%s", output)
	}
	if strings.Contains(nonUniformBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_nonuniform to avoid generic helper fallback, got:\n%s", nonUniformBody)
	}
	if strings.Contains(nonUniformBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_nonuniform to avoid memset specialization, got:\n%s", nonUniformBody)
	}
	if !strings.Contains(nonUniformBody, "store i32 7") {
		t.Fatalf("expected fill_nonuniform to lower through direct stores, got:\n%s", nonUniformBody)
	}

	nonUniformUnknownBody := functionIR(output, "fill_nonuniform_unknown")
	if nonUniformUnknownBody == "" {
		t.Fatalf("expected to find fill_nonuniform_unknown body, got:\n%s", output)
	}
	if !strings.Contains(nonUniformUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_nonuniform_unknown to keep helper fallback, got:\n%s", nonUniformUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewDynamicByteFill(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: any darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, value)

def fill_runtime_wide(values: any darray[i32, 4]&, value: i32) -> void:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	arena_da_fill(left, value)

def fill_runtime_wide_unknown(view: dview[i32], value: i32) -> void:
	arena_da_fill(view, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	runtimeByteBody := functionIR(output, "fill_runtime_byte")
	if runtimeByteBody == "" {
		t.Fatalf("expected to find fill_runtime_byte body, got:\n%s", output)
	}
	if strings.Contains(runtimeByteBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_byte to avoid generic helper fallback, got:\n%s", runtimeByteBody)
	}
	if strings.Contains(runtimeByteBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_byte to use direct byte stores on the tiny exact fast path, got:\n%s", runtimeByteBody)
	}
	if strings.Count(runtimeByteBody, "store i8 %value") < 2 {
		t.Fatalf("expected fill_runtime_byte to lower through direct runtime-value byte stores, got:\n%s", runtimeByteBody)
	}

	runtimeWideBody := functionIR(output, "fill_runtime_wide")
	if runtimeWideBody == "" {
		t.Fatalf("expected to find fill_runtime_wide body, got:\n%s", output)
	}
	if strings.Contains(runtimeWideBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_wide to avoid helper fallback for small exact extents, got:\n%s", runtimeWideBody)
	}
	if strings.Contains(runtimeWideBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_wide to avoid memset specialization, got:\n%s", runtimeWideBody)
	}
	if !strings.Contains(runtimeWideBody, "store i32 %1") && !strings.Contains(runtimeWideBody, "store i32 %0") {
		t.Fatalf("expected fill_runtime_wide to lower through direct runtime-value stores, got:\n%s", runtimeWideBody)
	}

	runtimeWideUnknownBody := functionIR(output, "fill_runtime_wide_unknown")
	if runtimeWideUnknownBody == "" {
		t.Fatalf("expected to find fill_runtime_wide_unknown body, got:\n%s", output)
	}
	if !strings.Contains(runtimeWideUnknownBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_wide_unknown to keep helper fallback, got:\n%s", runtimeWideUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewCoercedByteFill(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_literal_int_to_bytes(values: any darray[u8, 4]&) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, 7)

def fill_runtime_int_to_bytes(values: any darray[u8, 4]&, value: int) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_coerced_byte.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	literalBody := functionIR(output, "fill_literal_int_to_bytes")
	if literalBody == "" {
		t.Fatalf("expected to find fill_literal_int_to_bytes body, got:\n%s", output)
	}
	if strings.Contains(literalBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_literal_int_to_bytes to avoid generic helper fallback, got:\n%s", literalBody)
	}
	if strings.Contains(literalBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_literal_int_to_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", literalBody)
	}
	if strings.Count(literalBody, "store i8 7, ptr %dview.fill.elem.ptr") < 2 {
		t.Fatalf("expected fill_literal_int_to_bytes to lower through direct byte stores, got:\n%s", literalBody)
	}

	runtimeBody := functionIR(output, "fill_runtime_int_to_bytes")
	if runtimeBody == "" {
		t.Fatalf("expected to find fill_runtime_int_to_bytes body, got:\n%s", output)
	}
	if strings.Contains(runtimeBody, "call void @arena_da_fill") {
		t.Fatalf("expected fill_runtime_int_to_bytes to avoid generic helper fallback, got:\n%s", runtimeBody)
	}
	if strings.Contains(runtimeBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_int_to_bytes to use direct byte stores on the tiny exact fast path, got:\n%s", runtimeBody)
	}
	if strings.Count(runtimeBody, "store i8 %") < 2 {
		t.Fatalf("expected fill_runtime_int_to_bytes to lower through direct runtime-value byte stores, got:\n%s", runtimeBody)
	}
}

func TestGenerateOptimizedLLVMIRSupportsArenaDViewByteFillMemsetFastPath(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: any darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte_optimized.llcontext", src)
	output, err := backend.GenerateLLVMIRWithOpt(result, backend.OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.TrimSpace(output) == "" {
		t.Fatalf("expected optimized output to be non-empty")
	}
	if strings.Contains(output, "llvm.memset.p0.i64.invalid") {
		t.Fatalf("expected optimized output to avoid malformed llvm.memset intrinsic names, got:\n%s", output)
	}
}

func TestGenerateOptimizedLLVMObjectFileSupportsArenaDViewByteFillWithRuntimeMemsetDecl(t *testing.T) {
	src := `extern memset(dest: any void&, val: int, n: usize) -> any void&

repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_fill[T](dst: dview[T], value: T):
	_ = dst
	_ = value

def fill_runtime_byte(values: any darray[u8, 4]&, value: u8) -> void:
	base: dview[u8] = arena_da_view(values, 0u, 4u)
	left: dview[u8] = base[0u:2u]
	arena_da_fill(left, value)
`
	result := parseAndAnalyze(t, "backend_dview_fill_dynamic_byte_object.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "declare ptr @memset(ptr, i64, i64)") {
		t.Fatalf("expected runtime memset declaration to lower to an int-sized second argument, got:\n%s", output)
	}
	fillBody := functionIR(output, "fill_runtime_byte")
	if fillBody == "" {
		t.Fatalf("expected to find fill_runtime_byte body, got:\n%s", output)
	}
	if strings.Contains(fillBody, "call ptr @memset(ptr") {
		t.Fatalf("expected fill_runtime_byte to use direct byte stores on the tiny exact fast path, got:\n%s", fillBody)
	}
	if strings.Count(fillBody, "store i8 %value") < 2 {
		t.Fatalf("expected fill_runtime_byte to lower through direct runtime-value byte stores, got:\n%s", fillBody)
	}

	outputPath := filepath.Join(t.TempDir(), "fill_runtime_byte.o")
	if err := backend.WriteLLVMObjectFileWithOpt(result, outputPath, backend.OptimizationLevel3); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOpt returned error: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected optimized object file at %s: %v", outputPath, err)
	}
}

func TestGenerateOptimizedLLVMObjectFileSkipsRedundantPackedZeroPayloadStores(t *testing.T) {
	src := `repr(c) struct Payload:
	data: mutable any u8&?
	len: mutable i32

packed enum Node:
	Empty(Payload)
	Byte(u8)

def build() -> Node:
	region scratch(256u)
	store: Node.Store[Local] = Node.Store(scratch)
	return new[store] Node.Empty(zeroed)
`
	result := parseAndAnalyze(t, "backend_packed_zero_payload_object.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	buildBody := functionIR(output, "build")
	if buildBody == "" {
		t.Fatalf("expected to find build body, got:\n%s", output)
	}
	if strings.Contains(buildBody, "store %Payload zeroinitializer, ptr %enum.payload.ptr") {
		t.Fatalf("expected packed zero payload constructor to avoid redundant aggregate zero stores into enum payload storage, got:\n%s", buildBody)
	}

	outputPath := filepath.Join(t.TempDir(), "packed_zero_payload.o")
	if err := backend.WriteLLVMObjectFileWithOpt(result, outputPath, backend.OptimizationLevel3); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOpt returned error: %v", err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected optimized object file at %s: %v", outputPath, err)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExact(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_eq_exact[T](left: dview[T], right: dview[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_split(values: any darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	right: dview[i32] = base[2u:4u]
	return arena_da_eq_exact(left, right)

def eq_overlap(values: any darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:3u]
	right: dview[i32] = base[1u:4u]
	return arena_da_eq_exact(left, right)

def eq_same(values: any darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	return arena_da_eq_exact(base, base)

def eq_diff_extent(values: any darray[i32, 4]&) -> bool:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:1u]
	right: dview[i32] = base[2u:4u]
	return arena_da_eq_exact(left, right)
`
	result := parseAndAnalyze(t, "backend_dview_eq_exact.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqSplitBody := functionIR(output, "eq_split")
	if eqSplitBody == "" {
		t.Fatalf("expected to find eq_split body, got:\n%s", output)
	}
	if strings.Contains(eqSplitBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_split to avoid helper fallback, got:\n%s", eqSplitBody)
	}
	requireTinyExactDViewEqBody(t, eqSplitBody, true)

	eqOverlapBody := functionIR(output, "eq_overlap")
	if eqOverlapBody == "" {
		t.Fatalf("expected to find eq_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_overlap to avoid helper fallback, got:\n%s", eqOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqOverlapBody, false)

	eqSameBody := functionIR(output, "eq_same")
	if eqSameBody == "" {
		t.Fatalf("expected to find eq_same body, got:\n%s", output)
	}
	if strings.Contains(eqSameBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_same to avoid helper fallback, got:\n%s", eqSameBody)
	}
	requireTinyExactDViewEqBody(t, eqSameBody, false)

	eqDiffExtentBody := functionIR(output, "eq_diff_extent")
	if eqDiffExtentBody == "" {
		t.Fatalf("expected to find eq_diff_extent body, got:\n%s", output)
	}
	if !strings.Contains(eqDiffExtentBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_diff_extent to keep helper fallback, got:\n%s", eqDiffExtentBody)
	}
	if strings.Contains(eqDiffExtentBody, "call i64 @memcmp(ptr noalias") {
		t.Fatalf("expected eq_diff_extent to avoid direct memcmp specialization, got:\n%s", eqDiffExtentBody)
	}
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

@borrows_return_field(left, left, right, right)
extern wrap_views(left: view[i32], right: view[i32]) -> Views

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_struct(values: array[i32, 4]) -> bool:
	boxed: Views = Views(values[0u:2u], values[2u:4u])
	return arena_da_eq_exact(boxed.left, boxed.right)

def eq_helper(values: array[i32, 4]) -> bool:
	boxed: Views = wrap_views(values[0u:2u], values[2u:4u])
	return arena_da_eq_exact(boxed.left, boxed.right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqStructBody := functionIR(output, "eq_struct")
	if eqStructBody == "" {
		t.Fatalf("expected to find eq_struct body, got:\n%s", output)
	}
	if strings.Contains(eqStructBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_struct to avoid helper fallback, got:\n%s", eqStructBody)
	}
	requireTinyExactDViewEqBody(t, eqStructBody, true)

	eqHelperBody := functionIR(output, "eq_helper")
	if eqHelperBody == "" {
		t.Fatalf("expected to find eq_helper body, got:\n%s", output)
	}
	if strings.Contains(eqHelperBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_helper to avoid helper fallback, got:\n%s", eqHelperBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperBody, true)

}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 1] = [Views(values[0u:2u], values[2u:4u])]
	return arena_da_eq_exact(items[0u].left, items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqIndexedBody := functionIR(output, "eq_indexed")
	if eqIndexedBody == "" {
		t.Fatalf("expected to find eq_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_indexed to avoid helper fallback, got:\n%s", eqIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewHolder:
	items: array[Views, 1]

@borrows_return_field(items[0].left, left, items[0].right, right)
extern wrap_indexed_views(left: view[i32], right: view[i32]) -> ViewHolder

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_helper_indexed(values: array[i32, 4]) -> bool:
	wrapped: ViewHolder = wrap_indexed_views(values[0u:2u], values[2u:4u])
	return arena_da_eq_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqHelperIndexedBody := functionIR(output, "eq_helper_indexed")
	if eqHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_helper_indexed to avoid helper fallback, got:\n%s", eqHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewHolder:
	items: array[Views, 1]

repr(c) struct NestedHolder:
	holder: ViewHolder

@borrows_return_field(holder.items[0].left, left, holder.items[0].right, right)
extern wrap_nested_indexed_views(left: view[i32], right: view[i32]) -> NestedHolder

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_helper_indexed(values: array[i32, 4]) -> bool:
	wrapped: NestedHolder = wrap_nested_indexed_views(values[0u:2u], values[2u:4u])
	return arena_da_eq_exact(wrapped.holder.items[0u].left, wrapped.holder.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedHelperIndexedBody := functionIR(output, "eq_nested_helper_indexed")
	if eqNestedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_helper_indexed to avoid helper fallback, got:\n%s", eqNestedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedHelperIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items, src)
extern wrap_sub(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_rebased_helper_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 2] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u])]
	wrapped: ViewWindow = wrap_sub(items[1u:2u], 0u, 1u)
	return arena_da_eq_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqRebasedHelperIndexedBody := functionIR(output, "eq_rebased_helper_indexed")
	if eqRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqRebasedHelperIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> bool:
	items: array[Views, 4] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u]), Views(values[4u:5u], values[5u:6u]), Views(values[6u:7u], values[7u:8u])]
	wrapped: ViewWindow = wrap_sub_wild(items[1u:3u], 0u, 2u)
	return arena_da_eq_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqWildcardRebasedHelperIndexedBody := functionIR(output, "eq_wildcard_rebased_helper_indexed")
	if eqWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqWildcardRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqWildcardRebasedHelperIndexedBody, true)
}

func TestGenerateLLVMIRKeepsOverlapGuardrailsThroughWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct ViewWindow:
	items: view[Views]

@borrows_return_field_rebased(items[*].left, src[*].left, items[*].right, src[*].right)
extern wrap_sub_wild(src: view[Views], start: usize, end: usize) -> ViewWindow

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_wildcard_rebased_overlap(values: array[i32, 8]) -> bool:
	items: array[Views, 2] = [Views(values[0u:3u], values[1u:4u]), Views(values[4u:7u], values[5u:8u])]
	wrapped: ViewWindow = wrap_sub_wild(items[0u:1u], 0u, 1u)
	return arena_da_eq_exact(wrapped.items[0u].left, wrapped.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqWildcardRebasedOverlapBody := functionIR(output, "eq_wildcard_rebased_overlap")
	if eqWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find eq_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqWildcardRebasedOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", eqWildcardRebasedOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqWildcardRebasedOverlapBody, false)
}

func TestGenerateLLVMIRKeepsOverlapGuardrailsThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_wildcard_rebased_overlap(values: array[i32, 8]) -> bool:
	items: array[Views, 2] = [Views(values[0u:3u], values[1u:4u]), Views(values[4u:7u], values[5u:8u])]
	wrapped: Wrapper = wrap_submeta_wild(items[0u:1u], 0u, 1u)
	return arena_da_eq_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_wildcard_rebased_helper_indexed_overlap_guardrails.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedWildcardRebasedOverlapBody := functionIR(output, "eq_nested_wildcard_rebased_overlap")
	if eqNestedWildcardRebasedOverlapBody == "" {
		t.Fatalf("expected to find eq_nested_wildcard_rebased_overlap body, got:\n%s", output)
	}
	if strings.Contains(eqNestedWildcardRebasedOverlapBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_wildcard_rebased_overlap to avoid helper fallback, got:\n%s", eqNestedWildcardRebasedOverlapBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedWildcardRebasedOverlapBody, false)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_rebased_helper_indexed(values: array[i32, 4]) -> bool:
	items: array[Views, 2] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u])]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	return arena_da_eq_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedRebasedHelperIndexedBody := functionIR(output, "eq_nested_rebased_helper_indexed")
	if eqNestedRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqNestedRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedRebasedHelperIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedWildcardRebasedHelperReturnedIndexedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct Meta:
	items: view[Views]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].left, src[*].left, meta.items[*].right, src[*].right)
extern wrap_submeta_wild(src: view[Views], start: usize, end: usize) -> Wrapper

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_wildcard_rebased_helper_indexed(values: array[i32, 8]) -> bool:
	items: array[Views, 4] = [Views(values[0u:1u], values[1u:2u]), Views(values[2u:3u], values[3u:4u]), Views(values[4u:5u], values[5u:6u]), Views(values[6u:7u], values[7u:8u])]
	wrapped: Wrapper = wrap_submeta_wild(items[1u:3u], 0u, 2u)
	return arena_da_eq_exact(wrapped.meta.items[0u].left, wrapped.meta.items[0u].right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_wildcard_rebased_helper_indexed_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqNestedWildcardRebasedHelperIndexedBody := functionIR(output, "eq_nested_wildcard_rebased_helper_indexed")
	if eqNestedWildcardRebasedHelperIndexedBody == "" {
		t.Fatalf("expected to find eq_nested_wildcard_rebased_helper_indexed body, got:\n%s", output)
	}
	if strings.Contains(eqNestedWildcardRebasedHelperIndexedBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_wildcard_rebased_helper_indexed to avoid helper fallback, got:\n%s", eqNestedWildcardRebasedHelperIndexedBody)
	}
	requireTinyExactDViewEqBody(t, eqNestedWildcardRebasedHelperIndexedBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewEqExactThroughNestedFieldProjection(t *testing.T) {
	src := `repr(c) struct Views:
	left: view[i32]
	right: view[i32]

repr(c) struct NestedViews:
	inner: Views

@borrows_return_field(inner.left, left, inner.right, right)
extern wrap_nested_views(left: view[i32], right: view[i32]) -> NestedViews

def arena_da_eq_exact[T](left: view[T], right: view[T]) -> bool:
	_ = left
	_ = right
	return false

def eq_nested_struct(values: array[i32, 4]) -> bool:
	boxed: NestedViews = NestedViews(Views(values[0u:2u], values[2u:4u]))
	return arena_da_eq_exact(boxed.inner.left, boxed.inner.right)

def eq_nested_helper(values: array[i32, 4]) -> bool:
	boxed: NestedViews = wrap_nested_views(values[0u:2u], values[2u:4u])
	return arena_da_eq_exact(boxed.inner.left, boxed.inner.right)
	`
	result := parseAndAnalyze(t, "backend_dview_eq_exact_nested_field_projection.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	eqStructBody := functionIR(output, "eq_nested_struct")
	if eqStructBody == "" {
		t.Fatalf("expected to find eq_nested_struct body, got:\n%s", output)
	}
	if strings.Contains(eqStructBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_struct to avoid helper fallback, got:\n%s", eqStructBody)
	}
	requireTinyExactDViewEqBody(t, eqStructBody, true)

	eqHelperBody := functionIR(output, "eq_nested_helper")
	if eqHelperBody == "" {
		t.Fatalf("expected to find eq_nested_helper body, got:\n%s", output)
	}
	if strings.Contains(eqHelperBody, "call i1 @arena_da_eq_exact") {
		t.Fatalf("expected eq_nested_helper to avoid helper fallback, got:\n%s", eqHelperBody)
	}
	requireTinyExactDViewEqBody(t, eqHelperBody, true)
}

func TestGenerateLLVMIRSpecializesArenaDViewMaterialize(t *testing.T) {
	src := `repr(c) struct Arena:
	begin: mutable heap void&?
	end: mutable heap void&?
	end_index: mutable usize

repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&], values.count, sizeof(T))
	return DynArrayView(null, 0u, sizeof(T))

def arena_da_from_view[T](a: any Arena&, view: dview[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	out: darray[T, shape_out] = zeroed
	return out

def materialize_split(a: any Arena&, values: any darray[i32, 4]&) -> darray[i32]:
	base: dview[i32] = arena_da_view(values, 0u, 4u)
	left: dview[i32] = base[0u:2u]
	return arena_da_from_view(a, left)

def materialize_unknown(a: any Arena&, view: dview[i32]) -> darray[i32]:
	return arena_da_from_view(a, view)
`
	result := parseAndAnalyze(t, "backend_dview_materialize.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	materializeSplitBody := functionIR(output, "materialize_split")
	if materializeSplitBody == "" {
		t.Fatalf("expected to find materialize_split body, got:\n%s", output)
	}
	if strings.Contains(materializeSplitBody, "call %DynArray__i32 @arena_da_from_view") {
		t.Fatalf("expected materialize_split to avoid helper fallback, got:\n%s", materializeSplitBody)
	}
	requireTinyExactDViewMaterializeBody(t, materializeSplitBody)

	materializeUnknownBody := functionIR(output, "materialize_unknown")
	if materializeUnknownBody == "" {
		t.Fatalf("expected to find materialize_unknown body, got:\n%s", output)
	}
	if !strings.Contains(materializeUnknownBody, "call %DynArray__i32 @arena_da_from_view") {
		t.Fatalf("expected materialize_unknown to keep helper fallback when extent is not exact, got:\n%s", materializeUnknownBody)
	}
}

func TestGenerateLLVMIRSpecializesStringViewMaterialize(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

extern memcpy(dest: any void&?, src: any void&?, n: usize) -> any void&?
extern alloc_perm(size: i64) -> heap void&
extern register_perm_string_len(ptr: any u8&?, len: usize)
extern intern_small_string(src: any u8&, len: usize) -> heap u8&

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	src: any u8& = value if value != null else "".cast[any u8&]
	_ = start
	return StringView(src, end)

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def string_view_copy(view: StringView) -> heap u8&:
	_ = view
	return intern_small_string("".cast[any u8&], 0u)

def ctx_string_from_view(view: StringView) -> dstr[shape_out]:
	return string_view_copy(view)

def copy_small(text: dstr[row]) -> dstr:
	view: StringView = ctx_string_view(text, 0, 2)
	return ctx_string_from_view(view)

def copy_large(text: dstr[row]) -> dstr:
	view: StringView = ctx_string_view(text, 0, 12)
	return ctx_string_from_view(view)

def copy_unknown(view: StringView) -> dstr:
	return ctx_string_from_view(view)

def copy_small_raw(text: dstr[row]) -> heap u8&:
	view: StringView = ctx_string_view(text, 0, 2)
	return string_view_copy(view)
`
	result := parseAndAnalyze(t, "backend_string_view_materialize.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySmallBody := functionIR(output, "copy_small")
	if copySmallBody == "" {
		t.Fatalf("expected to find copy_small body, got:\n%s", output)
	}
	if strings.Contains(copySmallBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_small to avoid ctx_string_from_view helper fallback, got:\n%s", copySmallBody)
	}
	if !strings.Contains(copySmallBody, "call ptr @intern_small_string(ptr") {
		t.Fatalf("expected copy_small to lower through intern_small_string, got:\n%s", copySmallBody)
	}

	copyLargeBody := functionIR(output, "copy_large")
	if copyLargeBody == "" {
		t.Fatalf("expected to find copy_large body, got:\n%s", output)
	}
	for _, check := range []string{"call ptr @alloc_perm(i64 13)", "call ptr @memcpy(ptr", "call void @register_perm_string_len(ptr"} {
		if !strings.Contains(copyLargeBody, check) {
			t.Fatalf("expected copy_large to contain %q, got:\n%s", check, copyLargeBody)
		}
	}
	if strings.Contains(copyLargeBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_large to avoid ctx_string_from_view helper fallback, got:\n%s", copyLargeBody)
	}

	copyUnknownBody := functionIR(output, "copy_unknown")
	if copyUnknownBody == "" {
		t.Fatalf("expected to find copy_unknown body, got:\n%s", output)
	}
	if !strings.Contains(copyUnknownBody, "call ptr @ctx_string_from_view") {
		t.Fatalf("expected copy_unknown to keep helper fallback when extent is not exact, got:\n%s", copyUnknownBody)
	}

	copySmallRawBody := functionIR(output, "copy_small_raw")
	if copySmallRawBody == "" {
		t.Fatalf("expected to find copy_small_raw body, got:\n%s", output)
	}
	if strings.Contains(copySmallRawBody, "call ptr @string_view_copy") {
		t.Fatalf("expected copy_small_raw to avoid string_view_copy helper fallback, got:\n%s", copySmallRawBody)
	}
	if !strings.Contains(copySmallRawBody, "call ptr @intern_small_string(ptr") {
		t.Fatalf("expected copy_small_raw to lower through intern_small_string, got:\n%s", copySmallRawBody)
	}
}

func TestGenerateLLVMIRSpecializesStringViewLiteralWrapperCalls(t *testing.T) {
	src := `extern string_view_eq(view: StringView, other: any u8&?) -> int

def frontend_sv_eq_literal(view: StringView, literal: static u8&) -> bool:
	return string_view_eq(view, literal.cast[any u8&]) != 0

def same_short(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "def")

def same_long(view: StringView) -> bool:
	return frontend_sv_eq_literal(view, "destroy_region")
`
	result := parseAndAnalyze(t, "backend_runtime_string_literal_wrapper_eq.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @same_short(%StringView",
		"define i1 @same_long(%StringView",
		"declare i64 @memcmp(ptr, ptr, i64)",
		"call i64 @memcmp(ptr",
		"load i8, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call i1 @frontend_sv_eq_literal") {
		t.Fatalf("expected wrapper literal lowering to inline away frontend_sv_eq_literal at call sites, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersDStrLenFieldViaRuntimeHelper(t *testing.T) {
	src := `def text_len(text: dstr[row]) -> i64:
    return text.len
`
	result := parseAndAnalyze(t, "backend_dstr_len.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @text_len(ptr",
		"declare i64 @ctx_strlen(ptr)",
		"call i64 @ctx_strlen(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersDArrayViewRuntimeFields(t *testing.T) {
	src := `def non_empty[T](view: dview[T]) -> bool:
	return view.len > 0u and view.elem_size > 0u

def probe(view: dview[i64]) -> bool:
	return non_empty(view)
`
	result := parseAndAnalyze(t, "backend_darray_view_runtime_fields.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i1 @non_empty__i64(%DynArrayView",
		"getelementptr inbounds nuw %DynArrayView",
		"icmp ugt i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersArraySliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
    len: mutable usize
    elem_size: mutable usize

def head_owned(values: darray[i32, row]) -> i32:
	part: dview[i32] = values[1u:3u]
    return part[0u]

def head_view(view: dview[i32]) -> i32:
	part: dview[i32] = view[0u:1u]
    return part[0u]
`
	result := parseAndAnalyze(t, "backend_array_slice_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64, i64 }",
		"define i32 @head_owned(%DynArray__i32",
		"define i32 @head_view(%DynArrayView",
		"declare %DynArrayView @arena_da_view(ptr, i64, i64)",
		"declare %DynArrayView @arena_da_view_slice(%DynArrayView, i64, i64)",
		"call %DynArrayView @arena_da_view(ptr",
		"call %DynArrayView @arena_da_view_slice(%DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFixedArraySliceSyntaxWithoutRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def slice_owned(values: i32[4]) -> view[i32]:
	return values[1u:3u]

def head_ref(values: any i32[4]&) -> i32:
	return values[1u:3u][0u]
`
	result := parseAndAnalyze(t, "backend_fixed_array_slice.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArrayView = type { ptr, i64, i64 }",
		"define %DynArrayView @slice_owned([4 x i32]",
		"define i32 @head_ref(ptr",
		"getelementptr [4 x i32], ptr",
		"insertvalue %DynArrayView",
		"getelementptr i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@arena_da_view") || strings.Contains(output, "@arena_da_view_slice") {
		t.Fatalf("expected fixed-array slicing not to depend on dynamic array runtime helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersNestedCollectionAccessOnReturnedValues(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
    count: mutable usize
    capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
    len: mutable usize
    elem_size: mutable usize

extern make_array() -> darray[i32, row]
extern make_array_view() -> dview[i32]

def read_array_index() -> i32:
    return make_array()[1u]

def read_array_slice_index() -> i32:
    return make_array()[1u:3u][0u]

def read_array_view_index() -> i32:
    return make_array_view()[0u]
`
	result := parseAndAnalyze(t, "backend_nested_collection_access_returns.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare %DynArray__i32 @make_array()",
		"declare %DynArrayView @make_array_view()",
		"call %DynArray__i32 @make_array()",
		"call %DynArrayView @make_array_view()",
		"call %DynArrayView @arena_da_view(ptr",
		"getelementptr i32, ptr",
		"alloca %DynArray__i32",
		"alloca %DynArrayView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersArrayLiteralAndInferredLocalViaFixedArrayLowering(t *testing.T) {
	src := `def head_of_middle() -> int:
	values = [1, 2, 3, 4]
	part: view[int] = values[1:3]
	return part[0]
`
	result := parseAndAnalyze(t, "backend_array_literal_inferred_local.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @head_of_middle()",
		"alloca [4 x i64]",
		"getelementptr [4 x i64], ptr",
		"insertvalue %DynArrayView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "@rt_tlist_new") || strings.Contains(output, "@rt_tlist_push") {
		t.Fatalf("expected fixed array literals not to lower through typed-list runtime helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersStringSliceSyntaxViaRuntimeHelpers(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
    len: mutable i64

def head_codepoint(text: dstr[row]) -> char:
	view: StringView = text[1:3]
    return view[0]
`
	result := parseAndAnalyze(t, "backend_string_slice_syntax.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"declare %StringView @ctx_string_view(ptr, i64, i64)",
		"call %StringView @ctx_string_view(ptr",
		"call i64 @ctx_string_view_index(%StringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRSpecializesExactStringSliceMaterialize(t *testing.T) {
	src := `extern memcpy(dest: any void&?, src: any void&?, n: usize) -> any void&?
extern alloc_perm(size: i64) -> heap void&
extern register_perm_string_len(ptr: any u8&?, len: usize)
extern intern_small_string(src: any u8&, len: usize) -> heap u8&
extern ctx_strlen(value: dstr[shape_in]) -> i64
extern ctx_string_slice(value: dstr[shape_in], start: i64, end: i64) -> dstr[shape_out]

def copy_small(text: dstr[row]) -> dstr:
	return ctx_string_slice(text, 1, 3)

def copy_large(text: dstr[row]) -> dstr:
	return ctx_string_slice(text, 1, 13)

def copy_full(text: dstr[row]) -> dstr:
	return ctx_string_slice(text, 0, text.len)

def copy_unknown(text: dstr[row], start: i64, end: i64) -> dstr:
	return ctx_string_slice(text, start, end)
`
	result := parseAndAnalyze(t, "backend_exact_string_slice_materialize.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	copySmallBody := functionIR(output, "copy_small")
	if copySmallBody == "" {
		t.Fatalf("expected to find copy_small body, got:\n%s", output)
	}
	if strings.Contains(copySmallBody, "call ptr @ctx_string_slice") {
		t.Fatalf("expected copy_small to avoid ctx_string_slice helper fallback, got:\n%s", copySmallBody)
	}
	if !strings.Contains(copySmallBody, "call ptr @intern_small_string(ptr") {
		t.Fatalf("expected copy_small to lower through intern_small_string, got:\n%s", copySmallBody)
	}

	copyLargeBody := functionIR(output, "copy_large")
	if copyLargeBody == "" {
		t.Fatalf("expected to find copy_large body, got:\n%s", output)
	}
	for _, check := range []string{"call ptr @alloc_perm(i64 13)", "call ptr @memcpy(ptr", "call void @register_perm_string_len(ptr"} {
		if !strings.Contains(copyLargeBody, check) {
			t.Fatalf("expected copy_large to contain %q, got:\n%s", check, copyLargeBody)
		}
	}
	if strings.Contains(copyLargeBody, "call ptr @ctx_string_slice") {
		t.Fatalf("expected copy_large to avoid ctx_string_slice helper fallback, got:\n%s", copyLargeBody)
	}

	copyFullBody := functionIR(output, "copy_full")
	if copyFullBody == "" {
		t.Fatalf("expected to find copy_full body, got:\n%s", output)
	}
	for _, bad := range []string{"call ptr @ctx_string_slice", "call ptr @alloc_perm", "call ptr @intern_small_string", "call ptr @memcpy"} {
		if strings.Contains(copyFullBody, bad) {
			t.Fatalf("expected copy_full to lower as a direct return without %q, got:\n%s", bad, copyFullBody)
		}
	}
	if strings.Contains(copyFullBody, "call ") {
		t.Fatalf("expected copy_full to avoid all helper calls on the full-span fast path, got:\n%s", copyFullBody)
	}

	copyUnknownBody := functionIR(output, "copy_unknown")
	if copyUnknownBody == "" {
		t.Fatalf("expected to find copy_unknown body, got:\n%s", output)
	}
	if !strings.Contains(copyUnknownBody, "call ptr @ctx_string_slice(ptr") {
		t.Fatalf("expected copy_unknown to keep helper fallback when extent is not exact, got:\n%s", copyUnknownBody)
	}
}

func TestGenerateLLVMIRLowersStaticIfInFunctionBodies(t *testing.T) {
	src := `const ENABLE_FAST = 2 > 1

def choose() -> i32:
    static if ENABLE_FAST:
        return 7
	static else:
        return 9
`
	result := parseAndAnalyze(t, "backend_static_if.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @choose()",
		"ret i32 7",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "ret i32 9") {
		t.Fatalf("expected inactive static-if branch to be omitted, got:\n%s", output)
	}
	if strings.Contains(output, "br i1") {
		t.Fatalf("expected static-if lowering not to emit a runtime conditional branch, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersForLoopRanges(t *testing.T) {
	src := `def sum(limit: int) -> int:
	total: mutable int = 0
	for i in 0..<limit:
		total <- total + i
	for j in limit..>0:
		total <- total + j
	for k in 0..4..2:
		total <- total + k
	return total
`
	result := parseAndAnalyze(t, "backend_for_loop_ranges.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	ir := functionIR(output, "sum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for sum, got:\n%s", output)
	}
	for _, check := range []string{
		"define i64 @sum(i64",
		"for.cond",
		"for.body",
		"for.end",
		"for.next.asc",
		"for.next.desc",
		"select i1",
		"icmp slt",
		"icmp sgt",
		"icmp sle",
		"add i64",
		"sub i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected sum IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func TestGenerateLLVMIRLowersIterableForLoopDestructuring(t *testing.T) {
	src := `struct Pair:
	left: int
	right: int

def sum_pairs(items: array[Pair, 2]) -> int:
	total: mutable int = 0
	for Pair(left, right) in items:
		total <- total + left + right
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_destructure.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "sum_pairs")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for sum_pairs, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"extractvalue %Pair",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected sum_pairs IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func TestGenerateLLVMIRLowersIterableForLoopMutableRef(t *testing.T) {
	src := `struct Counter:
	value: mutable int

def bump() -> int:
	items: mutable array[Counter, 2] = [Counter(1), Counter(2)]
	for mutable ref item in items:
		item.value <- item.value + 1
	return items[0].value + items[1].value
`
	result := parseAndAnalyze(t, "backend_iterable_for_mutable_ref.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "bump")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for bump, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"iter.next",
		"getelementptr inbounds nuw %Counter",
		"store i64",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected bump IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func TestGenerateLLVMIRLowersIterableForLoopOverDynamicString(t *testing.T) {
	src := `def checksum(text: dstr[row]) -> int:
	total: mutable int = 0
	for ch in text:
		total <- total + ch
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_dstr.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	ir := functionIR(output, "checksum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for checksum, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"declare i64 @ctx_string_index",
		"add i64",
	} {
		if !strings.Contains(output, check) && !strings.Contains(ir, check) {
			t.Fatalf("expected iterable string lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersIterableForLoopOverChunksExactView(t *testing.T) {
	src := `def checksum(values: darray[i32, 4]) -> i32:
	base: dview[i32] = values[0u:4u]
	chunks: ChunksExactView[i32] = chunks_exact(readonly(base), 2u)
	total: mutable i32 = 0
	for chunk in chunks:
		total <- total + chunk[0u] + chunk[1u]
	return total
`
	result := parseAndAnalyze(t, "backend_iterable_for_chunks_exact.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if !strings.Contains(output, "%ChunksExactView__i32 = type { %DynArrayView, i64, i64 }") {
		t.Fatalf("expected output to declare the ChunksExactView carrier type, got:\n%s", output)
	}
	ir := functionIR(output, "checksum")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for checksum, got:\n%s", output)
	}
	for _, check := range []string{
		"iter.cond",
		"iter.body",
		"iter.end",
		"mul i64",
		"call %DynArrayView @arena_da_view_slice(%DynArrayView",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected checksum IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func TestGenerateLLVMIRLowersProofCarryingViewHelpers(t *testing.T) {
	src := `def run(values: darray[i32, 4]) -> void:
	base: dview[i32] = values[0u:4u]
	halves: SplitView[i32] = split_at(base, 2u)
	left: dview[i32] = halves.left
	chunks: ChunksExactView[i32] = chunks_exact(readonly(base), 2u)
	first: dview[i32] = chunks[0u]
	_ = left
	_ = first
`
	result := parseAndAnalyze(t, "backend_proof_carrying_view_helpers.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%SplitView__i32 = type { %DynArrayView, %DynArrayView }",
		"%ChunksExactView__i32 = type { %DynArrayView, i64, i64 }",
		"define void @run(%DynArray__i32",
		"call %DynArrayView @arena_da_view_slice(%DynArrayView",
		"urem i64",
		"call void @llvm.trap()",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersReduceSumHelper(t *testing.T) {
	src := `def add_bias(value: i64, bias: i64) -> i64:
	return value + bias

def run(values: darray[i64, 4], bias: i64) -> i64:
	base: dview[i64] = values[0u:4u]
	return reduce_sum(readonly(base), add_bias, bias)
`
	result := parseAndAnalyze(t, "backend_reduce_sum_helper.llcontext", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	ir := functionIR(output, "run")
	if ir == "" {
		t.Fatalf("expected to find LLVM IR for run, got:\n%s", output)
	}
	for _, check := range []string{
		"define i64 @run(%DynArray__i64",
		"reduce_sum.cond",
		"reduce_sum.body",
		"reduce_sum.end",
		"call i64 @add_bias",
		"add i64",
	} {
		if !strings.Contains(ir, check) {
			t.Fatalf("expected run IR to contain %q, got:\n%s", check, ir)
		}
	}
}

func looksLikeObjectFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magics := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xfe, 0xed, 0xfa, 0xce},
	}
	for _, magic := range magics {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}

func looksLikeBitcodeFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.HasPrefix(data, []byte{'B', 'C'}) || bytes.Equal(data[:4], []byte{0xde, 0xc0, 0x17, 0x0b})
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
