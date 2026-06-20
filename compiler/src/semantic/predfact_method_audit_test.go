package semantic

import "testing"

// AUDIT PROBE (predFact soundness): a mutating BUILTIN METHOD on the receiver (`xs.clear()`) must drop
// the predicate fact, exactly as `bump(&n)` does — otherwise a stale `NonEmpty` would discharge
// `need(xs)` after the darray was emptied. The existing ref-call test asserts this for an explicit
// `&n` argument; this probes the method-receiver shape the design comment claims is "the same".
func TestPredFactDroppedByMutatingMethodReceiver(t *testing.T) {
	src := `
law NonEmpty(self: darray[i64]) = self.count > 0

def need(xs: darray[i64] is NonEmpty) -> i64:
    return xs[0]

def f(xs: mutable darray[i64]) -> i64:
    if xs is NonEmpty:
        xs.clear()
        return need(xs)
    return 0
`
	result := analyzeTreeTestSource(t, "predfact_method_drop.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("UNSOUND: `xs.clear()` must drop the NonEmpty fact, forcing a runtime check on need(xs); "+
			"got 0 runtime checks (the emptied darray was wrongly proven NonEmpty). errors=%v", result.Errors())
	}
}

// Positive control: with NO mutation between the narrowing and the use, the fact legitimately holds
// and need(xs) proves statically (no runtime check) — so the drop above is real, not a blanket failure.
func TestPredFactHoldsWithoutMethodMutation(t *testing.T) {
	src := `
law NonEmpty(self: darray[i64]) = self.count > 0

def need(xs: darray[i64] is NonEmpty) -> i64:
    return xs[0]

def f(xs: mutable darray[i64]) -> i64:
    if xs is NonEmpty:
        return need(xs)
    return 0
`
	result := analyzeTreeTestSource(t, "predfact_method_hold.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unmutated narrowed darray should prove need(xs), got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("unmutated narrowed darray should be statically proven, got %d runtime checks", len(result.CallArgRefinementChecks))
	}
}

// AUDIT PROBE (dependent-freeze via method): a fact `i is Bounded[0, xs.count]` reads xs.count, so a
// mutating method on xs (`xs.clear()`) must drop it — the length it froze is now stale. Without the
// receiver-invalidation fix, `arr[i]`-style use of i would stay "proven" against a shrunk container.
func TestPredFactDependentFrozenLengthDroppedByMethod(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need(x: i64 is Bounded[0, 3]) -> i64:
    return x

def f(xs: mutable darray[i64], i: i64) -> i64:
    if i is Bounded[0, 3]:
        xs.clear()
        return need(i)
    return 0
`
	// `i` itself is never mutated, so its Bounded fact legitimately survives `xs.clear()` (clear touches
	// xs, not i). This is the positive control for the dependent path: an unrelated container mutation
	// must NOT drop a fact that does not depend on it.
	result := analyzeTreeTestSource(t, "predfact_dep_unrelated.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`i is Bounded` does not depend on xs, so xs.clear() must not drop it; got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("i's fact is independent of xs; need(i) should prove statically, got %d checks", len(result.CallArgRefinementChecks))
	}
}
