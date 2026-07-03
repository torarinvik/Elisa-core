//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Brick 118-C/D: a self-recursive function whose measure decreases ONLY through a callee's side effect —
// the recursive call re-passes the same `mutable Parser&`, so argument substitution reports the measure
// unchanged (Wall 1). The consumer `advance(p)` carries `ensure p.pos > old(p.pos)`; composing that
// summary proves `decreases p.stop - p.pos` strictly decreases. This is the recursive-descent parser
// termination pattern, proven at compile time with zero runtime cost.
func TestTerminationDecreasesViaCalleeSummaryProven(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def advance(p: mutable Parser&) changes p.pos:
    requires p.pos < p.stop
    ensure p.pos > old(p.pos)
    ensure p.pos <= p.stop
    p.pos <- p.pos + 1

def walk(p: mutable Parser&) -> void:
    decreases p.stop - p.pos
    if p.pos >= p.stop:
        return
    advance(p)
    walk(p)
`
	errs := analyzeContractStrict(t, "callee_summary_proven.elisa", src).Errors()
	joined := strings.Join(errs, "\n")
	if strings.Contains(joined, "decreases") || strings.Contains(joined, "may not terminate") {
		t.Fatalf("measure decreasing via advance()'s ensure must be proven terminating, got: %v", errs)
	}
}

// Soundness: if the consumer does NOT guarantee progress (its `ensure` only says the cursor is
// non-decreasing, `>= old`), the measure is not proven to strictly decrease and the recursion is
// correctly REFUTED — the summary is not a rubber stamp.
func TestTerminationDecreasesViaCalleeSummaryNonStrictRefuted(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def touch(p: mutable Parser&) changes p.pos:
    requires p.pos < p.stop
    ensure p.pos >= old(p.pos)
    ensure p.pos <= p.stop
    p.pos <- p.pos

def walk(p: mutable Parser&) -> void:
    decreases p.stop - p.pos
    if p.pos >= p.stop:
        return
    touch(p)
    walk(p)
`
	errs := strings.Join(analyzeContractStrict(t, "callee_summary_nonstrict.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "may not terminate") {
		t.Fatalf("a merely non-decreasing (>=) consumer must NOT prove termination, got: %v", errs)
	}
}

// Soundness: a consumer that mutates the cursor the WRONG way (decreases pos, so `stop - pos` grows) must
// be refuted.
func TestTerminationDecreasesViaCalleeSummaryWrongDirectionRefuted(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def back(p: mutable Parser&) changes p:
    ensure p.pos < old(p.pos)
    p.pos <- p.pos - 1

def walk(p: mutable Parser&) -> void:
    decreases p.stop - p.pos
    back(p)
    walk(p)
`
	errs := strings.Join(analyzeContractStrict(t, "callee_summary_wrongdir.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "may not terminate") {
		t.Fatalf("a consumer moving the cursor the wrong way must be refuted, got: %v", errs)
	}
}
