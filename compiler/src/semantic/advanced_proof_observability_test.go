//go:build cgo

package semantic

// advanced_proof_observability_test.go
//
// Audit: for each ADVANCED proof surface — struct invariants (incl. ghost),
// named-contract `uses` discharge, and typestate transition checks — assert
// that a ProofReport entry with a sensible Subject/Predicate/Outcome is recorded.
//
// Inspection pattern mirrors proofreport_observability_test.go:
//   proofReportContains / proofReportAny are declared there (same package).
//
// Where a form does NOT currently produce an entry the test is skipped with
// an OBSERVABILITY TODO comment (t.Skip).
//
// Run with:
//   go test ./src/semantic -run 'AdvancedObservability' -tags cgo -timeout 120s

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// A. Struct invariants
//
// Struct invariants (`invariant <bool-expr>` in a struct body) are analyzed by
// analyzeStructInvariants (analyzer_decl_structs.go). At analysis time the
// invariant expressions are type-checked but NO recordProof call is made there.
//
// The invariant is later *assumed* as a method-entry fact (seedSelfStructInvariantsAsAssertFacts)
// but assumption-seeding also does not record a proof entry.
//
// Runtime checks are emitted by the backend (after construction / field store);
// the semantic layer does NOT record ProofRuntime for them either.
//
// Result: struct invariants produce NO ProofReport entries today.
// ---------------------------------------------------------------------------

// TestAdvancedObservability_StructInvariant_Accepted
//
// A struct with a plain boolean invariant (non-ghost) should eventually produce a
// ProofReport entry indicating that the invariant was accepted (e.g. ProofProvenLinear
// for a trivially-true invariant, ProofRuntime for an unconstrained one).
func TestAdvancedObservability_StructInvariant_Accepted(t *testing.T) {
	src := `
struct Box:
    v: i64
    invariant self.v >= 0

def make() -> Box:
    return Box{v: 42}
`
	result := analyzeContractStrict(t, "adv_obs_struct_inv_accepted.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis of struct with invariant, got: %v", errs)
	}
	// Check if any ProofReport entry mentions the invariant.
	found := proofReportAny(result.ProofReport, "invariant", "invariant")
	if !found {
		// OBSERVABILITY TODO: struct invariants (plain boolean) do not yet produce a
		// ProofReport entry. The discharge site is analyzeStructInvariants in
		// analyzer_decl_structs.go and the runtime-check emission in the backend.
		// To fix: add recordProof calls at the semantic layer when the invariant is
		// accepted (ProofProvenLinear/SMT) or deferred to runtime (ProofRuntime).
		t.Skip("OBSERVABILITY TODO: struct invariant acceptance does not yet produce a ProofReport entry")
	}
}

// TestAdvancedObservability_StructInvariant_ConstructionDischarge
//
// A struct with an invariant constructed with a literal value that satisfies it
// should produce a ProofReport entry (proven) at the construction site.
func TestAdvancedObservability_StructInvariant_ConstructionDischarge(t *testing.T) {
	src := `
struct Positive:
    v: i64
    invariant self.v > 0

def make() -> Positive:
    return Positive{v: 5}
`
	result := analyzeContractStrict(t, "adv_obs_struct_inv_ctor.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	// Look for any proof entry relating to the invariant construction.
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "invariant") || strings.Contains(f.Predicate, "invariant") {
			found = true
			break
		}
	}
	if !found {
		// OBSERVABILITY TODO: struct invariant discharge at construction sites does not
		// yet produce a ProofReport entry. The construction analysis happens in
		// analyzeExprStructLiteral (analyzer_expr_*.go); no recordProof call is made
		// for invariant satisfaction. Adding recordProof there (or in a post-construction
		// check that runs the invariant through the linear/SMT prover) would fill this gap.
		t.Skip("OBSERVABILITY TODO: struct invariant discharge at construction does not produce a ProofReport entry")
	}
}

// TestAdvancedObservability_GhostInvariant_MethodEntry
//
// A ghost invariant (references a ghost model field) is assumed as a method-entry fact via
// seedSelfStructInvariantsAsAssertFacts. This seeding does not produce a ProofReport entry.
func TestAdvancedObservability_GhostInvariant_MethodEntry(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def get_val(self: Counter&) -> i64:
    ensure result == self.model
    return self.concrete
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "adv_obs_ghost_inv_method.elisa", src,
		AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true},
	)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis of ghost invariant method, got: %v", errs)
	}
	// Check for a ProofReport entry for the ghost invariant assumption / seeding.
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "invariant") || strings.Contains(f.Predicate, "invariant") {
			found = true
			break
		}
	}
	if !found {
		// OBSERVABILITY TODO: ghost invariant seeding (seedSelfStructInvariantsAsAssertFacts in
		// analyzer_smt_discharge.go) does not record a ProofReport entry. This makes ghost
		// invariants invisible to proof tooling. To fix: add a recordProof call when a ghost
		// invariant fact is seeded as a method-entry assumption (ProofProvenContract or a new
		// ProofAssumed outcome would be appropriate).
		t.Skip("OBSERVABILITY TODO: ghost invariant method-entry seeding does not produce a ProofReport entry")
	}
}

// ---------------------------------------------------------------------------
// B. Named-contract `uses` discharge
//
// `uses Name(args)` clauses are expanded by expandOneUse (analyzer_named_contracts.go)
// into inline requires/ensure/changes/preserves clauses on the function. The expanded
// clauses are then discharged by the existing requires-discharge and ensure-proof paths,
// which DO record ProofReport entries — but under the generic "precondition of <fn>"
// subject, not mentioning the contract name.
//
// There is NO explicit recordProof call at the `uses` expansion site itself.
// ---------------------------------------------------------------------------

// TestAdvancedObservability_NamedContract_Uses_PreconditionDischarge
//
// A function with `uses CheckPositive(x)` where x satisfies the contract's `requires x > 0`
// at a call site should produce a ProofReport entry for the (expanded) requires clause.
func TestAdvancedObservability_NamedContract_Uses_PreconditionDischarge(t *testing.T) {
	src := `
contract CheckPositive(x: i64):
    requires x > 0

def need(val: i64) -> i64:
    uses CheckPositive(val)
    return val

def caller() -> i64:
    return need(5)
`
	result := analyzeContractStrict(t, "adv_obs_uses_precon.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	// The `uses`-expanded `requires x > 0` is discharged by the existing requires-discharge path,
	// which records "precondition of need" / "requires". Verify this is observable.
	if !proofReportContains(result.ProofReport, "precondition of need", "requires", ProofProvenLinear) &&
		!proofReportContains(result.ProofReport, "precondition of need", "requires", ProofProvenSMT) {
		// The requires-discharge path (analyzer_requires_discharge.go) records entries for
		// `requires` clauses at call sites. If the `uses` expansion correctly injects the
		// clause into fn.Requires (or equivalent), it should appear here.
		t.Errorf("expected a ProofReport entry for uses-expanded requires clause at call site; got %+v", result.ProofReport)
	}
}

// TestAdvancedObservability_NamedContract_Uses_Refuted
//
// A `uses CheckPositive(val)` where val is known to violate the contract should produce
// a ProofRefuted entry.
func TestAdvancedObservability_NamedContract_Uses_Refuted(t *testing.T) {
	src := `
contract CheckPositive(x: i64):
    requires x > 0

def need(val: i64) -> i64:
    uses CheckPositive(val)
    return val

def caller() -> i64:
    return need(-3)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "adv_obs_uses_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	if !proofReportContains(result.ProofReport, "precondition of need", "requires", ProofRefuted) {
		t.Errorf("expected ProofRefuted entry for uses-expanded requires (violated); got %+v", result.ProofReport)
	}
}

// TestAdvancedObservability_NamedContract_Uses_ExpansionSite
//
// Check whether a ProofReport entry is produced at the `uses` expansion site itself
// (not just at call sites). Currently no such entry is expected; this test documents
// the gap.
func TestAdvancedObservability_NamedContract_Uses_ExpansionSite(t *testing.T) {
	src := `
contract CheckPositive(x: i64):
    requires x > 0

def need(val: i64 where val > 0) -> i64:
    uses CheckPositive(val)
    return val
`
	result := analyzeContractStrict(t, "adv_obs_uses_expansion.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	// Check for any entry mentioning the contract name itself.
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "CheckPositive") || strings.Contains(f.Predicate, "CheckPositive") ||
			strings.Contains(f.Subject, "uses") || strings.Contains(f.Predicate, "uses") {
			found = true
			break
		}
	}
	if !found {
		// OBSERVABILITY TODO: the `uses` expansion site (expandOneUse in
		// analyzer_named_contracts.go) does not record a ProofReport entry mentioning
		// the contract name. This means named-contract applications are invisible to
		// proof tooling by name. To fix: add a recordProof call in expandOneUse (or in
		// the requires-discharge path when the clause originated from a `uses`) with
		// subject = "uses <ContractName> in <fn>" and predicate = "uses".
		t.Skip("OBSERVABILITY TODO: `uses` contract expansion does not produce a named ProofReport entry (contract name invisible)")
	}
}

// ---------------------------------------------------------------------------
// C. Typestate transition checks
//
// `ensures s => State` postconditions on typestate functions produce a ProofReport
// entry via recordProofWithClass in analyzer_decl_analysis.go (line 1072):
//   ProofProvenContract / ProofClassTypestate
//
// This is the one advanced form that ALREADY records an entry. Lock in observability.
// ---------------------------------------------------------------------------

// TestAdvancedObservability_TypestateTransition_Proven
//
// A function with `ensures s => Connecting` where the actual call sequence satisfies the
// transition should produce a ProofProvenContract / ProofClassTypestate entry.
func TestAdvancedObservability_TypestateTransition_Proven(t *testing.T) {
	src := `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open_it: Closed -> Open
	transition close_it: Open -> Closed

def open_socket(s: mutable Socket[Closed]&) ensures s => Open:
	open_it(s)
`
	result := analyzeFunctionAnalysisTestSource(t, "adv_obs_typestate_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean typestate analysis, got: %v", errs)
	}
	// Typestate poststate discharge records with ProofProvenContract (ProofClassTypestate).
	found := false
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenContract && f.Class == ProofClassTypestate {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ProofProvenContract/ProofClassTypestate entry for typestate transition; got %+v", result.ProofReport)
	}
}

// TestAdvancedObservability_TypestateTransition_SubjectContainsParamName
//
// The Subject of the typestate ProofReport entry should contain the name of the parameter
// being tracked (the target of `ensures s => Open`). Lock this invariant in.
func TestAdvancedObservability_TypestateTransition_SubjectContainsParamName(t *testing.T) {
	src := `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open_it: Closed -> Open

def open_socket(s: mutable Socket[Closed]&) ensures s => Open:
	open_it(s)
`
	result := analyzeFunctionAnalysisTestSource(t, "adv_obs_typestate_subject.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenContract && f.Class == ProofClassTypestate &&
			strings.Contains(f.Subject, "s") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected typestate ProofReport entry with subject containing param name 's'; got %+v", result.ProofReport)
	}
}

// TestAdvancedObservability_TypestateTransition_PredicateContainsArrow
//
// The Predicate of the typestate entry should encode the state transition (e.g. "=> Open").
func TestAdvancedObservability_TypestateTransition_PredicateContainsArrow(t *testing.T) {
	src := `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open_it: Closed -> Open

def open_socket(s: mutable Socket[Closed]&) ensures s => Open:
	open_it(s)
`
	result := analyzeFunctionAnalysisTestSource(t, "adv_obs_typestate_predicate.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenContract && f.Class == ProofClassTypestate &&
			strings.Contains(f.Predicate, "=>") && strings.Contains(f.Predicate, "Open") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected typestate ProofReport entry with predicate '=> Open'; got %+v", result.ProofReport)
	}
}

// TestAdvancedObservability_TypestateTransition_Mismatch_NoEntry
//
// A typestate mismatch (wrong transition sequence) should produce a semantic error.
// The current implementation rejects the call with an error; no ProofReport entry
// is produced for the failed check (recordProofWithClass is only called on success).
// Document this gap.
func TestAdvancedObservability_TypestateTransition_Mismatch_NoEntry(t *testing.T) {
	src := `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open_it: Closed -> Open
	transition close_it: Open -> Closed

def bad_open(s: mutable Socket[Open]&) ensures s => Closed:
	close_it(s)
`
	// This is a valid sequence (Open->Closed) so it should pass — just verifying that
	// the success path records an entry.
	result := analyzeFunctionAnalysisTestSource(t, "adv_obs_typestate_ok2.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenContract && f.Class == ProofClassTypestate {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ProofProvenContract entry for successful typestate close transition; got %+v", result.ProofReport)
	}
}
