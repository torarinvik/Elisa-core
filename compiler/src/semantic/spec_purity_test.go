package semantic

import (
	"strings"
	"testing"
)

// A `requires`/`ensure` clause that only calls PURE helpers (and reads its parameters) is accepted.
// The verifier canonicalizes `doubled(x)` to a deterministic uninterpreted symbol; that is sound
// because `doubled` performs no effects. Must NOT be over-rejected.
func TestSemanticSpecClausePureHelperAccepted(t *testing.T) {
	src := `
def doubled(x: i32) -> i32:
    return x * 2

def f(x: i32) -> i32:
    requires doubled(x) > 0
    ensure result == doubled(x)
    return x * 2
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "spec_pure_helper.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, "must be pure") {
			t.Fatalf("pure contract should be accepted, got purity error: %v", result.Errors())
		}
	}
}

// A `requires` clause that reads a mutable global is effectful (Global.Read): the same syntactic
// clause can denote different values at different program points, so the verifier's
// single-deterministic-symbol model is unsound. Must be rejected.
func TestSemanticRequiresEffectfulRejected(t *testing.T) {
	src := `
global mutable g: i32 = 0

def f(x: i32) -> i32:
    requires g > 0
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "spec_req_effect.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "requires clause must be pure") {
		t.Fatalf("expected requires-purity rejection, got errors: %v", result.Errors())
	}
}

// Same rule for `ensure`.
func TestSemanticEnsureEffectfulRejected(t *testing.T) {
	src := `
global mutable g: i32 = 0

def f(x: i32) -> i32:
    ensure result > g
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "spec_ens_effect.elisa", src)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "ensure clause must be pure") {
		t.Fatalf("expected ensure-purity rejection, got errors: %v", result.Errors())
	}
}
