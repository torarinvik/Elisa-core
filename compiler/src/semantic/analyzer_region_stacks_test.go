package semantic

import "testing"

// onlyRegionStack returns the single region's stack assignment from an analysis result (test
// fixtures here have exactly one inferred region).
func onlyRegionStack(t *testing.T, result *Result) RegionStackAssignment {
	t.Helper()
	if len(result.RegionStacks) != 1 {
		t.Fatalf("expected exactly one inferred region, got %d", len(result.RegionStacks))
	}
	for _, asn := range result.RegionStacks {
		return asn
	}
	return RegionStackAssignment{}
}

// Two unreserved growables each get their own stack (1 and 2); the shared stack 0 stays empty.
func TestRegionStacksGivesEachGrowableItsOwnStack(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rs_two.elisa", `def f() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            foo: mutable darray[i64] = []
            bar: mutable darray[i64] = []
            foo.push(1)
            bar.push(2)
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if asn.StackOf["foo"] == 0 || asn.StackOf["bar"] == 0 {
		t.Fatalf("unreserved growables must get their own (non-shared) stacks, got %v", asn.StackOf)
	}
	if asn.StackOf["foo"] == asn.StackOf["bar"] {
		t.Fatalf("two unreserved growables must not share a stack, got %v", asn.StackOf)
	}
}

// A reserved (fixed-footprint) growable shares stack 0; an unreserved one gets its own.
func TestRegionStacksReservedSharesStackZero(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rs_reserved.elisa", `def f(n: usize) -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            foo: mutable darray[i64] = []
            bar: mutable darray[i64] = []
            foo.reserve(n)
            foo.push(1)
            bar.push(2)
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if asn.StackOf["foo"] != 0 {
		t.Fatalf("reserved growable must share stack 0, got %v", asn.StackOf)
	}
	if asn.StackOf["bar"] == 0 {
		t.Fatalf("unreserved growable must get its own stack, got %v", asn.StackOf)
	}
}
