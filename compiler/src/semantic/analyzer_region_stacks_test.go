package semantic

import (
	"strings"
	"testing"
)

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

// Phase C strategy inference (hard-bound): a darray whose footprint is PROVABLE — its sole growth
// is a single counting-loop push — AND which has an interior reference taken into it gets its own
// reserve_commit stack (stable base). A provably-bounded darray without an interior ref stays on
// the shared chained stack.
func TestRegionStacksReserveCommitForHardBoundedInteriorRef(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rs_rc.elisa", `def f(n: usize) -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            xs: mutable darray[i64] = []
            for i in 0..<n:
                xs.push(i.i64())
            e0: i64& = &xs[0]
            plain: mutable darray[i64] = []
            for i in 0..<n:
                plain.push(i.i64())
            sink: i64 = e0[0] + plain[0]
            if sink < 0:
                panic("x")
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if asn.stackStrategy(asn.StackOf["xs"]) != "reserve_commit" {
		t.Fatalf("hard-bounded darray with an interior ref must get a reserve_commit stack, got %v / %v", asn.StackOf, asn.StackStrategy)
	}
	if asn.StackOf["plain"] != 0 {
		t.Fatalf("bounded darray without an interior ref must stay on the shared stack, got %v", asn.StackOf)
	}
}

// An interior ref into an UNBOUNDED darray (growth not a single counting-loop push) must NOT get a
// reserve_commit stack — reserve_commit there would panic on overflow. It stays chained, and the
// honest interior-ref invalidation error remains (resolved only by an explicit reserve_commit region).
func TestRegionStacksUnboundedInteriorRefStaysChained(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rs_unbounded.elisa", `def f(n: usize) -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            xs: mutable darray[i64] = []
            xs.push(0)
            e0: i64& = &xs[0]
            for i in 1..<n:
                xs.push(i.i64())
            if e0[0] < 0:
                panic("x")
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if asn.stackStrategy(asn.StackOf["xs"]) == "reserve_commit" {
		t.Fatalf("unbounded darray must not get a reserve_commit stack (panic risk), got %v", asn.StackStrategy)
	}
}

// Soundness of the deferred-invalidation flow (Phase C1b): an interior ref across growth into an
// UNBOUNDED darray must STILL be rejected — the error is dropped only for a reserve_commit-backed
// (provably bounded) source, never unsoundly. Here xs has two push sites (push + loop) so it is not
// hard-bounded; the invalidation error must survive.
func TestReserveCommitInferenceUnboundedStillErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "rc_unsound.elisa", `def build(n: usize) -> i64:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(0)
        anchor: i64& = &xs[0]
        for i in 1..<n:
            xs.push(i.i64())
        return anchor[0]
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "cannot be used") {
		t.Fatalf("unbounded interior-ref-across-growth must still error, got:\n%s", all)
	}
}

// Phase B2: an own-stack growable that dies before region exit and is never aliased gets an early
// arena free scheduled (its stack reclaims at its last use, not region exit).
func TestRegionStacksEarlyFreesDeadUnaliasedObject(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "b2_dead.elisa", `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            big: mutable darray[i64] = []
            big.push(1)
            big.push(2)
            total: mutable i64 = big.count.i64()
            rest: mutable darray[i64] = []
            rest.push(3)
            return total + rest.count.i64()
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if _, ok := asn.StackEarlyFreeAfter[asn.StackOf["big"]]; !ok {
		t.Fatalf("a dead unaliased own-stack object must be scheduled for early free, got %v", asn.StackEarlyFreeAfter)
	}
	if _, ok := asn.StackEarlyFreeAfter[asn.StackOf["rest"]]; ok {
		t.Fatalf("an object live to region exit must NOT be early-freed, got %v", asn.StackEarlyFreeAfter)
	}
}

// Phase D (conservative merge): an object whose address is taken (an interior pointer escapes) must
// NOT be early-freed — freeing it would dangle the pointer. It stays to region exit.
func TestRegionStacksDoesNotEarlyFreeAliasedObject(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "b2_aliased.elisa", `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        in auto:
            big: mutable darray[i64] = []
            big.push(1)
            p: i64& = &big[0]
            rest: mutable darray[i64] = []
            rest.push(2)
            return p[0] + rest.count.i64()
`, AnalyzeOptions{})
	asn := onlyRegionStack(t, result)
	if _, ok := asn.StackEarlyFreeAfter[asn.StackOf["big"]]; ok {
		t.Fatalf("an aliased object must not be early-freed (dangling-pointer risk), got %v", asn.StackEarlyFreeAfter)
	}
}

// regionLifetimeClasses counts distinct death-points (lifetime equivalence classes), not stacks:
// one per distinct B2 early-free offset, plus one for the region exit iff a live stack survives to
// it. Stacks that share a region are layout (freed together) and must NOT inflate the count.
func TestRegionLifetimeClassesCountsDeathPoints(t *testing.T) {
	cases := []struct {
		name string
		asn  RegionStackAssignment
		want int
	}{
		{"two growables, none early-freed -> one class (both die at region exit)",
			RegionStackAssignment{StackOf: map[string]int{"foo": 1, "bar": 2}, StackEarlyFreeAfter: map[int]int{}}, 1},
		{"some early-freed at one offset + a survivor -> two classes",
			RegionStackAssignment{StackOf: map[string]int{"a": 1, "b": 2, "c": 3}, StackEarlyFreeAfter: map[int]int{1: 100, 2: 100}}, 2},
		{"two distinct early-free offsets + a survivor -> three classes",
			RegionStackAssignment{StackOf: map[string]int{"a": 1, "b": 2, "c": 3}, StackEarlyFreeAfter: map[int]int{1: 100, 2: 200}}, 3},
		{"everything early-freed at one offset, nothing survives -> one class",
			RegionStackAssignment{StackOf: map[string]int{"a": 1, "b": 2}, StackEarlyFreeAfter: map[int]int{1: 100, 2: 100}}, 1},
		{"no fresh allocations -> zero classes",
			RegionStackAssignment{StackOf: map[string]int{}, StackEarlyFreeAfter: map[int]int{}}, 0},
	}
	for _, tc := range cases {
		if got := tc.asn.regionLifetimeClasses(); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// End to end: four same-lifetime darrays in one scope unify into ONE class (the 5 stacks are pure
// layout); add a loop-local cohort and an early-freed cohort and the count tracks death-points.
func TestRegionLifetimeClassesEndToEnd(t *testing.T) {
	total := func(result *Result) int {
		sum := 0
		for _, asn := range result.RegionStacks {
			sum += asn.regionLifetimeClasses()
		}
		return sum
	}
	// 4 darrays, all live to function return -> 1 class, despite 5 layout stacks.
	four := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "lc_four.elisa", `def f() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        c: mutable darray[i64] = []
        d: mutable darray[i64] = []
        a.push(1)
        b.push(1)
        c.push(1)
        d.push(1)
        return a.count.i64() + b.count.i64() + c.count.i64() + d.count.i64()
`, AnalyzeOptions{})
	if got := total(four); got != 1 {
		t.Fatalf("four same-lifetime darrays: expected 1 unified lifetime class, got %d", got)
	}
	// Function-scope cohort + a loop-iteration-local cohort -> 2 classes (2 distinct scopes).
	withLoop := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "lc_loop.elisa", `def f(n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        a.push(1)
        acc: mutable i64 = a.count.i64()
        for i in 0..<n:
            t: mutable darray[i64] = []
            t.push(i.i64())
            acc <- acc + t[0]
        return acc
`, AnalyzeOptions{})
	if got := total(withLoop); got != 2 {
		t.Fatalf("function cohort + loop-local cohort: expected 2 unified lifetime classes, got %d", got)
	}
}
