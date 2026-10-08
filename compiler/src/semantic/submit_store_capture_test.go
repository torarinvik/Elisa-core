package semantic

import (
	"strings"
	"testing"
)

// A worker that reads a region-backed packed enum carries the enum's store as a hidden
// parameter. Submitting it captures the submitting scope's store (analyzer_submit_store_capture.go)
// instead of failing on `fn(A) with __packed_store_E: <invalid>`.
const submitStoreCapturePrelude = `def pool_new(threads: usize) -> ThreadPool:
    return zeroed
def perf_cores() -> usize:
    return 2
def task_group_new() -> TaskGroup:
    return zeroed
def task_group_add[R](group: mutable TaskGroup&, task: Task[R, Pending]):
    pass
def task_group_wait_all(group: mutable TaskGroup&):
    pass
def pool_submit1[A, R, permission P](pool: mutable ThreadPool&, fn: fn(A) -> R can[P], arg: A) -> Task[R, Pending]:
    return zeroed

enum Node layout(handle: u32):
    pass

enum Expr is Node:
    Absent
    Lit(value: i64)
    Add(lhs: Expr, rhs: Expr)

def eval(e: Expr) -> i64:
    can Abort.Panic:
        match e:
            Expr.Lit(value):
                return value
            Expr.Add(lhs, rhs):
                return eval(lhs) + eval(rhs)
            Expr.Absent:
                return 0

def build(n: i64) -> Expr:
    can Memory.Allocate:
        if n <= 0:
            return Expr.Lit(1)
        return Expr.Add(build(n - 1), Expr.Lit(n))
`

func TestSubmitOfStoreReadingWorkerCapturesTheStore(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "submit_store_capture_read.elisa", submitStoreCapturePrelude+`
def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(e)

def main() -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        nursery workers(2):
            submit worker(e)
        return eval(e)
`)
	if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "__packed_store_") || strings.Contains(got, "shares the submitting scope") {
		t.Fatalf("a store-reading worker must be submittable, got:\n%s", got)
	}
}

func TestSubmitOfStoreBuildingWorkerIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "submit_store_capture_build.elisa", submitStoreCapturePrelude+`
def grow(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return eval(build(3)) + eval(e)

def worker(e: Expr) -> i64:
    can Memory.Allocate, Abort.Panic:
        return grow(e)

def main() -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        nursery workers(2):
            submit worker(e)
        return eval(e)
`)
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, `"worker" is submitted to another thread and shares the submitting scope's Node store, but it builds Node nodes (through "build")`) {
		t.Fatalf("expected a store-building worker to be rejected naming the builder, got:\n%s", got)
	}
}

// A payload-less variant used as a value (`Expr.Absent`) allocates a node too.
func TestSubmitOfWorkerReturningABareVariantIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "submit_store_capture_bare.elisa", submitStoreCapturePrelude+`
def fallback(e: Expr, n: i64) -> Expr:
    can Abort.Panic:
        if n > 0:
            return e
        return Expr.Absent

def worker(e: Expr) -> i64:
    can Abort.Panic:
        return eval(fallback(e, 0))

def main() -> i64:
    can Memory.Allocate, Parallel, Abort.Panic:
        e: Expr = build(10)
        nursery workers(2):
            submit worker(e)
        return eval(e)
`)
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, `but it builds Node nodes (through "fallback")`) {
		t.Fatalf("expected a worker producing a bare variant to be rejected, got:\n%s", got)
	}
}
