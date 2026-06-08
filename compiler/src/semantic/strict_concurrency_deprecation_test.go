package semantic

import (
	"strings"
	"testing"
)

func TestLegacyRawConcurrencyCallsAreDeprecated(t *testing.T) {
	result := analyzeLegacyRawConcurrencySource(t, AnalyzeOptions{})
	deprecations := strings.Join(result.Deprecations(), "\n")
	for _, check := range []string{
		"`cond_wait` is legacy raw condition-variable surface",
		"predicate_wait(cv, move guard, predicate)",
		"`notify_one` is legacy raw notification surface",
		"predicate_notify_one",
		"`spawn1` is low-level escaped-task surface",
		"nursery workers(N)",
		"`detach` is a detached-task escape hatch",
		"`pool_submit1` is low-level pool submission surface",
		"task-group structure",
	} {
		if !strings.Contains(deprecations, check) {
			t.Fatalf("expected deprecation %q, got:\n%s", check, deprecations)
		}
	}
	if errors := strings.Join(result.Errors(), "\n"); errors != "" {
		t.Fatalf("legacy raw concurrency migration diagnostics should be soft deprecations for now, got errors:\n%s", errors)
	}
}

func TestStrictConcurrencyPromotesLegacyRawConcurrencyCallsToErrors(t *testing.T) {
	result := analyzeLegacyRawConcurrencySource(t, AnalyzeOptions{EnforceStrictConcurrency: true})
	errors := strings.Join(result.Errors(), "\n")
	for _, check := range []string{
		"strict concurrency error: `cond_wait` is legacy raw condition-variable surface",
		"predicate_wait(cv, move guard, predicate)",
		"strict concurrency error: `notify_one` is legacy raw notification surface",
		"predicate_notify_one",
		"strict concurrency error: `spawn1` is low-level escaped-task surface",
		"nursery workers(N)",
		"strict concurrency error: `detach` is a detached-task escape hatch",
		"strict concurrency error: `pool_submit1` is low-level pool submission surface",
		"task-group structure",
	} {
		if !strings.Contains(errors, check) {
			t.Fatalf("expected strict concurrency error %q, got:\n%s", check, errors)
		}
	}
	if deprecations := strings.Join(result.Deprecations(), "\n"); deprecations != "" {
		t.Fatalf("strict concurrency should promote these diagnostics to errors, got deprecations:\n%s", deprecations)
	}
}

func analyzeLegacyRawConcurrencySource(t *testing.T, options AnalyzeOptions) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "legacy_raw_concurrency.elisa", `
def cond_wait(cv: mutable CondVar&, g: MutexGuard[Held]) -> MutexGuard[Held]:
    return move g

def notify_one(cv: mutable CondVar&):
    pass

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed

def pool_submit1[A, R](pool: mutable ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
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
