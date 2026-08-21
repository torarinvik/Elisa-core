package backend_test

import (
	"elisacore/src/backend"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
		t.Fatalf("expected tiny exact view copy to avoid arena_memcpy, got:\n%s", body)
	}
	requireInstructionLineContainsAll(t, body, "load i32, ptr %view.copy.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, body, "store i32 %view.copy.elem, ptr %view.copy.dst.elem.ptr", "!alias.scope", "!noalias")
}
func requireTinyExactDViewEqBody(t *testing.T, body string, expectAliasMetadata bool) {
	t.Helper()
	if strings.Contains(body, "call i64 @memcmp(") || strings.Contains(body, "call i32 @memcmp(") {
		t.Fatalf("expected tiny exact view equality to avoid memcmp, got:\n%s", body)
	}
	if !strings.Contains(body, "view.eq.byte.eq = icmp eq i8") {
		t.Fatalf("expected tiny exact view equality to compare bytes directly, got:\n%s", body)
	}
	if expectAliasMetadata {
		requireInstructionLineContainsAll(t, body, "view.eq.left.byte = load i8", "!alias.scope", "!noalias")
		requireInstructionLineContainsAll(t, body, "view.eq.right.byte = load i8", "!alias.scope", "!noalias")
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
	requireInstructionLineContainsAll(t, body, "load i32, ptr %view.materialize.src.elem.ptr", "!alias.scope", "!noalias")
	requireInstructionLineContainsAll(t, body, "store i32 %view.materialize.elem, ptr %view.materialize.dst.elem.ptr", "!alias.scope", "!noalias")
	if !strings.Contains(body, "view.materialize.items") {
		t.Fatalf("expected tiny exact arena_da_from_view to still materialize the darray result, got:\n%s", body)
	}
}
func TestGenerateLLVMIRDefinesSimpleFunctionBody(t *testing.T) {
	src := `struct Box:
    value: i32

extern alloc_box() -> Box&
extern take_box(box: Box) -> void
extern errno_value: i32

def read_box(box: Box&) -> i32:
    return box.value
`
	result := parseAndAnalyze(t, "backend_box.elisa", src)
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
	src := `struct ScratchPair:
	left: mutable int
	right: mutable int


struct ScratchHolder:
	pair: mutable ScratchPair


def make_holder() -> ScratchHolder:
		return ScratchHolder{pair: ScratchPair{left: 8, right: 9}}
`
	result := parseAndAnalyze(t, "backend_nested_struct_literals.elisa", src)
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
	result := parseAndAnalyze(t, "backend_aggregate_state_struct.elisa", src)
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
	result := parseAndAnalyze(t, "backend_aggregate_state_struct_multi.elisa", src)
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
	src := `struct Pair:
    left: mutable i64
    right: mutable i64

def sum(pair: Pair) -> i64:
    move pair as Pair(left, right)
    return left + right
`
	result := parseAndAnalyze(t, "backend_move_as_struct.elisa", src)
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
		"llvm.sadd.with.overflow.i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
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
	result := parseAndAnalyze(t, "backend_control_flow.elisa", src)
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
	src := `extern mutex_lock(mu: Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void

def lock_then_return(mu: mutable Mutex) -> i64:
    lock mu as g:
        return 7

def lock_then_fallthrough(mu: mutable Mutex) -> void:
    lock mu as g:
        pass
`
	result := parseAndAnalyze(t, "backend_lock_scope.elisa", src)
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
extern pool_shutdown(pool: ThreadPool&) -> void
extern mutex_lock(mu: Mutex&) -> MutexGuard[Held]
extern mutex_unlock(g: MutexGuard[Held]) -> void

def pool_then_return(mu: mutable Mutex) -> i64:
	pool workers(4):
		lock mu as g:
			return 7

def pool_then_fallthrough(mu: mutable Mutex) -> void:
	pool workers(2):
		lock mu as g:
			pass
`
	result := parseAndAnalyze(t, "backend_pool_scope.elisa", src)
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
func TestGenerateLLVMIRLowersSubmitSyntaxInsidePoolScope(t *testing.T) {
	src := `extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

extern pool_await(task: Task[i64, Pending]) -> i64

def work(value: i64) -> i64:
	return value + 1

def submit_then_await() -> i64:
	pool workers(2):
		task: Task[i64, Pending] = submit work(7)
		return await task
`
	result := parseAndAnalyze(t, "backend_submit_syntax.elisa", src)
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
	src := `def pool_submit1(pool: ThreadPool&, fn: fn(i64) -> i64, arg: i64) -> Task[i64, Pending]:
	task: Task[i64, Pending] = zeroed
	return move task

extern pool_await(task: Task[i64, Pending]) -> i64

def work(value: i64) -> i64:
	return value + 1

def submit_then_await(pool: ThreadPool&) -> i64:
	task: Task[i64, Pending] = submit[pool] work(7)
	return await task
`
	result := parseAndAnalyze(t, "backend_submit_explicit_pool_syntax.elisa", src)
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
	result := parseAndAnalyze(t, "backend_void_call.elisa", src)
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
