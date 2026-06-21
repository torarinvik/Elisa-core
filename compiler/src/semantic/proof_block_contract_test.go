//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestStandaloneProofBlockExportsOnlyGoal(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def use(n: i64) -> i64:
    proof n >= 5:
        assert(n >= 10)
        weaken(n)
    assert(n >= 5)
    return n
`
	result := analyzeContractStrict(t, "proof_block_exports_goal.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected standalone proof goal to be available downstream, got: %v", errs)
	}
}

func TestStandaloneProofBlockWallsOutAmbientFacts(t *testing.T) {
	src := `
def use(n: i64):
    requires n >= 100
    proof n >= 5:
        pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "proof_block_walls_ambient.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "proof goal could not be proven") {
		t.Fatalf("expected proof block to wall out ambient facts, got: %v", result.Errors())
	}
}

func TestEnsureByScopedProofAccepted(t *testing.T) {
	src := `
lemma weaken(x: i64):
    requires x >= 10
    ensure x >= 5
    pass

def f(n: i64) -> i64:
    ensure result >= 5 by scoped:
        assert(result >= 10)
        weaken(result)
    return n + 10
`
	result := analyzeContractStrict(t, "ensure_by_scoped.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected scoped ensure proof to discharge cleanly, got: %v", errs)
	}
}

func TestEnsureByScopedProofRejectsEffectfulBody(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    ensure result >= 5 by scoped:
        n <- 10
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensure_by_scoped_effectful.elisa", src,
		AnalyzeOptions{EnableSMT: true})
	if joined := strings.Join(result.Errors(), "\n"); !strings.Contains(joined, "verification-only") {
		t.Fatalf("expected effectful scoped ensure proof body to be rejected, got: %v", result.Errors())
	}
}
