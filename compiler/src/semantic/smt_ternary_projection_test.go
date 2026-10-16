//go:build cgo

package semantic

import "testing"

// A field read of a conditional value is the conditional of the field reads.
func TestSMTTernaryFieldProjection(t *testing.T) {
	src := `
def mk(n: i64) -> (len: i64, ok: bool):
    ensure result.len == n
    return (n, true)

def pick(c: bool, a: i64, b: i64) -> i64:
    ensure result == (a if c else b)
    return (mk(a) if c else mk(b)).len

def pickbad(c: bool, a: i64, b: i64) -> i64:
    ensure result == a
    return (mk(a) if c else mk(b)).len

`
	result := analyzeContractStrict(t, "ternary_projection.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "pick"); n != 0 {
		t.Fatalf("pick: expected proof, got %v", result.Errors())
	}
	// SOUNDNESS: the else arm is a different value.
	if ensureFailuresFor(result.Errors(), "pickbad") == 0 {
		t.Fatalf("pickbad: false ensure must stay unproven; got %v", result.Errors())
	}
}
