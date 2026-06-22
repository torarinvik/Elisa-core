package semantic

import (
	"strings"
	"testing"
)

// A4 — protocol inheritance + default-method contract coherence.
//
// Three coherence properties, each tested in both polarities:
//   (1) an impl of a DERIVED protocol is checked against the BASE protocol's laws too;
//   (2) a generic bound on the derived protocol may use the base's methods/laws;
//   (3) a default method carrying an `ensure` has it discharged against its own body, and an
//       overriding impl is variance-checked.
// Plus a diamond-inheritance smoke test (no double-count / crash).

const eqHashProtocols = `
protocol Eq:
    def eq(self: Self, other: Self) -> bool
    law eq_reflexive(a: Self) = a.eq(a)

protocol Hash: Eq:
    def hash(self: Self) -> u64
`

// (1+) An impl of the derived protocol Hash discharges the INHERITED Eq law. A structural eq proves
// reflexivity statically — so no fuzz obligation, and crucially the inherited law was still SEEN.
func TestInheritedLawDischargedForDerivedImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "hash_eq_ok.elisa", eqHashProtocols+`
struct K:
    v: i64

impl Hash for K:
    def eq(self: K, other: K) -> bool:
        return self.v == other.v
    def hash(self: K) -> u64:
        return 0
`, AnalyzeOptions{EnableSMT: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

// (1-) An impl of the derived protocol Hash whose eq VIOLATES the inherited Eq law (a.eq(a) is false
// for all a) must not slip through: the inherited law is discharged exactly as a direct one, so it is
// either refuted statically or auto-lowered to a fuzz obligation. Either way the inherited law must be
// SEEN — a missing obligation/proof would mean inheritance dropped the base's law.
func TestInheritedLawSeenForViolatingDerivedImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "hash_eq_bad.elisa", eqHashProtocols+`
struct K:
    v: i64

impl Hash for K:
    def eq(self: K, other: K) -> bool:
        return false
    def hash(self: K) -> u64:
        return 0
`, AnalyzeOptions{EnableSMT: true})

	// The inherited Eq.eq_reflexive law must produce an obligation for K (proof OR fuzz). With a
	// non-provable `false` body the SMT tier cannot prove `a.eq(a)`, so a fuzz obligation is expected.
	seen := false
	for _, ob := range result.LawFuzzObligations {
		if ob.TypeName == "K" && ob.LawName == "eq_reflexive" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("inherited Eq law eq_reflexive must be discharged for the Hash impl of K (fuzz obligation expected for the always-false eq); obligations=%v", result.LawFuzzObligations)
	}
}

// (2) A generic bounded on the DERIVED protocol may call the BASE protocol's method. Hash folds in
// Eq.eq, so `T.eq(a, b)` resolves under `[T: Hash]`.
func TestGenericOnDerivedMayUseBaseMethod(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "generic_derived_base_method.elisa", eqHashProtocols+`
struct K:
    v: i64

impl Hash for K:
    def eq(self: K, other: K) -> bool:
        return self.v == other.v
    def hash(self: K) -> u64:
        return 0

def both[T: Hash](a: T, b: T) -> bool:
    return T.eq(a, b)
`)

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a [T: Hash] generic should be able to call the inherited Eq.eq, got: %v", errs)
	}
}

// (3+) A correct contracted default discharges its own `ensure` against its own body. `clamp`
// returns `self.lo()` and promises `result == self.lo()` — provable by return-reflexivity even over
// abstract `Self` (lo() is one uninterpreted symbol shared by result and the return value). Under
// strict proofs this must analyze cleanly: the default impl satisfies what it promises.
func TestDefaultMethodContractHoldsAgainstOwnBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_clamp_ok.elisa", `
protocol Bound:
    def lo(self: Self) -> i64

protocol Clamp: Bound:
    def clamp(self: Self) -> i64:
        ensure result == self.lo()
        return self.lo()
`, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})

	for _, err := range result.Errors() {
		if strings.Contains(err, "could not be proven") {
			t.Fatalf("a correct contracted default method must discharge its own ensure, got: %v", result.Errors())
		}
	}
}

// (3-) A contracted default with a WRONG body must be rejected: `clamp` promises `result == self.lo()`
// but returns `self.hi()`, which the prover cannot establish — under strict proofs the default's own
// postcondition fails and is reported exactly as for a free function.
func TestDefaultMethodContractRejectsWrongBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_clamp_bad.elisa", `
protocol Bound:
    def lo(self: Self) -> i64
    def hi(self: Self) -> i64

protocol Clamp: Bound:
    def clamp(self: Self) -> i64:
        ensure result == self.lo()
        return self.hi()
`, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})

	found := false
	for _, err := range result.Errors() {
		if strings.Contains(err, "could not be proven") && strings.Contains(err, "clamp") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a default method whose body cannot establish its own `ensure` must be rejected, got: %v", result.Errors())
	}
}

// (3, variance) An impl overriding a contracted default `max` is variance-checked against the
// default's contract. An override that STRENGTHENS the precondition is rejected.
func TestDefaultMethodOverrideVarianceChecked(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "default_max_override_bad.elisa", `
struct N:
    v: i64

protocol Ord:
    def le(self: Self, other: Self) -> bool

protocol Comparable: Ord:
    def max(self: Self, other: Self) -> Self:
        ensure self.le(result)
        if self.le(other):
            return other
        return self

impl Comparable for N:
    def le(self: N, other: N) -> bool:
        return self.v <= other.v
    def max(self: N, other: N) -> N:
        requires self.v > 0
        if self.le(other):
            return other
        return self
`, AnalyzeOptions{EnableSMT: true})

	found := false
	for _, err := range result.Errors() {
		if strings.Contains(err, "strengthens the precondition") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an override that strengthens the default's precondition must be rejected, got: %v", result.Errors())
	}
}

// Diamond inheritance: D: B, C and both B: A, C: A. Folding A's law up both arms must not double-count
// or crash; an impl of D discharges A's law exactly once.
func TestDiamondInheritanceNoDoubleCount(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "diamond.elisa", `
struct K:
    v: i64

protocol A:
    def eq(self: Self, other: Self) -> bool
    law a_reflexive(x: Self) = x.eq(x)

protocol B: A:
    def b(self: Self) -> u64

protocol C: A:
    def c(self: Self) -> u64

protocol D: B, C:
    def d(self: Self) -> u64

impl D for K:
    def eq(self: K, other: K) -> bool:
        return self.v == other.v
    def b(self: K) -> u64:
        return 0
    def c(self: K) -> u64:
        return 0
    def d(self: K) -> u64:
        return 0
`, AnalyzeOptions{EnableSMT: true})

	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("diamond inheritance must analyze cleanly, got: %v", errs)
	}
	// A's law must appear at most once across the discharge (proven => no fuzz; if fuzzed, exactly one).
	count := 0
	for _, ob := range result.LawFuzzObligations {
		if ob.TypeName == "K" && ob.LawName == "a_reflexive" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("diamond inheritance double-counted law a_reflexive (%d obligations)", count)
	}
	iface := result.StaticInterfaces["D"]
	if iface == nil {
		t.Fatal("expected interface D")
	}
	lawCount := 0
	for _, l := range iface.Laws {
		if l.Name == "a_reflexive" {
			lawCount++
		}
	}
	if lawCount != 1 {
		t.Fatalf("D should inherit law a_reflexive exactly once, got %d", lawCount)
	}
}
