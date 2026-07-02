package semantic

import (
	"strings"
	"testing"
)

// Raw concurrency primitives were removed from the public surface: calls from
// user code are always hard errors, regardless of any warning flag. The stdlib
// stays exempt so the safe wrappers can keep building on them internally.
func TestLegacyRawConcurrencyCallsAreRejected(t *testing.T) {
	result := analyzeLegacyRawConcurrencySource(t, AnalyzeOptions{})
	errors := strings.Join(result.Errors(), "\n")
	for _, check := range []string{
		"raw concurrency surface removed: `cond_wait` is legacy raw condition-variable surface",
		"predicate_wait(cv, move guard, predicate)",
		"raw concurrency surface removed: `notify_one` is legacy raw notification surface",
		"predicate_notify_one",
		"raw concurrency surface removed: `spawn1` is low-level escaped-task surface",
		"nursery workers(N)",
		"raw concurrency surface removed: `detach` is a detached-task escape hatch",
		"raw concurrency surface removed: `pool_submit1` is low-level pool submission surface",
		"task-group structure",
	} {
		if !strings.Contains(errors, check) {
			t.Fatalf("expected error %q, got:\n%s", check, errors)
		}
	}
	if deprecations := strings.Join(result.Deprecations(), "\n"); strings.Contains(deprecations, "legacy raw") {
		t.Fatalf("raw concurrency calls should be hard errors, not deprecations, got:\n%s", deprecations)
	}
}

// The warning flag is now redundant — removal is unconditional — but it must
// stay accepted and keep producing the same hard errors for backward compat.
func TestStrictConcurrencyFlagStillRejectsLegacyRawConcurrencyCalls(t *testing.T) {
	result := analyzeLegacyRawConcurrencySource(t, AnalyzeOptions{EnforceStrictConcurrency: true})
	errors := strings.Join(result.Errors(), "\n")
	for _, check := range []string{
		"raw concurrency surface removed: `cond_wait` is legacy raw condition-variable surface",
		"raw concurrency surface removed: `spawn1` is low-level escaped-task surface",
		"raw concurrency surface removed: `pool_submit1` is low-level pool submission surface",
	} {
		if !strings.Contains(errors, check) {
			t.Fatalf("expected error %q, got:\n%s", check, errors)
		}
	}
}

func analyzeLegacyRawConcurrencySource(t *testing.T, options AnalyzeOptions) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "legacy_raw_concurrency.elisa", `
def cond_wait(cv: mutable CondVar&, g: MutexGuard[Held]) -> MutexGuard[Held]:
    return move g

def notify_one(cv: mutable CondVar&):
    pass

def spawn1[A, R](fn: fn(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed

def pool_submit1[A, R](pool: mutable ThreadPool&, fn: fn(A) -> R, arg: A) -> Task[R, Pending]:
    return zeroed

def detach[R](thread: Thread[R, Joinable]):
    _ = move thread

def worker(value: i64) -> i64:
    return value

def use_raw(cv: mutable CondVar&, guard: MutexGuard[Held], pool: mutable ThreadPool&):
    next: MutexGuard[Held] = cond_wait(cv, move guard)
    notify_one(cv)
    thread: Thread[i64, Joinable] = spawn1(worker, 1)
    detach(move thread)
    _ = pool_submit1(pool, worker, 2)
    _ = move next
`, options)
}
