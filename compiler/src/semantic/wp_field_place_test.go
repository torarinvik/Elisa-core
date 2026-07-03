//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Brick 118-A: the weakest-precondition engine can thread a SCALAR STRUCT-FIELD place mutated through a
// `mutable T&` parameter — the shape of every recursive-descent parser's `advance()`. A conditional
// increment `if p.pos < p.stop: p.pos <- p.pos + 1` must discharge the MONOTONE postcondition relating
// the exit cursor to `old(p.pos)`: it holds on the advancing branch (`old+1 >= old`) and the saturating
// branch (`old >= old`) alike. This is the fact Brick 118-D folds into the `decreases` proof.
func TestWPFieldPlaceMonotoneProven(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def advance(p: mutable Parser&) changes p:
    ensure p.pos >= old(p.pos)
    if p.pos < p.stop:
        p.pos <- p.pos + 1
`
	if errs := analyzeContractStrict(t, "wp_field_monotone.elisa", src).Errors(); len(errs) != 0 {
		t.Fatalf("conditional-increment field place: monotone postcondition must discharge, got: %v", errs)
	}
}

// The UNCONDITIONAL-strict postcondition `ensure p.pos > old(p.pos)` is FALSE for the same body: at EOF
// (`p.pos >= p.stop`) the cursor does not advance. WP must REJECT it — this is exactly the conditional
// progress that motivates the implication postcondition (Brick 118-B). A false field-place ensure that
// merely printed the same symbol on both sides (unthreaded) would spuriously pass, so this pins the
// exit-vs-entry threading.
func TestWPFieldPlaceUnconditionalStrictRejected(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def advance(p: mutable Parser&) changes p:
    ensure p.pos > old(p.pos)
    if p.pos < p.stop:
        p.pos <- p.pos + 1
`
	errs := strings.Join(analyzeContractStrict(t, "wp_field_strict.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("unconditional strict `p.pos > old(p.pos)` is false at EOF and must be rejected, got: %v", errs)
	}
}

// Brick 118-B: the IMPLICATION postcondition `ensure GUARD => POST` (and its `implies` spelling) makes
// the conditional progress expressible and PROVABLE. The advancing branch discharges POST directly; the
// saturating (EOF) branch discharges because the guard `old(p.pos) < p.stop` is false there. This is the
// exact `guard => strict` contract Brick 118-D consumes to prove the parser's measure decreases.
func TestWPFieldPlaceConditionalStrictProven(t *testing.T) {
	fatArrow := `
struct Parser:
    pos: mutable usize
    stop: usize

def advance(p: mutable Parser&) changes p:
    ensure old(p.pos) < p.stop => p.pos > old(p.pos)
    if p.pos < p.stop:
        p.pos <- p.pos + 1
`
	if errs := analyzeContractStrict(t, "wp_field_impl_arrow.elisa", fatArrow).Errors(); len(errs) != 0 {
		t.Fatalf("`ensure GUARD => strict` conditional postcondition must discharge, got: %v", errs)
	}

	// The `implies` keyword spelling desugars identically and must prove the same contract.
	impliesKw := strings.Replace(fatArrow, "=>", "implies", 1)
	if errs := analyzeContractStrict(t, "wp_field_impl_kw.elisa", impliesKw).Errors(); len(errs) != 0 {
		t.Fatalf("`ensure GUARD implies strict` must discharge identically to `=>`, got: %v", errs)
	}
}

// The implication must not be a rubber stamp: a FALSE consequent under a satisfiable guard is still
// rejected. `old(p.pos) < p.stop => p.pos > old(p.pos) + 5` cannot hold (the body advances by at most 1).
func TestWPFieldPlaceConditionalFalseConsequentRejected(t *testing.T) {
	src := `
struct Parser:
    pos: mutable usize
    stop: usize

def advance(p: mutable Parser&) changes p:
    ensure old(p.pos) < p.stop => p.pos > old(p.pos) + 5
    if p.pos < p.stop:
        p.pos <- p.pos + 1
`
	errs := strings.Join(analyzeContractStrict(t, "wp_field_impl_false.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("an implication with an unprovable consequent must still be rejected, got: %v", errs)
	}
}

// A field place that genuinely DECREASES must be rejected for a `>=` postcondition — guards against the
// engine folding a field read to `true` reflexively regardless of the mutation direction.
func TestWPFieldPlaceDecrementRejected(t *testing.T) {
	src := `
struct Cursor:
    pos: mutable usize

def back(c: mutable Cursor&) changes c:
    ensure c.pos >= old(c.pos)
    c.pos <- c.pos - 1
`
	errs := strings.Join(analyzeContractStrict(t, "wp_field_decrement.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("a decrementing field place must not prove `>= old`, got: %v", errs)
	}
}

// SOUNDNESS (aliasing gate): with TWO reference-typed parameters, two syntactic field paths (`a.pos`,
// `b.pos`) could denote the SAME location, so a WP-threaded write to one could miss the aliased read of
// the other. Field-place WP must DECLINE here — the monotone postcondition is rejected even though it
// looks locally true, because `mutable T&` reference params are correctly counted (they wrap in a
// MutableType). A false accept would be an aliasing unsoundness.
func TestWPFieldPlaceTwoRefParamsDeclined(t *testing.T) {
	src := `
struct Cursor:
    pos: mutable usize

def bump(a: mutable Cursor&, b: mutable Cursor&) changes a.pos:
    ensure a.pos >= old(a.pos)
    a.pos <- a.pos + 1
`
	errs := strings.Join(analyzeContractStrict(t, "wp_two_ref.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "could not be proven statically") {
		t.Fatalf("field-place WP must decline with 2 reference params (aliasing), got: %v", errs)
	}
}
