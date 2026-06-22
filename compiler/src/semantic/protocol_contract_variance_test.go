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

// A1 — protocol-contract COMPOSITION: a generic caller may ASSUME the protocol method's `ensure`
// after the call (the covariant postcondition half of behavioral subtyping). The injected fact (with
// `result` -> the binding the call value is assigned to) lets a downstream claim implied by the
// protocol ensure discharge.
func TestProtocolEnsureInjectedDownstreamProven(t *testing.T) {
	src := `
struct Path:
    n: usize

protocol Reader:
    def read(self: Self, p: Path) -> dstr
        ensure result.count >= 1

def load[R: Reader](r: R, p: Path) -> dstr:
    ensure result.count >= 1
    text = r.read(p)
    return text
`
	r := analyzeProtocolVariance(t, "proto_ensure_inject_ok.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("downstream `result.count >= 1` should be proven from the injected protocol ensure, got:\n%s", allDiagnostics(r))
	}
}

// A1 — NO over-assumption: a downstream claim NOT implied by the protocol's `ensure`
// (`result.count >= 5` when the protocol only guarantees `>= 0`) is still rejected under -strict.
func TestProtocolEnsureInjectedDoesNotOverAssume(t *testing.T) {
	src := `
struct Path:
    n: usize

protocol Reader:
    def read(self: Self, p: Path) -> dstr
        ensure result.count >= 0

def load[R: Reader](r: R, p: Path) -> dstr:
    ensure result.count >= 5
    text = r.read(p)
    return text
`
	r := analyzeProtocolVariance(t, "proto_ensure_inject_overassume.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("a claim stronger than the protocol's `ensure` must NOT be assumed; expected a -strict error, got none:\n%s", allDiagnostics(r))
	}
}

// A1 — SOUNDNESS: the injected fact is the PROTOCOL's `ensure`, never any concrete impl's stronger
// one. An impl promising MORE (`ensure result.count >= 3`) must not leak that extra guarantee to a
// generic caller bounded only on the protocol — so a downstream `result.count >= 3` is still rejected.
func TestProtocolEnsureInjectionUsesProtocolNotImpl(t *testing.T) {
	src := `
struct Path:
    n: usize

struct StrongReader:
    k: usize

protocol Reader:
    def read(self: Self, p: Path) -> dstr
        ensure result.count >= 0

impl Reader for StrongReader:
    def read(self: StrongReader, p: Path) -> dstr:
        ensure result.count >= 3
        return "abc"

def load[R: Reader](r: R, p: Path) -> dstr:
    ensure result.count >= 3
    text = r.read(p)
    return text
`
	r := analyzeProtocolVariance(t, "proto_ensure_inject_impl_leak.elisa", src)
	if len(r.Errors()) == 0 {
		t.Fatalf("an impl's stronger ensure must not leak to generic callers; expected a -strict error on `result.count >= 3`, got none:\n%s", allDiagnostics(r))
	}
}
