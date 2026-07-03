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
