package semantic

import (
	"strings"
	"testing"
)

const borrowedNurseryPrelude = `def task_group_new() -> TaskGroup:
	return zeroed
def task_group_add[R](group: mutable TaskGroup&, task: Task[R, Pending]):
	_ = move task
def task_group_wait_all(group: mutable TaskGroup&):
	pass
def pool_submit1[A, R](pool: mutable ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
	return zeroed
def work(value: i64) -> i64:
	return value
`

func TestBorrowedNurseryAcceptsMutablePoolReferenceAndIgnoresSourceNameShadow(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "borrowed_nursery.elisa", borrowedNurseryPrelude+`
def run(pool: mutable ThreadPool&) -> void:
	nursery on(pool):
		pool: mutable ThreadPool = zeroed
		submit work(1)
`)
	if got := strings.Join(result.Errors(), "\n"); got != "" {
		t.Fatalf("mutable borrowed-pool nursery should accept a same-name body shadow, got:\n%s", got)
	}
}

func TestBorrowedNurseryAcceptsMutablePoolValue(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "borrowed_nursery_value.elisa", borrowedNurseryPrelude+`
def run() -> void:
	pool: mutable ThreadPool = zeroed
	nursery on(pool):
		submit work(1)
`)
	if got := strings.Join(result.Errors(), "\n"); got != "" {
		t.Fatalf("mutable borrowed-pool nursery should accept a ThreadPool value, got:\n%s", got)
	}
}

func TestBorrowedNurseryJoinsSubmittedTaskBeforeEarlyReturn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "borrowed_nursery_return.elisa", borrowedNurseryPrelude+`
def run(pool: mutable ThreadPool&) -> i64:
	nursery on(pool):
		submit work(1)
		return 7
`)
	if got := strings.Join(result.Errors(), "\n"); got != "" {
		t.Fatalf("early return should join the nursery's generated TaskGroup, got:\n%s", got)
	}
}

func TestBorrowedNurseryDoesNotConsumeUserTaskGroupOnEarlyReturn(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "borrowed_nursery_user_group_return.elisa", borrowedNurseryPrelude+`
def run(pool: mutable ThreadPool&) -> i64:
	nursery on(pool):
		group: mutable TaskGroup = task_group_new()
		task_group_add(&group, submit[pool] work(1))
		return 7
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, `task group with pending tasks "group" must be consumed before scope exit`) {
		t.Fatalf("early return must still reject a pending user TaskGroup, got:\n%s", got)
	}
}

func TestBorrowedNurseryRejectsImmutablePool(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "borrowed_nursery_immutable.elisa", `def run(pool: ThreadPool&):
	nursery on(pool):
		pass
`)
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, `borrowed nursery requires "pool" to be a mutable ThreadPool reference`) {
		t.Fatalf("expected immutable pool reference to be rejected, got:\n%s", got)
	}
}
