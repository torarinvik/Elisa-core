//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func analyzeWPStrict(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
}

// The headline: a postcondition over a straight-line body with MUTABLE reassignments — which the
// immutable-local machinery cannot prove (the reassigned variable is a free var) — is discharged by
// weakest-precondition transport. WP threads `result > 2` backward through `y<-y*2; y<-y+1; y:=x` to
// `((x+1)*2) > 2`, which follows from `x > 0`.
func TestWPProvesMutableReassignment(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 2
    y: mutable i64 = x
    y <- y + 1
    y <- y * 2
    return y
`
	result := analyzeWPStrict(t, "wp_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("WP should prove the postcondition over the reassignment chain, got: %v", errs)
	}
}

// Soundness: a postcondition that does NOT follow must still fail. `((x+1)*2) > 100` is false for x=1,
// so WP must decline and the strict-mode error must fire.
func TestWPSoundOnFalsePostcondition(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 100
    y: mutable i64 = x
    y <- y + 1
    y <- y * 2
    return y
`
	result := analyzeWPStrict(t, "wp_false.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("a false postcondition must not be proven by WP; got errors: %v", result.Errors())
	}
}

// A constant-only chain folds entirely: `result == 6` after `y:=1; y<-y+2; y<-y*2` (1+2=3, 3*2=6)
// threads to `6 == 6`, which folds to true — discharged with no solver.
func TestWPFoldsConstantChain(t *testing.T) {
	src := `
def g() -> i64:
    ensure result > 5
    y: mutable i64 = 1
    y <- y + 2
    y <- y * 2
    return y
`
	result := analyzeWPStrict(t, "wp_const.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a constant reassignment chain must fold and prove, got: %v", errs)
	}
}

// Soundness, WP as the prover: WP is needed for this chain (the immutable-local machinery cannot prove
// it — verified by disabling WP), so a target it cannot reach must still fail. `((x+1)*2) > 1000` does
// not follow from `x > 0`, so WP must decline.
func TestWPSoundOnUnreachableTarget(t *testing.T) {
	src := `
def f(x: i64) -> i64:
    requires x > 0
    ensure result > 1000
    y: mutable i64 = x
    y <- y + 1
    y <- y * 2
    return y
`
	result := analyzeWPStrict(t, "wp_unreachable.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`((x+1)*2) > 1000` does not follow from `x>0`; WP must not prove it, got errors: %v", result.Errors())
	}
}
