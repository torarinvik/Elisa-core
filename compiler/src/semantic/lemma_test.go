//go:build cgo

package semantic

import "testing"

// A valid lemma: its `ensure` follows from its body, so it verifies (recorded as a proven "lemma"
// obligation), and its statement-position call is registered for codegen erasure. (Contracts are
// leading body statements, not signature clauses — the region-prefix grammar collision.)
func TestLemmaVerifiesAndErasesCall(t *testing.T) {
	src := `
lemma nonneg_square(x: i64):
    ensure x * x >= 0
    pass

def use(n: i64):
    nonneg_square(n)
`
	result := analyzeWithSMT(t, "lemma_valid.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis of a valid lemma, got: %v", errs)
	}
	if got := countRefinementProof(result, "ensure", ProofProvenSMT); got < 1 {
		t.Fatalf("expected the lemma's `ensure` to be proven at its definition, report: %+v", result.ProofReport)
	}
	if len(result.LemmaCalls) != 1 {
		t.Fatalf("expected exactly one lemma call registered for codegen erasure, got %d", len(result.LemmaCalls))
	}
}

// Soundness: a lemma whose `ensure` does NOT follow from its `requires`/body is a HARD ERROR. A lemma
// is erased from code (no runtime check), so an unproven postcondition cannot be assumed at call
// sites — that would be an axiom backdoor. `x >= 0` does not entail `x >= 100`.
func TestLemmaUnprovenEnsureIsError(t *testing.T) {
	src := `
lemma bogus(x: i64):
    requires x >= 0
    ensure x >= 100
    pass
`
	result := analyzeWithSMT(t, "lemma_bogus.elisa", src)
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatalf("expected an error: an unprovable lemma `ensure` must be rejected (no runtime fallback exists)")
	}
}

// Soundness: a self-recursive lemma must NOT be able to assume its own conclusion to prove itself.
// The circular-reasoning guard withholds the ensure inside the lemma's own body, so the bogus
// `ensure x != x` is left unproven → error (rather than a vacuous self-justifying "proof").
func TestRecursiveLemmaCannotAssumeItself(t *testing.T) {
	src := `
lemma circular(x: i64):
    ensure x != x
    circular(x)
`
	result := analyzeWithSMT(t, "lemma_circular.elisa", src)
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatalf("a self-recursive lemma must not prove its own (false) ensure by assuming it; expected an error")
	}
}

// Soundness: a lemma's `requires` must provably hold at each call site. Because the lemma is erased,
// an unmet precondition has no runtime check, so the ensure cannot be assumed — calling it without
// establishing its `requires` is a hard error (the driver gives no bound on `x`).
func TestLemmaUnmetRequiresAtCallIsError(t *testing.T) {
	src := `
lemma needs_bound(a: i64):
    requires a >= 0
    ensure a + 1 >= 1
    pass

def driver(x: i64):
    needs_bound(x)
`
	result := analyzeWithSMT(t, "lemma_unmet_req.elisa", src)
	if errs := result.Errors(); len(errs) == 0 {
		t.Fatalf("calling a lemma whose `requires` is not established must be an error (no runtime fallback)")
	}
}

// The injected fact is usable downstream: after the lemma call, its (proven) `ensure`, with the
// argument substituted, is available to discharge a later obligation — here a callee `requires`.
func TestLemmaInjectsFactForDownstreamRequires(t *testing.T) {
	src := `
lemma bounded_product(a: i64, b: i64):
    requires a >= 0
    requires a <= 10
    requires b >= 0
    requires b <= 10
    ensure a * b <= 100
    pass

def sink(p: i64):
    requires p <= 100
    pass

def driver(x: i64, y: i64):
    requires x >= 0
    requires x <= 10
    requires y >= 0
    requires y <= 10
    bounded_product(x, y)
    sink(x * y)
`
	result := analyzeWithSMT(t, "lemma_inject.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis with the lemma supplying the bound, got: %v", errs)
	}
	if len(result.LemmaCalls) != 1 {
		t.Fatalf("expected the bounded_product call registered for erasure, got %d", len(result.LemmaCalls))
	}
}
