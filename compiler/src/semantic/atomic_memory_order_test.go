package semantic

import (
	"strings"
	"testing"
)

const atomicOrderTestPrelude = `
enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

def load[T](slot: atomic[T]&, order: MemoryOrder) -> T:
    return slot.value

def store[T](slot: mutable atomic[T]&, value: T, order: MemoryOrder):
    slot.value <- value

def compare_exchange[T](slot: mutable atomic[T]&, expected: T, desired: T, success: MemoryOrder, failure: MemoryOrder) -> bool:
    return false

def fence(order: MemoryOrder) can[Atomics.Fence]:
    _ = order
`

func TestAtomicMemoryOrderRejectsInvalidLoadStoreAndFailureOrders(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "atomic_order_invalid.elisa", atomicOrderTestPrelude+`
def bad(slot: mutable atomic[i64]&):
    _ = load(slot, MemoryOrder.Release)
    store(slot, 1, MemoryOrder.AcqRel)
    _ = compare_exchange(slot, 1, 2, MemoryOrder.AcqRel, MemoryOrder.Release)
    fence(MemoryOrder.Relaxed)
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	for _, want := range []string{
		"atomic load cannot use release ordering",
		"atomic store cannot use acquire ordering",
		"compare_exchange failure ordering cannot use release semantics",
		"atomic fence cannot use relaxed ordering",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic %q, got:\n%s", want, all)
		}
	}
}

func TestLegacyRawAtomicsAreDeprecated(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "legacy_raw_atomics.elisa", atomicOrderTestPrelude+`
def use_raw(slot: mutable atomic[i64]&):
    _ = load(slot, MemoryOrder.Acquire)
    store(slot, 1, MemoryOrder.Release)
    _ = compare_exchange(slot, 1, 2, MemoryOrder.AcqRel, MemoryOrder.Acquire)
    fence(MemoryOrder.SeqCst)
`, AnalyzeOptions{})
	deprecations := strings.Join(result.Deprecations(), "\n")
	for _, check := range []string{
		"`load` is legacy raw atomic surface",
		"`store` is legacy raw atomic surface",
		"`compare_exchange` is legacy raw atomic surface",
		"`fence` is legacy raw atomic surface",
	} {
		if !strings.Contains(deprecations, check) {
			t.Fatalf("expected deprecation %q, got:\n%s", check, deprecations)
		}
	}
	if errors := strings.Join(result.Errors(), "\n"); strings.Contains(errors, "strict concurrency error") {
		t.Fatalf("legacy raw atomics should remain deprecations by default, got errors:\n%s", errors)
	}
}

func TestStrictConcurrencyPromotesLegacyRawAtomicsToErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "strict_raw_atomics.elisa", atomicOrderTestPrelude+`
def use_raw(slot: mutable atomic[i64]&):
    _ = load(slot, MemoryOrder.Acquire)
    store(slot, 1, MemoryOrder.Release)
    _ = compare_exchange(slot, 1, 2, MemoryOrder.AcqRel, MemoryOrder.Acquire)
    fence(MemoryOrder.SeqCst)
`, AnalyzeOptions{EnforceStrictConcurrency: true})
	errors := strings.Join(result.Errors(), "\n")
	for _, check := range []string{
		"strict concurrency error: `load` is legacy raw atomic surface",
		"strict concurrency error: `store` is legacy raw atomic surface",
		"strict concurrency error: `compare_exchange` is legacy raw atomic surface",
		"strict concurrency error: `fence` is legacy raw atomic surface",
	} {
		if !strings.Contains(errors, check) {
			t.Fatalf("expected strict concurrency error %q, got:\n%s", check, errors)
		}
	}
	if deprecations := strings.Join(result.Deprecations(), "\n"); strings.Contains(deprecations, "legacy raw atomic surface") {
		t.Fatalf("strict concurrency should promote raw atomic diagnostics to errors, got deprecations:\n%s", deprecations)
	}
}

func TestAtomicMemoryOrderAcceptsValidOrders(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "atomic_order_valid.elisa", atomicOrderTestPrelude+`
def ok(slot: mutable atomic[i64]&):
    _ = load(slot, MemoryOrder.Acquire)
    store(slot, 1, MemoryOrder.Release)
    _ = compare_exchange(slot, 1, 2, MemoryOrder.AcqRel, MemoryOrder.Acquire)
    fence(MemoryOrder.SeqCst)
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if strings.Contains(all, "atomic load cannot") || strings.Contains(all, "atomic store cannot") || strings.Contains(all, "failure ordering") || strings.Contains(all, "atomic fence cannot") {
		t.Fatalf("expected valid memory orders to pass, got:\n%s", all)
	}
}

func TestAtomicMemoryOrderRequiresCompileTimeConstant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "atomic_order_dynamic.elisa", atomicOrderTestPrelude+`
def dynamic(slot: mutable atomic[i64]&, order: MemoryOrder):
    _ = load(slot, order)
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "atomic load memory order must be a compile-time MemoryOrder constant") {
		t.Fatalf("expected non-constant memory order diagnostic, got:\n%s", all)
	}
}

func TestCompareExchangeFailureOrderingCannotBeStrongerThanSuccess(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "atomic_order_cmpxchg_strength.elisa", atomicOrderTestPrelude+`
def bad(slot: mutable atomic[i64]&):
    _ = compare_exchange(slot, 1, 2, MemoryOrder.Relaxed, MemoryOrder.Acquire)
    _ = compare_exchange(slot, 1, 2, MemoryOrder.Release, MemoryOrder.Acquire)
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if got := strings.Count(all, "compare_exchange failure ordering cannot be stronger than success ordering"); got != 2 {
		t.Fatalf("expected two failure-ordering strength diagnostics, got %d:\n%s", got, all)
	}
}
