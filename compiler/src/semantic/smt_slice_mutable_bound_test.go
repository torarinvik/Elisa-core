//go:build cgo

package semantic

import "testing"

// A slice local `s: sview = text[a:n]` over a by-value mutable local `a` keeps its length
// equation while `a` is unwritten since the slice; a later write retires it.
func TestSMTSliceLocalOverMutableBound(t *testing.T) {
	positive := `
def find(t: sview, c: sview) -> usize:
    ensure result <= t.len
    return t.len

def ok_slice(text: sview, from: usize, close: usize) -> usize:
    requires close <= text.len and from <= text.len
    ensure result <= text.len
    n: usize = text.len
    a: mutable usize = close + 1
    a <- from if from > a
    if a < n:
        s: sview = text[a:n]
        k: usize = find(s, ",")
        if k < s.len:
            return a + k
    return n
`
	result := analyzeContractStrict(t, "ok_slice.elisa", positive)
	if n := ensureFailuresFor(result.Errors(), "ok_slice"); n != 0 {
		t.Fatalf("ok_slice: expected proof, got %v", result.Errors())
	}
	// SOUNDNESS: after `a <- n` the stale equation would give s.len == 0, making the branch
	// vacuous and "proving" the false `result <= text.len` for `n + 1`.
	negative := `
def find(t: sview, c: sview) -> usize:
    ensure result <= t.len
    return t.len

def bad_slice(text: sview, close: usize) -> usize:
    requires close <= text.len
    ensure result <= text.len
    n: usize = text.len
    a: mutable usize = close
    if a < n:
        s: sview = text[a:n]
        a <- n
        k: usize = find(s, ",")
        if k < s.len:
            return n + 1
    return n
`
	result = analyzeContractStrict(t, "bad_slice.elisa", negative)
	if ensureFailuresFor(result.Errors(), "bad_slice") == 0 {
		t.Fatalf("bad_slice: false ensure must stay unproven; got %v", result.Errors())
	}
}
