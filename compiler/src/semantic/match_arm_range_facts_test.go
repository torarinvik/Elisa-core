package semantic

import "testing"

// TestMatchArmIntegerScrutineeFact: inside a literal integer match arm, the scrutinee carries the
// equality fact (k == 5) so refinement obligations that follow from it prove statically.
//
// Before the fix, the scrutinee range was unseeded → the `k is Five` refinement was unproven and
// emitted a hard error under EnforceStrictProofs. After the fix, the `match k: 5:` arm seeds
// k ∈ [5,5], which entails `k is Five` (self == 5) → zero errors.
func TestMatchArmIntegerScrutineeFact(t *testing.T) {
	src := `
law Five(self: i64) = self == 5

def f(k: i64) -> i64:
    match k:
        5:
            x: i64 is Five = k
            return x
        _:
            return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_arm_int_fact.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("match arm for literal 5 should seed k==5 and prove `k is Five` statically, got errors: %v", result.Errors())
	}
}

// TestMatchArmIntegerScrutineeFactNoLeak: the range fact from one arm must NOT be visible in
// another arm or after the match. Without leakage, the refinement obligation in the `_` arm
// (where k != 5) is unproven → error under EnforceStrictProofs.
func TestMatchArmIntegerScrutineeFactNoLeak(t *testing.T) {
	src := `
law Five(self: i64) = self == 5

def f(k: i64) -> i64:
    match k:
        5:
            return k
        _:
            x: i64 is Five = k
            return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "match_arm_int_fact_noleak.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatal("fact from the `5` arm must NOT leak into the `_` arm; the refinement must remain unproven")
	}
}
