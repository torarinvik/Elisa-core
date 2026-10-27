//go:build cgo

package backend

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestPartitionedDistinctLambdaBodies(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	names := [2]string{}
	for n := 0; names[0] == "" || names[1] == ""; n++ {
		candidate := fmt.Sprintf("worker%d", n)
		p := partitionOwner(candidate, 2)
		if names[p] == "" {
			names[p] = candidate
		}
	}
	source := fmt.Sprintf(`def %s(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def %s(n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 2
    return f(n)

def main() -> i64:
    return %s(3) + %s(3)
`, names[0], names[1], names[0], names[1])
	result := parseAndAnalyzeBackendTest(t, "lambda_collision.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "lambda-collision", 64<<20)
	object := filepath.Join(t.TempDir(), "out.o")
	if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 {
		t.Fatalf("partitioned IR was not exercised: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, object); got != 9 {
		t.Fatalf("partitioned runtime got %d, want 9", got)
	}
	before := session.Stats()
	replayed := filepath.Join(t.TempDir(), "replayed.o")
	if err := session.WriteObject(result, replayed, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsReused-before.PartitionsReused != 2 {
		t.Fatalf("cache did not replay both partitions: %+v", stats)
	}
	if got := incrementalObjectNativeStatus(t, replayed); got != 9 {
		t.Fatalf("cached runtime got %d, want 9", got)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 9 {
		t.Fatalf("cold runtime got %d, want 9", got)
	}
}

func TestPartitionedGenericLambdaAcrossPartitions(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	names := [2]string{}
	for n := 0; names[0] == "" || names[1] == ""; n++ {
		candidate := fmt.Sprintf("caller%d", n)
		p := partitionOwner(candidate, 2)
		if names[p] == "" {
			names[p] = candidate
		}
	}
	source := fmt.Sprintf(`def invoke[T](value: T, n: i64) -> i64:
    f: fn(i64) -> i64 = fn(x: i64) => x + 1
    return f(n)

def %s() -> i64:
    return invoke[i64](0, 3)

def %s() -> i64:
    return invoke[i64](0, 4)

def main() -> i64:
    return %s() + %s()
`, names[0], names[1], names[0], names[1])
	result := parseAndAnalyzeBackendTest(t, "generic_lambda.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "generic-lambda", 64<<20)
	for i := 0; i < 2; i++ {
		object := filepath.Join(t.TempDir(), "out.o")
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
			t.Fatal(err)
		}
		if got := incrementalObjectNativeStatus(t, object); got != 9 {
			t.Fatalf("iteration %d: got %d, want 9", i, got)
		}
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 || stats.PartitionsReused != 2 {
		t.Fatalf("partitions not emitted and reused: %+v", stats)
	}
	cold := filepath.Join(t.TempDir(), "cold.o")
	if err := WriteLLVMObjectFileWithOptions(result, cold, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0, Serial: true}); err != nil {
		t.Fatal(err)
	}
	if got := incrementalObjectNativeStatus(t, cold); got != 9 {
		t.Fatalf("cold got %d, want 9", got)
	}
}

func TestPartitionedParallelWorkerDeclinesUnsafeIdentity(t *testing.T) {
	source := parallelForConcurrencyPrelude + `
packed enum Expr:
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
    store: Expr.Store[Local] = Expr.Store(owner)
    in store:
        _ = new Expr.Int(span: 1, value: 3)
    frozen: Expr.Store[Frozen] = freeze(move store)
    pool workers(2):
        parallel for node in frozen:
            if node in frozen is Expr.Int(value: value):
                _ = value + node.span
    return 0
`
	result := parseAndAnalyzeBackendTest(t, "parallel_worker_decline.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "worker-decline", 64<<20)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	worklists := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
	var unit, target [32]byte
	var members []string
	_, err := session.buildIRPartition(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, partitionOwner("visit", 2), 2, true, "test", unit, target, worklists[partitionOwner("visit", 2)], &members, shared)
	if !errors.Is(err, errPartitionDeclined) {
		t.Fatalf("parallel worker must decline partitioning, got %v", err)
	}
}

func TestPartitionedSubmitWorkerDeclinesUnsafeIdentity(t *testing.T) {
	source := parallelForConcurrencyPrelude + `
module Shapes:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

module Other:
    public:
        enum Node layout(handle: u32):
            pass
        enum Expr is Node:
            Lit(value: i64)
            Pair(left: Expr, right: Expr)

struct Ticket:
    node: Shapes::Expr?
    bias: i64

def worker(ticket: Ticket) -> i64:
    return ticket.bias

struct OtherTicket:
    node: Other::Expr?
    bias: i64

def other_worker(ticket: OtherTicket) -> i64:
    return ticket.bias

def main() -> i64:
    can Memory.Allocate, Pool{Create,Submit,Shutdown}, Abort.Panic:
        region storage(65536):
            pool workers(1):
                task: Task[i64, Pending] = submit[workers] worker(Ticket{node: null, bias: 42})
                other_task: Task[i64, Pending] = submit[workers] other_worker(OtherTicket{node: null, bias: 17})
                _ = move task
                _ = move other_task
                return 0
`
	result := parseAndAnalyzeBackendTest(t, "submit_worker_decline.elisa", source)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "worker-decline", 64<<20)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	worklists := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
	var unit, target [32]byte
	var members []string
	part := partitionOwner("main", 2)
	_, err := session.buildIRPartition(result, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}, part, 2, true, "test", unit, target, worklists[part], &members, shared)
	if !errors.Is(err, errPartitionDeclined) {
		t.Fatalf("submit worker must decline partitioning, got %v", err)
	}
}
