package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

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
		" @mutex_lock(ptr)",
		"declare void @mutex_unlock(",
		"define i64 @pool_then_return(%Mutex",
		"define void @pool_then_fallthrough(%Mutex",
		"call %ThreadPool @pool_new(",
		"call void @pool_shutdown(ptr",
		"call void @mutex_unlock(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	returnIR := functionIR(output, "pool_then_return")
	guardType := genericV1Symbol(t, output, `%"MutexGuard$generic_v1$`)
	assertLLVMIRContains(t, returnIR,
		"call "+guardType+" @mutex_lock(ptr",
		"call void @mutex_unlock("+guardType,
	)
	unlockIdx := strings.Index(returnIR, "call void @mutex_unlock(")
	shutdownIdx := strings.Index(returnIR, "call void @pool_shutdown(ptr")
	retIdx := strings.Index(returnIR, "ret i64 7")
	if unlockIdx < 0 || shutdownIdx < 0 || retIdx < 0 || unlockIdx > shutdownIdx || shutdownIdx > retIdx {
		t.Fatalf("expected pool_then_return to unlock, then shutdown the pool, then return, got:\n%s", returnIR)
	}
	fallthroughIR := functionIR(output, "pool_then_fallthrough")
	assertLLVMIRContains(t, fallthroughIR,
		"call "+guardType+" @mutex_lock(ptr",
		"call void @mutex_unlock("+guardType,
	)
	unlockIdx = strings.Index(fallthroughIR, "call void @mutex_unlock(")
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
		"call i64 @pool_await(%\"Task$generic_v1$",
		"call void @pool_shutdown(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	body := functionIR(output, "submit_then_await")
	taskType := genericV1Symbol(t, output, `%"Task$generic_v1$`)
	assertLLVMIRContains(t, body,
		"call "+taskType+" @pool_submit1(ptr",
		"call i64 @pool_await("+taskType,
	)
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
		"call i64 @pool_await(%\"Task$generic_v1$",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	body := functionIR(output, "submit_then_await")
	taskType := genericV1Symbol(t, output, `%"Task$generic_v1$`)
	assertLLVMIRContains(t, body,
		"call "+taskType+" @pool_submit1(ptr",
		"call i64 @pool_await("+taskType,
	)
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

func TestUserDefinedStringViewEqualityHelperKeepsItsImplementation(t *testing.T) {
	src := `struct StringView:
	data: u8&
	len: i64

def ctx_string_view_eq(left: StringView, right: StringView) -> int:
	_ = left
	_ = right
	return 0

def check(left: StringView, right: StringView) -> bool:
	return ctx_string_view_eq(left, right) != 0
`
	result := parseAndAnalyze(t, "backend_user_defined_string_view_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	body := functionIR(output, "check")
	if body == "" {
		t.Fatalf("expected to find check body, got:\n%s", output)
	}
	if !strings.Contains(body, "call i64 @ctx_string_view_eq(") {
		t.Fatalf("expected check to call the user-defined helper instead of compiler equality lowering, got:\n%s", body)
	}
}
