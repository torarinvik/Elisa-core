//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRHonorsAtomicMemoryOrders(t *testing.T) {
	src := `struct atomic[T]:
	value: mutable T

enum MemoryOrder:
	Relaxed
	Acquire
	Release
	AcqRel
	SeqCst

def load[T](slot: atomic[T]&, order: MemoryOrder) -> T:
	can Atomics.Load:
		_ = order
		return slot.value

def store[T](slot: mutable atomic[T]&, value: T, order: MemoryOrder):
	can Atomics.Store:
		_ = order
		slot.value <- value

def compare_exchange[T](slot: mutable atomic[T]&, expected: T, desired: T, success: MemoryOrder, failure: MemoryOrder) -> bool:
	can Atomics.CompareExchange:
		_ = success
		_ = failure
		if slot.value == expected:
			slot.value <- desired
			return true
		return false

def fetch_add(slot: mutable atomic[i64]&, value: i64, order: MemoryOrder) -> i64:
	can Atomics.Rmw:
		_ = order
		old_value: i64 = slot.value
		slot.value <- old_value + value
		return old_value

def fence(order: MemoryOrder):
	can Atomics.Fence:
		_ = order

def atomics_order_roundtrip() -> i64:
	can Atomics.Load, Atomics.Store, Atomics.CompareExchange, Atomics.Rmw, Atomics.Fence:
		slot: atomic[i64] = zeroed
		slot_ref: mutable atomic[i64]& = slot.ref[atomic[i64]&]
		store(slot_ref, 1, MemoryOrder.Release)
		prior: i64 = fetch_add(slot_ref, 1, MemoryOrder.AcqRel)
		updated: bool = compare_exchange(slot_ref, 2, 3, MemoryOrder.AcqRel, MemoryOrder.Acquire)
		fence(MemoryOrder.Release)
		current: i64 = load(slot_ref, MemoryOrder.Acquire)
		if updated:
			return prior + current
		return current
`
	result := parseAndAnalyzeBackendTest(t, "backend_atomic_memory_orders.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"store atomic i64",
		" release, align",
		"atomicrmw add",
		" acq_rel, align",
		"cmpxchg ptr",
		" acq_rel acquire",
		"fence release",
		"load atomic i64",
		" acquire, align",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM atomic lowering to contain %q, got:\n%s", check, output)
		}
	}
}
