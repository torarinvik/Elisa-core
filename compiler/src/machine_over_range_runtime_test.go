package main

import "testing"

// docs/122 §5.2 / docs/125 §1 — `machine over` arm headers accept RANGE alternatives
// (`'0'..='9'`), shared with the match/when pattern grammar. A range lowers to a bounds
// test on the input (`lo <= input and input <(=) hi`), OR'd with literal alternatives.
// This drives a small cursor over a byte string and counts characters by class, exercising
// inclusive ranges, exclusive ranges, mixed range|literal alternation, and the wildcard
// fallback — then checks the tallies at runtime.
func TestMachineOverRangeRuntime(t *testing.T) {
	body := `
struct Cursor:
    data: darray[char]
    pos: mutable usize
    digits: mutable i64
    hexish: mutable i64

def at(cur: Cursor&) -> char:
    return cur.data[cur.pos]

def has_more(cur: Cursor&) -> bool:
    return cur.pos < cur.data.count

# digits counted via an inclusive range, hex letters via mixed range|range|literal
# alternation, everything else falls to the wildcard. All mutable state lives in the
# driven resource cur (arms may mutate only it).
def tally(text: darray[char]) -> i64:
    cur: mutable Cursor = Cursor{data: text, pos: 0, digits: 0, hexish: 0}
    machine over cur.at() while cur.has_more():
        state Go
        start Go
        Go, '0'..='9':
            cur.digits <- cur.digits + 1
            cur.pos <- cur.pos + 1
            -> Go
        Go, 'a'..<'g' | 'A'..<'G' | '_':
            cur.hexish <- cur.hexish + 1
            cur.pos <- cur.pos + 1
            -> Go
        Go, _:
            cur.pos <- cur.pos + 1
            -> Go
    return cur.digits * 100 + cur.hexish

@test
def range_arms_tally() -> void:
    can Abort.Panic:
        # "12ab_xZ9" -> digits: 1,2,9 = 3 ; hexish: a,b,_ = 3 (x and Z are not in a..<g / A..<G)
        mixed: darray[char] = ['1', '2', 'a', 'b', '_', 'x', 'Z', '9']
        if tally(mixed) != 303:
            panic("mixed range/literal alternation tally wrong")
        empty: darray[char] = []
        if tally(empty) != 0:
            panic("empty input")
        # exclusive upper bound must exclude g/G, include f/F
        boundary: darray[char] = ['f', 'F', 'g', 'G']
        if tally(boundary) != 2:
            panic("exclusive upper bound wrong")
`
	exit, stdout, stderr := runStressProgram(t, "machine_over_range", body)
	assertAllPassed(t, exit, stdout, stderr, "range_arms_tally")
}
