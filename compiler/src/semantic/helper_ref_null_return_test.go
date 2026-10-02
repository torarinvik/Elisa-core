package semantic

import (
	"strings"
	"testing"
)

// `return null` from a `T&?` helper borrows nothing, so it must not make the helper's
// return summary unknown: the `&b[0]` path still ties the result to `b`.
func TestOptionalRefHelperNullReturnKeepsBorrowSummary(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "opt_ref_helper_null.elisa", `
def maybe(b: darray[i64]&, k: bool) -> i64&?:
    if k:
        return &b[0]
    return null

def run(b: mutable darray[i64]&) -> i64:
    r: i64&? = maybe(b, true)
    for i in 0..<5000:
        b.push(9)
    if r is v:
        return v
    return 1
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "cannot be used") {
		t.Fatalf("expected the helper-borrowed ref to be rejected after growth, got:\n%s", got)
	}
}
