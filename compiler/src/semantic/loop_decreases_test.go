//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A leading `decreases` measure that strictly drops and stays >= 0 each iteration proves the loop
// terminates — no error. `decreases i` for `while i > 0: i <- i - 1` is the canonical countdown.
func TestLoopDecreasesCountdownTerminates(t *testing.T) {
	src := `def countdown(n: i64):
    requires n >= 0
    i: mutable i64 = n
    while i > 0:
        decreases i
        i <- i - 1
`
	for _, e := range analyzeContractStrict(t, "countdown.elisa", src).Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") {
			t.Fatalf("a strictly-decreasing bounded measure must prove termination, got: %v", e)
		}
	}
}

// A relational measure `n - i` proves termination once an `invariant i >= 0` supplies the bound the
// signed-overflow model needs (without it, n - i could overflow). Demonstrates measure + invariant.
func TestLoopDecreasesCountupWithInvariant(t *testing.T) {
	src := `def countup(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        invariant i >= 0
        decreases n - i
        i <- i + 1
`
	for _, e := range analyzeContractStrict(t, "countup.elisa", src).Errors() {
		if strings.Contains(e, "terminate") || strings.Contains(e, "decreases") {
			t.Fatalf("n - i with invariant i >= 0 must prove termination, got: %v", e)
		}
	}
}

// SOUNDNESS: a measure that INCREASES (`decreases i` while `i <- i + 1`) must be refuted — the loop
// may not terminate.
func TestLoopDecreasesIncreasingMeasureRejected(t *testing.T) {
	src := `def bad(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        decreases i
        i <- i + 1
`
	errs := strings.Join(analyzeContractStrict(t, "bad_loop.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "may not terminate") {
		t.Fatalf("an increasing `decreases` measure must be rejected, got: %v", errs)
	}
}

// SOUNDNESS: a measure with NO provable lower bound (signed `n - i` without the i >= 0 invariant) must
// be refuted — the difference could overflow, so the decrease is not sound.
func TestLoopDecreasesUnboundedMeasureRejected(t *testing.T) {
	src := `def maybe(n: i64):
    requires n >= 0
    i: mutable i64 = 0
    while i < n:
        decreases n - i
        i <- i + 1
`
	errs := strings.Join(analyzeContractStrict(t, "unbounded_loop.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "may not terminate") {
		t.Fatalf("an unbounded signed measure must be rejected (overflow), got: %v", errs)
	}
}

// SOUNDNESS: a body the analyzer cannot model (a call) cannot be proven terminating — the explicit
// `decreases` claim is reported, not silently trusted.
func TestLoopDecreasesUnmodelableBodyRejected(t *testing.T) {
	src := `def sink(x: i64):
    return

def loop(n: i64):
    requires n >= 0
    i: mutable i64 = n
    while i > 0:
        decreases i
        sink(i)
        i <- i - 1
`
	errs := strings.Join(analyzeContractStrict(t, "unmodelable_loop.elisa", src).Errors(), "\n")
	if !strings.Contains(errs, "cannot model") && !strings.Contains(errs, "may not terminate") {
		t.Fatalf("an unmodelable loop body must not silently pass its `decreases` claim, got: %v", errs)
	}
}
