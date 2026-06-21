//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"
)

// countAssertProof counts proof-report entries for a plain `assert` discharged with the given outcome.
func countAssertProof(result *Result, outcome ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if f.Subject == "assert" && f.Predicate == "assert" && f.Outcome == outcome {
			n++
		}
	}
	return n
}

func analyzeStrictSMT(t *testing.T, name, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; plain-assert SMT discharge test skipped")
	}
	return analyzeProofHoleWithOptions(t, name, src, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
}

// docs/101 — a PLAIN `assert COND` (no `by:`) now attempts the same static discharge ladder as
// `assert … by:`. Here `i > 1` follows from the `requires i >= 3` fact, so the SMT tier closes it and
// a ProofProvenSMT (or linear) proof is recorded WITHOUT the opt-in proof-hole hint flag. Previously a
// plain assert was never statically discharged unless EmitProofHoleHints was set.
func TestPlainAssertDischargesFromInScopeFacts(t *testing.T) {
	src := `
def ok(i: i64):
    requires i >= 3
    assert(i > 1)
`
	result := analyzeStrictSMT(t, "plain_assert_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a provable plain assert must analyze cleanly, got: %v", errs)
	}
	if got := countAssertProof(result, ProofProvenSMT) + countAssertProof(result, ProofProvenLinear); got != 1 {
		t.Fatalf("expected the plain assert to be statically discharged (1 proof), got %d: %+v", got, result.ProofReport)
	}
	if got := countAssertProof(result, ProofRuntime); got != 0 {
		t.Fatalf("a discharged plain assert must not also be recorded as a runtime check, got %d: %+v", got, result.ProofReport)
	}
}

// A plain assert provable only via a nonlinear fact (the affine tier declines var*var) discharges via
// the SMT tier — exactly the kind of goal that would prove under `assert … by:` but, before this
// change, did NOT prove as a plain assert.
func TestPlainAssertDischargesNonlinearViaSMT(t *testing.T) {
	src := `
def scale(a: i64, b: i64):
    requires a >= 2
    requires a <= 10
    requires b >= 2
    requires b <= 10
    assert(a * b >= 4)
`
	result := analyzeStrictSMT(t, "plain_assert_nonlinear.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a provable nonlinear plain assert must analyze cleanly, got: %v", errs)
	}
	if got := countAssertProof(result, ProofProvenSMT); got != 1 {
		t.Fatalf("expected the nonlinear plain assert to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS: an UNPROVABLE plain assert stays a RUNTIME CHECK — it must NOT become a hard error (that
// would reject previously-accepted code). Nothing bounds `i` above relative to `n`, so the ladder
// declines and the assert falls back to ProofRuntime with no error emitted.
func TestPlainAssertUnprovableStaysRuntimeCheck(t *testing.T) {
	src := `
def get(i: i64, n: i64):
    requires i >= 0
    assert(i < n)
`
	result := analyzeStrictSMT(t, "plain_assert_unprovable.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an unprovable plain assert must NOT be a hard error (stays a runtime check), got: %v", errs)
	}
	if got := countAssertProof(result, ProofProvenSMT) + countAssertProof(result, ProofProvenLinear); got != 0 {
		t.Fatalf("an unprovable plain assert must not record a static proof, got %d: %+v", got, result.ProofReport)
	}
}

// `assert … by:` semantics are UNCHANGED: a condition provable only via its proof block still
// discharges cleanly, and a `by:` block whose facts do not entail COND still fails under strict proofs.
func TestAssertBySemanticsUnchangedByPlainAssertDischarge(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; assert-by regression test skipped")
	}
	provable := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by:
        assert(n >= 10)
        weaken(n)
`
	result := analyzeWithSMT(t, "assert_by_unchanged_ok.elisa", provable)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a provable `assert … by:` must still analyze cleanly, got: %v", errs)
	}

	failing := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by:
        weaken(n)
`
	bad := analyzeProofHoleWithOptions(t, "assert_by_unchanged_fail.elisa", failing,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if joined := strings.Join(bad.Errors(), "\n"); !strings.Contains(joined, "could not be proven") {
		t.Fatalf("an unprovable `assert … by:` must still fail under strict proofs, got: %v", bad.Errors())
	}
}
