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

// dictSurfaceStubs declares the stdlib free functions the SURFACE dict methods resolve to, so the
// dict membership/lookup wiring is exercised end-to-end from source spelling (`d.contains(k)` /
// `d.get(k)`) inside the test package (which has no stdlib prelude).
const dictSurfaceStubs = `
def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> T&?:
    return null

def arena_dict_contains[K, T](m: dict[K, T]&, key: K) -> bool:
    return arena_dict_get(m, key) != null
`

// DICT MEMBERSHIP + POSITIVE-VALUE LOOKUP, REACHABLE FROM SOURCE. The surface language accesses
// dicts via methods — `d.contains(k)` (membership) and `d.get(k)` (optional lookup) — which resolve
// to `arena_dict_contains` / `arena_dict_get`. The SMT lowering maps them onto the dict model:
// `d.contains(k)` -> `(select d_keys k)` and `d.get(k)` -> `(select d_vals k)`. So under a
// `forall (key,v) in d: v > 0` hypothesis and a `requires d.contains(k)` membership guard, the goal
// `d.get(k) > 0` discharges by instantiating the quantifier at the witnessed key. This is the
// headline dict-modeling win made reachable from real Elisa syntax.
func TestSMTProvesDictMembershipLookup(t *testing.T) {
	src := dictSurfaceStubs + `
def vals_pos(d: dict[i64, i64], k: i64) -> void:
    requires forall (key, v) in d: v > 0
    requires d.contains(k)
    assert d.get(k) > 0 by:
        pass
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_dict_lookup.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("dict membership + positive-value lookup must discharge under SMT, got:\n%s", strings.Join(errs, "\n"))
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Subject == "assert by" {
			smtProven++
		}
	}
	if smtProven == 0 {
		t.Fatalf("expected the dict lookup assert to be SMT-proven, report: %+v", result.ProofReport)
	}
}

// SOUNDNESS: WITHOUT the `requires d.contains(k)` membership guard, `k` is not constrained to be a
// key of `d`, so `d.get(k)` selects an arbitrary-but-total value the `forall` hypothesis does not
// range over. The goal must NOT be SMT-proven (z3 finds a non-member k with a non-positive value).
func TestSMTDeclinesDictLookupWithoutMembership(t *testing.T) {
	src := dictSurfaceStubs + `
def vals_pos(d: dict[i64, i64], k: i64) -> void:
    requires forall (key, v) in d: v > 0
    assert d.get(k) > 0 by:
        pass
`
	result := analyzeWithSMT(t, "smt_dict_noguard.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Subject == "assert by" {
			t.Fatalf("dict lookup without a membership guard must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// SOUNDNESS: a FALSE claim about dict values must NOT be proven. `forall (key,v) in d: v > 0` and
// `d.contains(k)` establish `d.get(k) > 0`, but NOT `d.get(k) > 1` (a member value could be exactly
// 1). z3 finds the witness, so the assert stays a runtime check.
func TestSMTDeclinesFalseDictClaim(t *testing.T) {
	src := dictSurfaceStubs + `
def vals_pos(d: dict[i64, i64], k: i64) -> void:
    requires forall (key, v) in d: v > 0
    requires d.contains(k)
    assert d.get(k) > 1 by:
        pass
`
	result := analyzeWithSMT(t, "smt_dict_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Subject == "assert by" {
			t.Fatalf("false dict-value claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// NON-INT VALUE DICT DECLINES (soundness/scope): a dict with a float VALUE is not modelable — the
// value array is fixed to (Array <KSort> Int). The `d.get(k)` lookup term declines, so a value goal
// cannot be SMT-proven; it is left to the runtime check, never fabricated. (Here z3 also genuinely
// could not establish `d.get(k) > 0.0` even were it modeled — the point is the model declines.)
func TestSMTDeclinesFloatValueDict(t *testing.T) {
	src := dictSurfaceStubs + `
def vals_pos(d: dict[i64, f64], k: i64) -> void:
    requires forall (key, v) in d: v > 0.0
    requires d.contains(k)
    assert d.get(k) > 0.0 by:
        pass
`
	result := analyzeWithSMT(t, "smt_dict_floatval.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Subject == "assert by" {
			t.Fatalf("a float-value dict must not be SMT-modeled: %+v", result.ProofReport)
		}
	}
}

// DICT QUANTIFIER over a key/value binder lowers cleanly: `forall (key,v) in d: v > 0` translates
// through the dict-source quantifier path (binds the value to `(select d_vals key)` guarded by
// `(select d_keys key)`).
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
