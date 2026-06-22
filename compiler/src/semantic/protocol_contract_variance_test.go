package semantic

import "testing"

// P1 — behavioral subtyping (Liskov–Wing) on protocol conformance.
//
// A protocol method may carry `requires`/`ensure` contracts. Each impl method must refine them with
// the correct variance: preconditions contravariant (impl may require LESS), postconditions
// covariant (impl may promise MORE). Generic code calling through a bounded type param must prove
// the protocol method's precondition.

func analyzeProtocolVariance(t *testing.T, name, src string) *Result {
	t.Helper()
	return analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
}

// Conforming impl: same precondition, same postcondition — accepted.
func TestProtocolContractConformingImplOK(t *testing.T) {
	src := `
struct Ring:
    n: usize

protocol Indexable:
    type Elem
    def count(self: Self) -> usize
    def get(self: Self, i: usize) -> Elem
        requires i < self.count()

impl Indexable for Ring:
    type Elem = u8

    def count(self: Ring) -> usize:
        return self.n

    def get(self: Ring, i: usize) -> u8:
        requires i < self.count()
        return 0
`
	r := analyzeProtocolVariance(t, "proto_conforming.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("conforming impl (same precondition) should be accepted, got:\n%s", allDiagnostics(r))
	}
}

// Strengthened precondition — REJECTED (contravariance violation).
func TestProtocolContractStrengthenedPreconditionRejected(t *testing.T) {
	src := `
struct EvenOnly:
    n: usize

protocol Indexable:
    type Elem
    def count(self: Self) -> usize
    def get(self: Self, i: usize) -> Elem
        requires i < self.count()

impl Indexable for EvenOnly:
    type Elem = u8

    def count(self: EvenOnly) -> usize:
        return self.n

    def get(self: EvenOnly, i: usize) -> u8:
        requires i < self.count()
        requires i % 2 == 0
        return 0
`
	r := analyzeProtocolVariance(t, "proto_strengthened.elisa", src)
	if !contains(allDiagnostics(r), "strengthens the precondition") {
		t.Fatalf("strengthened precondition must be rejected, got:\n%s", allDiagnostics(r))
	}
}

// Weakened (missing) postcondition — REJECTED (covariance violation).
func TestProtocolContractWeakenedPostconditionRejected(t *testing.T) {
	src := `
struct Ring:
    n: usize

protocol Sized:
    def size(self: Self) -> usize
        ensure result >= 1

impl Sized for Ring:
    def size(self: Ring) -> usize:
        return self.n
`
	r := analyzeProtocolVariance(t, "proto_weakened.elisa", src)
	if !contains(allDiagnostics(r), "weakens the postcondition") {
		t.Fatalf("missing/weakened postcondition must be rejected, got:\n%s", allDiagnostics(r))
	}
}

// Strengthened postcondition (impl promises MORE) — ACCEPTED (covariance OK).
func TestProtocolContractStrengthenedPostconditionOK(t *testing.T) {
	src := `
struct Ring:
    n: usize

protocol Sized:
    def size(self: Self) -> usize
        ensure result >= 1

impl Sized for Ring:
    def size(self: Ring) -> usize:
        ensure result >= 1
        ensure result >= 0
        return 1
`
	r := analyzeProtocolVariance(t, "proto_stronger_post.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("impl promising MORE (covariant) should be accepted, got:\n%s", allDiagnostics(r))
	}
}

// Generic caller PROVES the protocol precondition from its own `requires`.
func TestProtocolContractGenericCallerProves(t *testing.T) {
	src := `
protocol Indexable:
    type Elem
    def count(self: Self) -> usize
    def get(self: Self, i: usize) -> Elem
        requires i < self.count()

def second[C: Indexable](c: C) -> C.Elem:
    requires c.count() >= 2
    return c.get(1)
`
	r := analyzeProtocolVariance(t, "proto_generic_proves.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("generic caller should discharge `1 < c.count()` from `c.count() >= 2`, got:\n%s", allDiagnostics(r))
	}
}

// Generic caller that does NOT establish the precondition — REJECTED / flagged.
func TestProtocolContractGenericCallerUnprovenRejected(t *testing.T) {
	src := `
protocol Indexable:
    type Elem
    def count(self: Self) -> usize
    def get(self: Self, i: usize) -> Elem
        requires i < self.count()

def reckless[C: Indexable](c: C) -> C.Elem:
    return c.get(1)
`
	r := analyzeProtocolVariance(t, "proto_generic_unproven.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("generic call without establishing the protocol precondition must be flagged under -strict, got no diagnostics")
	}
}
