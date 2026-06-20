package semantic

import (
	"testing"
)

// AUDIT (cluster C, typestate channel): a named-state field mutation through a MUTABLE ref alias
// (`q: mutable Player& = p; q.health <- -5`) must transition the borrowed root p — otherwise the false
// `ensures p => Alive` is "proven" after p is driven Dead. The resolver now follows the mutable-ref
// alias binding to p.
func TestTypestateAliasMutationTransitionsRoot(t *testing.T) {
	src := `
struct Player[state Alive | Dead]:
    health: mutable i64

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def via(p: mutable Player[Alive]&) ensures p => Alive:
    q: mutable Player& = p
    q.health <- 0 - 5
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "typestate_alias.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "cannot prove ensures") {
		t.Fatalf("a mutation through the alias drives p Dead; `ensures p => Alive` must be rejected, got:\n%s", allDiagnostics(result))
	}
}

// COMPLETENESS: an alias mutation that KEEPS the state valid still passes.
func TestTypestateAliasMutationPreservingStatePasses(t *testing.T) {
	src := `
struct Player[state Alive | Dead]:
    health: mutable i64

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def via(p: mutable Player[Alive]&) ensures p => Alive:
    q: mutable Player& = p
    q.health <- 10
`
	result := analyzeTreeTestSource(t, "typestate_alias_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("q.health <- 10 keeps p Alive; the ensures should hold, got: %v", errs)
	}
}
