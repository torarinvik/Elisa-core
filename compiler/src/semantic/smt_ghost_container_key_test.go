//go:build cgo

package semantic

import "testing"

// Ghost-call congruence keys an immutable darray binding (including the implicit borrow passed to a
// reference parameter), so total(xs, k) and total(xs, 0) agree under k == 0. Distinct containers keep
// distinct values, and an untranslatable call argument becomes a free value: both negatives fail.
func TestSMTGhostContainerCongruenceAndOpaqueArgs(t *testing.T) {
	src := `module M:
    public:
        ghost def total(xs: darray[sview]&, index: usize) -> i64:
            requires index <= xs.count
            decreases xs.count - index
            return 0 if index == xs.count
            return 1 + total(xs, index + 1.usize())

        def same(xs: darray[sview], ys: darray[sview]) -> bool:
            ensure result == (total(xs, 0.usize()) == total(ys, 0.usize()))
            return true

        def ident(x: i64) -> i64:
            ensure result == x
            return x

        def opaque(v: darray[sview]) -> i64:
            requires v.count > 2
            ensure result == 0
            a: i64 = ident(v[0].len.i64())
            b: i64 = ident(v[1].len.i64())
            return a - b

        def stable(xs: darray[sview], k: usize) -> bool:
            requires k == 0
            ensure result == (total(xs, k) == total(xs, 0.usize()))
            return true
`
	result := analyzeContractStrict(t, "ghost_container.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "stable"); n != 0 {
		t.Fatalf("stable: expected proof, got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "same") == 0 {
		t.Fatalf("same: distinct containers must not be equated; got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "opaque") == 0 {
		t.Fatalf("opaque: free stand-ins must not be equated; got %v", result.Errors())
	}
}

// A call-valued loop update whose argument is outside the fragment still contributes the callee's
// ensures (over a free stand-in) and its result's machine range, so greatest >= 0 is inductive.
func TestSMTCallEnsuresWithOpaqueArgInLoop(t *testing.T) {
	src := `module M:
    public:
        def gb(left: i64, right: i64) -> i64:
            ensure result >= left
            ensure result >= right
            ensure result == (left if left > right else right)
            return left if left > right else right

        ghost def lbf(facts: darray[sview]&, index: usize, greatest: i64) -> i64:
            requires index <= facts.count and greatest >= 0
            decreases facts.count - index
            ensure result >= greatest
            return greatest if index == facts.count
            return lbf(facts, index + 1.usize(), gb(greatest, 5)) if 3 < index
            return lbf(facts, index + 1.usize(), greatest)

        def lb(facts: darray[sview]) -> i64:
            ensure result >= 0
            greatest: mutable i64 = 0
            index: mutable usize = 0
            while index < facts.count |index, facts, greatest|:
                decreases facts.count - index
                invariant index <= facts.count
                invariant greatest >= 0
                fact: sview = facts[index]
                if 3 < fact.len:
                    vv: i64 = fact.len.i64()
                    greatest <- gb(greatest, vv)
                index <- index + 1
            return greatest
`
	result := analyzeContractStrict(t, "opaque_loop.elisa", src)
	if n := loopLintCount(result.Errors()) + ensureFailuresFor(result.Errors(), "lb"); n != 0 {
		t.Fatalf("expected full proof, got %v", result.Errors())
	}
	// SOUNDNESS: the range axiom is the type's range, nothing tighter.
	rng := `
def pass8(x: u8) -> u8:
    return x

def tight(y: u8) -> u8:
    ensure result < 255
    return pass8(y)
`
	result = analyzeContractStrict(t, "call_range.elisa", rng)
	if ensureFailuresFor(result.Errors(), "tight") == 0 {
		t.Fatalf("tight: u8 call result may be 255; got %v", result.Errors())
	}
}
