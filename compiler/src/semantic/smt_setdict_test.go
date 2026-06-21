//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// SET MEMBERSHIP: a `forall x in s: x != 0` precondition plus `p in s` discharges `assert p != 0`
// by quantifier instantiation at the membership witness. This is the headline set-modeling win.
func TestSMTProvesSetMembership(t *testing.T) {
	src := `
law NoZeros(self: set[i64]) = forall x in self: x != 0

def probe(s: set[i64], p: i64) -> void:
    requires forall x in s: x != 0
    requires p in s
    assert p != 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_set_member.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("set membership precondition must discharge the assert, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Soundness: WITHOUT `p in s`, nothing constrains `p` to a member, so `assert p != 0` must NOT be
// SMT-proven (z3 finds p outside s with p == 0).
func TestSMTDeclinesSetMembershipWithoutGuard(t *testing.T) {
	src := `
def probe(s: set[i64], p: i64) -> void:
    requires forall x in s: x != 0
    assert p != 0
`
	result := analyzeWithSMT(t, "smt_set_member_noguard.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("assert p != 0 must not be SMT-proven without a membership guard: %+v", result.ProofReport)
		}
	}
}

// DICT QUANTIFIER over a key/value binder lowers cleanly: `forall (key,v) in d: v > 0` translates
// through the dict-source quantifier path (binds the value to `(select d_vals key)` guarded by
// `(select d_keys key)`). The surface language expresses dict lookup/membership via `d.get(k)` /
// `d.contains(k)` methods (not `d[k]` / `k in d`), so the assert-discharge form is not yet reachable
// from source — but the law body must still lower without error or false proof.
//
// NOTE: the dict term-modeling (dictTermEnv, `d[k]` -> (select d_vals k), `k in d` -> (select
// d_keys k)) is sound and present; wiring the rewritten `arena_dict_get`/`arena_dict_contains`
// calls (and the `T?` optional result of `.get`) to it is a follow-up.
func TestSMTDictQuantifierLawLowersCleanly(t *testing.T) {
	src := `
law ValsPos(self: dict[i64, i64]) = forall (key, v) in self: v > 0

def keep(d: dict[i64, i64]) -> void:
    requires forall (key, v) in d: v > 0
    return
`
	result := analyzeWithSMT(t, "smt_dict_quant.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("dict key/value quantifier law must analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Soundness: a FALSE claim about set members must NOT be proven. `forall x in s: x != 0` and
// `p in s` do NOT establish `p > 0` (a member could be negative).
func TestSMTDeclinesFalseSetClaim(t *testing.T) {
	src := `
def probe(s: set[i64], p: i64) -> void:
    requires forall x in s: x != 0
    requires p in s
    assert p > 0
`
	result := analyzeWithSMT(t, "smt_set_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("false set claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// BOOL ELEMENT SET: a set keyed by bool is soundly modelable (SMT sort Bool). The `forall x in s`
// quantifier and `p in s` membership lower over a (Array Bool Bool) without error.
func TestSMTSetBoolElementLowersCleanly(t *testing.T) {
	src := `
def keep(s: set[bool]) -> bool:
    requires forall x in s: x
    return true
`
	result := analyzeWithSMT(t, "smt_set_bool.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("bool-element set quantifier must analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// EXISTS over a set: `exists x in s: x > 0` is a satisfiable existential the solver should not be able
// to disprove. Here we assert it as a non-falsifiable obligation is NOT claimed — instead, prove a
// universally-quantified consequence: if every member is positive (`forall`) and the set is used, the
// membership-guarded element is positive. (Smoke test that the exists path lowers without error.)
func TestSMTSetForallExistsLowerCleanly(t *testing.T) {
	src := `
law SomePositive(self: set[i64]) = exists x in self: x > 0
law AllPositive(self: set[i64]) = forall x in self: x > 0

def keep(s: set[i64]) -> void:
    requires forall x in s: x > 0
    return
`
	result := analyzeWithSMT(t, "smt_set_exists.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("set forall/exists laws must analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// NON-INT ELEMENT TYPE DECLINES (soundness/scope): a set of floats is not modelable — the membership
// term declines, so the assert is left to the runtime check, never fabricated. The analysis stays
// clean (no false proof, no crash).
func TestSMTDeclinesFloatSetElement(t *testing.T) {
	src := `
def probe(s: set[f64], p: f64) -> void:
    requires p in s
    assert p in s
`
	result := analyzeWithSMT(t, "smt_set_float.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("a float-element set must not be SMT-modeled: %+v", result.ProofReport)
		}
	}
}
