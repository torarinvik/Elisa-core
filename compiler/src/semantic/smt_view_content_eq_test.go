//go:build cgo

package semantic

import "testing"

// View content equality: equal lengths (both non-negative) and equal bytes, over the modeled
// byte arrays of parameters and their slices.
func TestSMTViewContentEquality(t *testing.T) {
	src := `
def find(text: sview, needle: sview) -> usize:
    ensure result <= text.len
    ensure result < text.len implies (needle.len > 0 and needle.len <= text.len and result <= text.len - needle.len and text[result:result + needle.len] == needle)
    text_length: usize = text.len
    needle_length: usize = needle.len
    if needle_length == 0 or text_length < needle_length:
        return text_length
    search_limit: usize = text_length - needle_length + 1
    index: mutable usize = 0
    while index < search_limit |index, search_limit, text, needle, needle_length, text_length|:
        decreases search_limit - index
        invariant index <= search_limit
        if text[index:index + needle_length] == needle:
            return index
        index <- index + 1
    return text_length

def findbad(text: sview, needle: sview) -> usize:
    ensure result < text.len implies text[result:result + needle.len] == needle
    text_length: usize = text.len
    needle_length: usize = needle.len
    if needle_length == 0 or text_length < needle_length:
        return text_length
    return 0

def eqsym(a: sview, b: sview) -> bool:
    ensure result == (b == a)
    return a == b

def eqbytes(a: sview, b: sview) -> bool:
    requires a.len > 0
    ensure result implies a[0] == b[0]
    return a == b

def eqbad(a: sview, b: sview) -> bool:
    requires a.len > 1
    ensure result implies a[0] == b[1]
    return a == b

`
	result := analyzeContractStrict(t, "view_eq.elisa", src)
	for _, name := range []string{"find", "eqsym", "eqbytes"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	// SOUNDNESS: index 0 is not a match for every text, and equal views do not relate a[0] to b[1].
	for _, name := range []string{"findbad", "eqbad"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}

// An array's length symbol must not share a name with a local spelled `<array>_len`.
func TestSMTArrayLengthSymbolDistinctFromLocal(t *testing.T) {
	src := `
def okc(view: sview) -> bool:
    requires view.len > 1
    ensure result == (view[0] == view[1])
    view_len: i64 = view.len
    return view[0] == view[1] and view_len > 1


def okc_bad(view: sview) -> bool:
    requires view.len > 1
    ensure result == (view[0] != view[1])
    view_len: i64 = view.len
    return view[0] == view[1] and view_len > 1

`
	result := analyzeContractStrict(t, "array_len_name.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "okc"); n != 0 {
		t.Fatalf("okc: expected proof, got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "okc_bad") == 0 {
		t.Fatalf("okc_bad: false ensure must stay unproven; got %v", result.Errors())
	}
}
