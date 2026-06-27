package semantic

import (
	"strings"
	"testing"
)

// POSITIVE: an immutable-darray parameter indexed by an immutable index parameter whose value is
// pinned STRICTLY below xs.count by a live `requires i < xs.count` precondition. Both xs and i are
// readonly params, so xs.count cannot change inside the body and i cannot drift — the access is
// provably in bounds with NO runtime check.
//
// SOUNDNESS argument: the precondition `i < xs.count` is enforced at every call site. Because xs is
// an immutable darray (readonly reference) its count cannot change inside the body. Because i is
// an immutable usize its value cannot change. Together: i < xs.count holds at the index site.
func TestDependentRequiresFactElidesDArrayBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "darray_dep_bounds.elisa", `def read_at(xs: darray[u8]&, i: usize) -> u8:
    requires i < xs.count
    return xs[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("a live `requires i < xs.count` on immutable params must prove xs[i] in bounds, got:\n%s", all)
	}
}

// SOUNDNESS-NEGATIVE: `i <= xs.count` (less-than-OR-EQUAL) does NOT prove `xs[i]` in bounds —
// when `i == xs.count` the index is exactly out of bounds. The runtime check MUST remain.
func TestDependentRequiresLteqDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_dep_lteq.elisa", `def read_at(xs: darray[u8]&, i: usize) -> u8:
    requires i <= xs.count
    return xs[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("`requires i <= xs.count` allows i==count (out of bounds); the check must NOT be eliminated, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS-NEGATIVE: after xs.push the darray's count grows, so a precondition `i < xs.count`
// that held at function entry no longer matches the CURRENT count. The bounds proof must be
// invalidated and xs[i] must keep its runtime check.
func TestDependentRequiresAfterPushDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_dep_push.elisa", `def read_at(owner: mutable Arena&, xs: mutable darray[u8], i: usize) -> u8:
    requires i < xs.count
    in owner:
        xs.push(0)
    return xs[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("after xs.push the old `i < xs.count` fact is stale; the check must NOT be eliminated, got:\n%s", allDiagnostics(result))
	}
}
