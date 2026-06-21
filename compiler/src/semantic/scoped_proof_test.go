//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/99 — SCOPED PROOF AUTOMATION ("automation with walls").
//
// A `by scoped:` proof block is a CLOSED WORLD: the goal is discharged using ONLY the facts cited
// inside the block (lemma `use`-calls / nested asserts). Ambient flow and assert facts from the
// enclosing scope are WALLED OUT, so the verdict is STABLE — it depends solely on what the block cites,
// never on unrelated surrounding code that would otherwise perturb the solver's hypothesis set.

// POSITIVE: the goal proves under `by scoped:` because the block cites exactly the lemma it needs. The
// caller knows nothing about `n`, so a bare `assert n >= 5` is unprovable; the block establishes
// `n >= 10` (a nested assert seed) and weakens it to `n >= 5` via the cited lemma. The wall does not
// block facts established INSIDE the block, only ambient ones — so the right citations still prove.
func TestScopedProofProvesWithRightCitations(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by scoped:
        assert(n >= 10)
        weaken(n)
`
	result := analyzeWithSMT(t, "scoped_proof_positive.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis: the scoped assert is provable from its own citations, got: %v", errs)
	}
}

// THE WALL IS REAL: the SAME goal DECLINES under `by scoped:` when the establishing citation is
// removed. The block calls `weaken` WITHOUT first establishing `n >= 10`, so the lemma's `requires`
// is unmet and the goal cannot follow from the (now empty) cited world.
func TestScopedProofDeclinesWhenCitationRemoved(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64):
    assert n >= 5 by scoped:
        weaken(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scoped_proof_declines.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven") {
		t.Fatalf("expected the scoped assert to decline without its establishing citation, got: %v", result.Errors())
	}
}

// STABILITY / CLOSED-WORLD: an ambient fact in the enclosing scope is WALLED OUT of a `by scoped:`
// block. The caller's `requires n >= 100` makes `n >= 5` trivially provable in the OPEN world — but a
// scoped block that does NOT re-cite it must DECLINE, proving that surrounding facts cannot silently
// drive (or destabilize) the proof. This is the contrast that distinguishes scoped from plain `by:`.
func TestScopedProofWallsOutAmbientFacts(t *testing.T) {
	src := `
def use(n: i64):
    requires n >= 100
    assert n >= 5 by scoped:
        pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scoped_proof_walls_ambient.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "could not be proven") {
		t.Fatalf("expected the scoped assert to wall out the ambient `requires n >= 100` and decline, got: %v", result.Errors())
	}
}

// CONTRAST CONTROL: the very same body with a PLAIN `by:` block (open world) PROVES, because the
// ambient `requires n >= 100` is visible. This pins the difference to the wall, not to a coincidental
// solver decline — `by:` proves, `by scoped:` (previous test) declines, on identical facts.
func TestPlainByProvesFromAmbientFactsControl(t *testing.T) {
	src := `
def use(n: i64):
    requires n >= 100
    assert n >= 5 by:
        pass
`
	result := analyzeWithSMT(t, "plain_by_ambient_control.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected the open-world `by:` block to prove from the ambient requires, got: %v", errs)
	}
}
