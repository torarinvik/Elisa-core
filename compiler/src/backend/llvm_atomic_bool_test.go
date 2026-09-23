//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestAtomicBoolOperationsUseByteSizedLLVMStorage(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "elisacore_std/atomic_bool_backend.elisa", `
struct atomic[T]:
    value: mutable T

enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

extern load(slot: atomic[bool]&, order: MemoryOrder) -> bool can[Atomics.Load]
extern store(slot: mutable atomic[bool]&, value: bool, order: MemoryOrder) -> void can[Atomics.Store]
extern exchange(slot: mutable atomic[bool]&, value: bool, order: MemoryOrder) -> bool can[Atomics.Exchange]
extern compare_exchange(slot: mutable atomic[bool]&, expected: bool, desired: bool, success: MemoryOrder, failure: MemoryOrder) -> bool can[Atomics.CompareExchange]

def bool_atomic_probe(value: bool, expected: bool, desired: bool) -> bool can[Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange]:
    can Atomics.Load, Atomics.Store, Atomics.Exchange, Atomics.CompareExchange:
        cell: mutable atomic[bool] = zeroed
        store(&cell, value, MemoryOrder.Release)
        old: bool = exchange(&cell, desired, MemoryOrder.AcqRel)
        swapped: bool = compare_exchange(&cell, expected, value, MemoryOrder.AcqRel, MemoryOrder.Acquire)
        return old and swapped and load(&cell, MemoryOrder.Acquire)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt rejected byte-sized bool atomics: %v", err)
	}
	for _, instruction := range []string{
		"load atomic i8",
		"store atomic i8",
		"atomicrmw xchg",
		"cmpxchg",
		"trunc i8",
		"zext i1",
	} {
		if !strings.Contains(output, instruction) {
			t.Errorf("expected bool atomic IR to contain %q, got:\n%s", instruction, output)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		invalidBoolAtomic := strings.Contains(line, "load atomic i1") || strings.Contains(line, "store atomic i1") || strings.Contains(line, " = atomicrmw") && strings.Contains(line, ", i1 ") || strings.Contains(line, " = cmpxchg") && strings.Contains(line, ", i1 ")
		if invalidBoolAtomic {
			t.Fatalf("bool atomics must not use invalid i1 storage; found %q in IR:\n%s", line, output)
		}
	}
}
