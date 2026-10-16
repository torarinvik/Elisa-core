//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Facts over branch-local immutable locals survive the if-join, rewritten through the locals'
// initializers.
func TestSMTBranchLocalFactsSurviveJoin(t *testing.T) {
	src := `
def find(t: sview, c: sview) -> usize:
    ensure result <= t.len
    return t.len

def f(text: sview, start: usize) -> usize:
    requires start < text.len
    ensure result <= text.len
    n: usize = text.len
    close: mutable usize = n
    if start + 1 < n:
        after: sview = text[start + 1:n]
        k: usize = find(after, ")")
        if k >= after.len:
            return n
        close <- start + 1 + k
    return close

def bad(text: sview, start: usize) -> usize:
    requires start < text.len
    ensure result < text.len
    n: usize = text.len
    close: mutable usize = n - 1
    if start + 1 < n:
        after: sview = text[start + 1:n]
        k: usize = find(after, ")")
        if k > after.len:
            return 0
        close <- start + 1 + k
    return close


def stale(c: bool) -> usize:
    ensure result == 11 or result == 5
    x: mutable usize = 0
    close: mutable usize = 5
    if c:
        k: usize = x + 1
        x <- 10
        close <- k
    return close

def fresh(c: bool) -> usize:
    ensure result == 1 or result == 5
    x: mutable usize = 0
    close: mutable usize = 5
    if c:
        k: usize = x + 1
        close <- k
    return close

def shadow(c: bool, y: usize) -> usize:
    requires y < 100
    ensure result == y + 1 or result == 500
    close: mutable usize = 500
    if c:
        k: usize = y + 1
        close <- k
    return close

`
	result := analyzeContractStrict(t, "branch_local.elisa", src)
	for _, name := range []string{"f", "fresh", "shadow"} {
		if n := ensureFailuresFor(result.Errors(), name); n != 0 {
			t.Fatalf("%s: expected proof, got %v", name, result.Errors())
		}
	}
	// SOUNDNESS: "bad" has a path returning text.len; in "stale" the local's input was written
	// after its declaration, so k == 1, not 11.
	for _, name := range []string{"bad", "stale"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}

// A by-value mutable bool denotes its current value; facts about it are retired at writes.
func TestSMTMutableBoolLocal(t *testing.T) {
	src := `
def bstale(c: bool) -> bool:
    ensure result == true
    b: mutable bool = true
    b <- c
    return b

def bloop(n: i64) -> bool:
    requires n > 0 and n < 10
    ensure result == true
    b: mutable bool = true
    i: mutable i64 = 0
    while i < n:
        decreases n - i
        b <- false
        i <- i + 1
    return b

def bok(c: bool) -> bool:
    ensure result == (c or true)
    b: mutable bool = false
    b <- true
    return b


const SCAN_STEP: usize = 1

def find(t: sview, c: sview) -> usize:
    ensure result <= t.len
    return t.len

def nts(text: sview, from: usize, has: bool, open: usize, close: usize) -> usize:
    requires from <= text.len and open <= text.len and close <= text.len
    ensure result <= text.len
    return text.len

def scan(text: sview, start: usize) -> usize:
    requires start <= text.len
    ensure result <= text.len
    text_length: usize = text.len
    if start >= text_length:
        return text_length
    operands: sview = text[start:text_length]
    open_relative: usize = find(operands, "(")
    close_relative: usize = find(operands, ")")
    open: mutable usize = text_length
    close: mutable usize = text_length
    has_address: mutable bool = false
    if open_relative < operands.len:
        has_address <- true
        open <- start + open_relative
        if close_relative < open_relative:
            return 0
        after_open: sview = text[open + SCAN_STEP:text_length]
        close_after_open: usize = find(after_open, ")")
        if close_after_open >= after_open.len:
            return 0
        close <- open + SCAN_STEP + close_after_open
    elif close_relative < operands.len:
        return 0
    return nts(text, start, has_address, open, close)

`
	result := analyzeContractStrict(t, "mutable_bool.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "bok"); n != 0 {
		t.Fatalf("bok: expected proof, got %v", result.Errors())
	}
	if n := requiresFailuresFor(result.Errors(), "nts"); n != 0 {
		t.Fatalf("scan: expected nts precondition proof, got %v", result.Errors())
	}
	// SOUNDNESS: the entry value `true` must not survive a write (straight-line or in a loop).
	for _, name := range []string{"bstale", "bloop"} {
		if ensureFailuresFor(result.Errors(), name) == 0 {
			t.Fatalf("%s: false ensure must stay unproven; got %v", name, result.Errors())
		}
	}
}

func requiresFailuresFor(errs []string, fn string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "precondition of \""+fn+"\"") {
			n++
		}
	}
	return n
}
