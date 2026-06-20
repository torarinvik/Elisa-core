package semantic

import (
	"strings"
	"testing"
)

// REGRESSION: a self-referential value binding (`x = x` on a parameter) used to send
// functionValueTypeForExpr into unbounded recursion — it followed the symbol's value binding back to
// the same symbol forever, overflowing the goroutine stack ("fatal error: stack overflow"). A stack
// overflow is FATAL (not a recoverable panic), so the mere fact that this test runs to completion
// proves the cycle now terminates; we also assert a normal diagnostic is produced under -strict.
func TestSelfReferentialAssignmentDoesNotOverflow(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    ensure result == 5
    if not (x == 5):
        x = x
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "selfassign.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true})
	// Reaching here at all proves the recursion terminated. The unprovable `ensure result == 5` then
	// surfaces as a normal strict diagnostic rather than a crash.
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("expected the unprovable ensure to report a normal diagnostic, got:\n%s", allDiagnostics(result))
	}
}

// A mutual / chained referential binding (`a = b; b = a`) must also terminate, not just the direct
// self-reference. The visited-set guard breaks the cycle at whichever symbol is re-entered first.
// (The redeclaration itself draws a diagnostic; the point is that resolution does not overflow.)
func TestMutualReferentialBindingDoesNotOverflow(t *testing.T) {
	src := `
def g(p: i64) -> i64:
    a = p
    b = a
    a = b
    return a
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutualassign.elisa", src,
		AnalyzeOptions{})
	// No assertion on specific diagnostics — reaching here at all proves the resolution terminated
	// instead of overflowing the stack.
	_ = strings.Join(result.Errors(), "\n")
}
