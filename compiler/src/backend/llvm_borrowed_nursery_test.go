//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestBorrowedNurseryJoinsOnReturnAndFallthroughWithoutShuttingDownPool(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_borrowed_nursery.elisa", parallelForConcurrencyPrelude+`
def return_from_borrowed(pool: mutable ThreadPool&) -> i64:
	can Pool.WaitAll:
		nursery on(pool):
			return 7

def fallthrough_borrowed(pool: mutable ThreadPool&) -> void:
	can Pool.WaitAll:
		nursery on(pool):
			pass

def return_from_owned() -> i64:
	can Pool.Create, Pool.Shutdown, Pool.WaitAll:
		nursery workers(1):
			return 9
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}

	returnBody := functionBodyForDeferTest(t, output, "return_from_borrowed")
	wait := "call void @task_group_wait_all(ptr"
	if waitIndex, returnIndex := strings.Index(returnBody, wait), strings.Index(returnBody, "ret i64 7"); waitIndex < 0 || returnIndex < 0 || waitIndex > returnIndex {
		t.Fatalf("expected borrowed nursery to join before its early return, got:\n%s", returnBody)
	}
	fallthroughBody := functionBodyForDeferTest(t, output, "fallthrough_borrowed")
	if count := strings.Count(fallthroughBody, wait); count != 1 {
		t.Fatalf("expected exactly one borrowed-nursery join on fallthrough, got %d:\n%s", count, fallthroughBody)
	}
	ownedBody := functionBodyForDeferTest(t, output, "return_from_owned")
	waitIndex := strings.Index(ownedBody, wait)
	shutdownIndex := strings.Index(ownedBody, "call void @pool_shutdown(ptr")
	returnIndex := strings.Index(ownedBody, "ret i64 9")
	if waitIndex < 0 || shutdownIndex < 0 || returnIndex < 0 || waitIndex > shutdownIndex || shutdownIndex > returnIndex {
		t.Fatalf("owned nursery must join before shutting down its pool and returning, got:\n%s", ownedBody)
	}
	if strings.Contains(returnBody, "call void @pool_shutdown") || strings.Contains(fallthroughBody, "call void @pool_shutdown") {
		t.Fatalf("borrowed nursery must not shut down its caller-owned pool:\n%s", output)
	}
}

func TestBorrowedNurseryReusesLexicalPoolBeforeEarlyReturn(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_borrowed_lexical_pool.elisa", parallelForConcurrencyPrelude+`
def work(value: i64) -> i64:
	return value + 1

def reuse_lexical_pool() -> i64:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Abort.Panic:
		pool workers(1):
			nursery on(workers):
				submit work(41)
				return 17
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	body := functionBodyForDeferTest(t, output, "reuse_lexical_pool")
	newIndex := strings.Index(body, "call %ThreadPool @pool_new")
	submitSymbol := genericIRSymbolForTest(t, "pool_submit1", &semantic.BuiltinType{Name: "i64"}, &semantic.BuiltinType{Name: "i64"})
	submitIndex := strings.Index(body, "@"+submitSymbol+"(ptr %workers")
	waitIndex := strings.Index(body, "call void @task_group_wait_all")
	shutdownIndex := strings.Index(body, "call void @pool_shutdown")
	returnIndex := strings.Index(body, "ret i64 17")
	if newIndex < 0 || submitIndex < 0 || waitIndex < 0 || shutdownIndex < 0 || returnIndex < 0 ||
		!(newIndex < submitIndex && submitIndex < waitIndex && waitIndex < shutdownIndex && shutdownIndex < returnIndex) {
		t.Fatalf("borrowed nursery must submit on the lexical pool and join before its owner shuts down on return:\n%s", body)
	}
	if count := strings.Count(body, "call %ThreadPool @pool_new"); count != 1 {
		t.Fatalf("borrowed nursery created %d pools, want one owned by the outer pool scope:\n%s", count, body)
	}
}
