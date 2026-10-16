//go:build cgo

package semantic

import "testing"

// In a body that writes no memory, an immutable sview parameter is one byte array for the whole
// function, so a call-initialized local and the same call in a contract share a symbol.
func TestSMTViewFrameWriteFreeCallCongruence(t *testing.T) {
	positive := `
def fob(text: sview, i: usize) -> usize:
    requires i <= text.len
    ensure result <= text.len
    return text.len

def same_call(text: sview) -> usize:
    ensure result == fob(text, 0.usize())
    s: usize = fob(text, 0.usize())
    k: mutable usize = 0
    k <- s if s > 3
    if s >= text.len:
        return text.len
    return s
`
	result := analyzeContractStrict(t, "same_call.elisa", positive)
	if n := ensureFailuresFor(result.Errors(), "same_call"); n != 0 {
		t.Fatalf("same_call: expected proof, got %v", result.Errors())
	}
	// SOUNDNESS: different arguments are different calls.
	wrongArg := `
def fob(text: sview, i: usize) -> usize:
    requires i <= text.len
    ensure result <= text.len
    return i

def other_call(text: sview) -> usize:
    requires text.len > 2
    ensure result == fob(text, 1.usize())
    s: usize = fob(text, 0.usize())
    return s
`
	result = analyzeContractStrict(t, "other_call.elisa", wrongArg)
	if ensureFailuresFor(result.Errors(), "other_call") == 0 {
		t.Fatalf("other_call: false ensure must stay unproven; got %v", result.Errors())
	}
	// SOUNDNESS: a body that may write memory (a call with a mutable argument) keeps the
	// per-read modeling; the two calls are not identified.
	writes := `
def fob(text: sview, i: usize) -> usize:
    requires i <= text.len
    ensure result <= text.len
    return text.len

def poke(xs: mutable darray[u8]&):
    xs.push(1)

def writes_between(text: sview, xs: mutable darray[u8]&) -> usize:
    ensure result == fob(text, 0.usize())
    s: usize = fob(text, 0.usize())
    poke(xs)
    return s
`
	result = analyzeContractStrict(t, "writes_between.elisa", writes)
	if ensureFailuresFor(result.Errors(), "writes_between") == 0 {
		t.Fatalf("writes_between: a writing body must not identify the calls; got %v", result.Errors())
	}
}
