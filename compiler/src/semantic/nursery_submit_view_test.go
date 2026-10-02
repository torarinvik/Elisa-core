package semantic

import (
	"strings"
	"testing"
)

const nurseryViewPrelude = `def pool_new(threads: usize) -> ThreadPool:
    return zeroed
def perf_cores() -> usize:
    return 2
def task_group_new() -> TaskGroup:
    return zeroed
def task_group_add[R](group: mutable TaskGroup&, task: Task[R, Pending]):
    pass
def task_group_wait_all(group: mutable TaskGroup&):
    pass
def pool_submit1[A, R](pool: mutable ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
    return zeroed
def first(s: sview) -> i64:
    return s[0].i64()
`

// A view handed to a nursery task stays live until the nursery joins, so growing its
// backing later in the same body (after the submit) races the worker's read.
func TestNurserySubmittedViewGrownBeforeJoinIsRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "nursery_submit_view_grow.elisa", nurseryViewPrelude+`
def main() -> i64:
    can Parallel, Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        v: sview = buf.as_sview()
        nursery workers(2):
            submit first(v)
            for i in 0..<64:
                buf.push(1.u8())
        return 0
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, `view "v" cannot be used`) {
		t.Fatalf("expected the submitted view to be rejected after growth before the join, got:\n%s", got)
	}
}

func TestNurserySubmittedViewWithoutGrowthIsAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "nursery_submit_view_ok.elisa", nurseryViewPrelude+`
def main() -> i64:
    can Parallel, Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        v: sview = buf.as_sview()
        nursery workers(2):
            submit first(v)
        buf.push(1.u8())
        return 0
`)
	if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "cannot be used") {
		t.Fatalf("growth after the nursery join must not invalidate the submitted view, got:\n%s", got)
	}
}

// Auto-region wrapping of a function body must not hide the permission checks inside it.
func TestRawSpawnInsideAutoRegionBodyIsStillRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "raw_spawn_auto_region.elisa", `def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed
def work(n: i64) -> i64:
    return n
def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = [65.u8()]
        t: Thread[i64, Joinable] = spawn1(work, buf.len.i64())
        return 0
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "raw concurrency surface removed: `spawn1`") {
		t.Fatalf("expected raw spawn1 inside an auto-region body to be rejected, got:\n%s", got)
	}
}
