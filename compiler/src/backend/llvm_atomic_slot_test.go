//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestAtomicSlotUsesLLVMAtomicInstructions(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "elisacore_std/atomic_slot_backend.elisa", `
struct AtomicSlot[T]:
    value: mutable T

enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

extern load(slot: AtomicSlot[i64]&, order: MemoryOrder) -> i64 can[Atomics.Load]
extern store(slot: mutable AtomicSlot[i64]&, value: i64, order: MemoryOrder) -> void can[Atomics.Store]
extern exchange(slot: mutable AtomicSlot[i64]&, value: i64, order: MemoryOrder) -> i64 can[Atomics.Exchange]
extern compare_exchange(slot: mutable AtomicSlot[i64]&, expected: i64, desired: i64, success: MemoryOrder, failure: MemoryOrder) -> bool can[Atomics.CompareExchange]

def probe(value: i64, expected: i64) -> i64 can[Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange]:
    can Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange:
        cell: mutable AtomicSlot[i64] = AtomicSlot[i64]{value: 0}
        store(&cell, value, MemoryOrder.Release)
        old: i64 = exchange(&cell, value + 1, MemoryOrder.AcqRel)
        swapped: bool = compare_exchange(&cell, expected, value, MemoryOrder.AcqRel, MemoryOrder.Acquire)
        _ = swapped
        return old + load(&cell, MemoryOrder.Acquire)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt rejected AtomicSlot atomics: %v", err)
	}
	for _, instruction := range []string{
		"load atomic i64",
		"store atomic i64",
		"atomicrmw xchg",
		"cmpxchg",
	} {
		if !strings.Contains(output, instruction) {
			t.Errorf("expected AtomicSlot IR to contain %q, got:\n%s", instruction, output)
		}
	}
}
