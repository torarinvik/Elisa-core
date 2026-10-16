//go:build cgo

package semantic

import "testing"

// A tuple field named `count`/`len` is a field read, not a container length: guards over it
// (`result.count == 2 implies ...`) translate, so call preconditions under them can discharge.
func TestSMTRecordFieldNamedCount(t *testing.T) {
	src := `
const SCAN_STEP: usize = 1

def fob(text: sview, index: usize) -> usize:
    requires index <= text.len
    ensure result <= text.len
    return text.len

def nts(text: sview, from: usize, has: bool, open: usize, close: usize) -> usize:
    requires from <= text.len and open <= text.len and close <= text.len
    ensure result <= text.len
    return text.len

def sc(text: sview) -> (start: usize, count: usize, first_separator: usize, last_separator: usize, has_address_group: bool, address_open: usize, address_close: usize, valid: bool):
    ensure result.start <= text.len
    ensure result.first_separator <= text.len and result.last_separator <= text.len
    ensure result.address_open <= text.len and result.address_close <= text.len
    ensure result.valid and result.count == 2 implies (
        result.first_separator < text.len and result.first_separator == result.last_separator and
        result.first_separator == nts(text, result.start, result.has_address_group, result.address_open, result.address_close) and
        fob(text[result.start:result.first_separator], 0.usize()) < (text[result.start:result.first_separator]).len
    )
    n: usize = text.len
    return (n, 0, n, n, false, n, n, true)


def cnt(x: usize) -> (count: usize, len: usize):
    requires x < 100
    ensure result.count == x + 1
    ensure result.len == 3
    return (x + 1, 3)

def cntbad(x: usize) -> (count: usize, len: usize):
    requires x < 100
    ensure result.count == x
    return (x + 1, 3)

`
	result := analyzeContractStrict(t, "record_count.elisa", src)
	if n := requiresFailuresFor(result.Errors(), "fob"); n != 0 {
		t.Fatalf("sc: expected fob precondition proof, got %v", result.Errors())
	}
	if n := ensureFailuresFor(result.Errors(), "cnt"); n != 0 {
		t.Fatalf("cnt: expected proof, got %v", result.Errors())
	}
	// SOUNDNESS: the field value is the constructed one.
	if ensureFailuresFor(result.Errors(), "cntbad") == 0 {
		t.Fatalf("cntbad: false ensure must stay unproven; got %v", result.Errors())
	}
}
