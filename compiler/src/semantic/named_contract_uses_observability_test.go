//go:build cgo

package semantic

import "testing"

// expandOneUse (analyzer_named_contracts.go) records an observability entry
// "uses <ContractName>" / "uses" / ProofProvenContract at each successful `uses`
// application, so the contract NAME is visible in the proof report (the folded
// requires/ensure obligations themselves discharge under generic subjects).
func TestNamedContractUsesRecordsContractName(t *testing.T) {
	src := `
contract CheckPositive(x: i64):
    requires x > 0

def need(val: i64) -> i64:
    uses CheckPositive(val)
    return val

def caller() -> i64:
    return need(5)
`
	result := analyzeContractStrict(t, "uses_observability.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "uses CheckPositive", "uses", ProofProvenContract) {
		t.Errorf("expected a ProofReport entry naming the applied contract `uses CheckPositive`; got %+v", result.ProofReport)
	}
}

// A second contract applied on the same function records its own named entry,
// so multiple `uses` clauses are each individually attributable.
func TestNamedContractMultipleUsesEachRecorded(t *testing.T) {
	src := `
contract CheckPositive(x: i64):
    requires x > 0

contract CheckBounded(x: i64):
    requires x < 100

def need(val: i64) -> i64:
    uses CheckPositive(val)
    uses CheckBounded(val)
    return val

def caller() -> i64:
    return need(5)
`
	result := analyzeContractStrict(t, "uses_observability_multi.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	if !proofReportContains(result.ProofReport, "uses CheckPositive", "uses", ProofProvenContract) {
		t.Errorf("expected entry for `uses CheckPositive`; got %+v", result.ProofReport)
	}
	if !proofReportContains(result.ProofReport, "uses CheckBounded", "uses", ProofProvenContract) {
		t.Errorf("expected entry for `uses CheckBounded`; got %+v", result.ProofReport)
	}
}
