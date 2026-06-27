//go:build cgo

package semantic

// struct_field_observability_test.go
//
// Tests that ProofReport entries are recorded with the struct name included in the
// Subject field when a struct field where-refinement is discharged at a construction site.
//
// The subject format is: "where refinement on field <fieldName> of <StructName>"
// which is a superset of the legacy "where refinement on field <fieldName>" substring
// so existing observability tests continue to pass.
//
// Run with:
//   go test ./src/semantic -run 'StructFieldObservability' -tags cgo -timeout 120s

import (
	"strings"
	"testing"
)

// TestStructFieldObservability_Runtime verifies that constructing a struct with an
// unprovable field-where value (a runtime parameter) records a ProofRuntime entry
// with a Subject that identifies both the struct name and the field name.
func TestStructFieldObservability_Runtime(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make(n: i64) -> Box:
    return Box{v: n}
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "struct_field_obs_runtime.elisa", src,
		AnalyzeOptions{},
	)
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "where refinement on field") &&
			strings.Contains(f.Subject, `"v"`) &&
			strings.Contains(f.Subject, "Box") &&
			f.Predicate == "where" &&
			f.Outcome == ProofRuntime {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ProofRuntime entry with struct+field in Subject for Box.v construction; got %+v", result.ProofReport)
	}
}

// TestStructFieldObservability_Refuted verifies that constructing a struct with a
// constant field-where value that violates the predicate records a ProofRefuted entry
// with the struct name in the Subject.
func TestStructFieldObservability_Refuted(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make() -> Box:
    return Box{v: -7}
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "struct_field_obs_refuted.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "where refinement on field") &&
			strings.Contains(f.Subject, `"v"`) &&
			strings.Contains(f.Subject, "Box") &&
			f.Predicate == "where" &&
			f.Outcome == ProofRefuted {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a ProofRefuted entry with struct+field in Subject for Box(v: -7); got %+v", result.ProofReport)
	}
}

// TestStructFieldObservability_Proven verifies that constructing a struct with a
// constant field-where value that satisfies the predicate records a proven ProofReport
// entry with the struct name in the Subject.
func TestStructFieldObservability_Proven(t *testing.T) {
	src := `
struct Box:
    v: i64 where v > 0

def make() -> Box:
    return Box{v: 42}
`
	result := analyzeContractStrict(t, "struct_field_obs_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected clean analysis, got: %v", errs)
	}
	found := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "where refinement on field") &&
			strings.Contains(f.Subject, `"v"`) &&
			strings.Contains(f.Subject, "Box") &&
			f.Predicate == "where" &&
			(f.Outcome == ProofProvenLinear || f.Outcome == ProofProvenSMT || f.Outcome == ProofProvenConst) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a proven ProofReport entry with struct+field in Subject for Box(v: 42); got %+v", result.ProofReport)
	}
}

// TestStructFieldObservability_SubjectContainsStructName verifies that the Subject
// in the recorded ProofFact includes the struct name (not just the bare field name).
// This guards the observability requirement: "Box.\"v\" of Box" should appear so
// tooling can filter by struct.
func TestStructFieldObservability_SubjectContainsStructName(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo

def make(a: i64, b: i64) -> Range:
    return Range{lo: a, hi: b}
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "struct_field_obs_structname.elisa", src,
		AnalyzeOptions{},
	)
	foundStructName := false
	for _, f := range result.ProofReport {
		if strings.Contains(f.Subject, "where refinement on field") &&
			strings.Contains(f.Subject, "Range") {
			foundStructName = true
			break
		}
	}
	if !foundStructName {
		t.Errorf("expected at least one ProofReport entry with 'Range' in Subject; got %+v", result.ProofReport)
	}
}
