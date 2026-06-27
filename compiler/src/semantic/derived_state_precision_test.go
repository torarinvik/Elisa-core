//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const derivedStatePlayerPreamble = `struct Player[state Alive | Dead]:
	health: mutable i64

	derive state:
		Alive when self.health > 0
		Dead when self.health <= 0

def expect_alive(p: mutable Player[Alive]&):
	pass

`

func analyzeDerivedStatePrecision(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
}

// A field mutation that PROVABLY preserves the derived-state condition keeps the named state narrow:
// from Alive (`health > 0`), `health <- health + 1` stays Alive (`health + 1 > 0`), so the value still
// passes where `Player[Alive]&` is required — no `ensures`/widening, proven via the SMT exclusion of Dead.
func TestDerivedStatePreservedByIncrement(t *testing.T) {
	src := derivedStatePlayerPreamble + `def heal(p: mutable Player[Alive]&):
	p.health <- p.health + 1
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_heal.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("an increment that provably keeps health > 0 must preserve Alive, got: %v", errs)
	}
}

// Soundness: a field mutation that could BREAK the condition must NOT be narrowed. `health <- health -
// amt` with `amt > 0` can reach Dead, so the state widens to `Alive | Dead` and the strict-balance /
// expect_alive checks fire — the prover cannot exclude Dead.
func TestDerivedStateNotPreservedByArbitraryDecrement(t *testing.T) {
	src := derivedStatePlayerPreamble + `def damage(p: mutable Player[Alive]&, amt: i64):
	requires amt > 0
	p.health <- p.health - amt
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_damage.elisa", src)
	if !strings.Contains(allDiagnostics(result), "Player[Alive | Dead]") {
		t.Fatalf("a decrement that may reach Dead must widen the state, not narrow to Alive; got: %s", allDiagnostics(result))
	}
}

// A decrement bounded to stay positive preserves Alive: from Alive, `health <- health - 1` is NOT safe
// in general (health could be 1 -> 0 = Dead), so this must WIDEN — a precise soundness boundary.
func TestDerivedStateDecrementByOneWidens(t *testing.T) {
	src := derivedStatePlayerPreamble + `def tick(p: mutable Player[Alive]&):
	p.health <- p.health - 1
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_tick.elisa", src)
	if !strings.Contains(allDiagnostics(result), "Player[Alive | Dead]") {
		t.Fatalf("health-1 from health>0 can reach 0 (Dead), so it must widen; got: %s", allDiagnostics(result))
	}
}

// A precondition that keeps the decrement safe DOES preserve Alive: `requires p.health > amt` (with
// amt >= 0) means `health - amt > 0`, so the SMT exclusion of Dead succeeds even for a symbolic amount.
func TestDerivedStatePreservedByGuardedDecrement(t *testing.T) {
	src := derivedStatePlayerPreamble + `def safe_damage(p: mutable Player[Alive]&, amt: i64):
	requires amt >= 0
	requires p.health > amt
	p.health <- p.health - amt
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_guarded.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a decrement guarded by `health > amt` provably stays Alive, got: %v", errs)
	}
}

// Multi-state entry with a requires-only guarantee: the entry type is multi-state (Alive | Dead), so
// no single-state hypothesis exists. But a `requires starting_health > 0` clause alone is enough
// for SMT to exclude Dead from the post-assignment value — the fix lets derivedStateProvablyExcluded
// run without a hypExpr, falling back to the standard requires hypotheses.
func TestDerivedStateNarrowedByRequiresWithNoEntryHypothesis(t *testing.T) {
	src := derivedStatePlayerPreamble + `def init_player(p: mutable Player&, starting_health: i64):
	requires starting_health > 0
	p.health <- starting_health
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_init_player.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("requires starting_health > 0 should let SMT exclude Dead from the post-value, preserving Alive; got: %v", errs)
	}
}

// Soundness-negative for the multi-state requires-only path: assigning an unconstrained value to
// health must NOT yield a narrowed Alive state — the prover cannot exclude Dead (val could be <= 0).
func TestDerivedStateNotNarrowedByUnconstainedAssign(t *testing.T) {
	src := derivedStatePlayerPreamble + `def reset_player(p: mutable Player&, val: i64):
	p.health <- val
	expect_alive(p)
`
	result := analyzeDerivedStatePrecision(t, "ds_reset_player.elisa", src)
	if !strings.Contains(allDiagnostics(result), "Player[Alive | Dead]") {
		t.Fatalf("assigning an unconstrained value must widen state to Alive | Dead; got: %s", allDiagnostics(result))
	}
}
