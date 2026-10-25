//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"
)

func TestRetainedRuntimeDefaultRemovalKeepsBodyCollectorScoped(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const prefix = `global values: i32[2] = [11, 22]
global cursor: usize = 0
def keep() -> i32:
    return 1
`
	const suffix = `def consume(value: i32 = values[cursor]) -> i32:
    return value
`
	source := prefix + suffix
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	if len(state.a.indexBoundsRuntimeSites) == 0 {
		t.Fatal("fixture has no real runtime header/default site")
	}
	if len(state.a.currentFunctionGuardedIndexes) != 0 || len(state.a.currentFunctionUsedPermissions) != 0 || len(state.a.currentFunctionUsedPermissionRefs) != 0 {
		t.Fatal("declaration/default guards escaped into body-local collector")
	}
	state = updateLiveBoundsTelemetry(t, state, prefix)
	cold := AnalyzeWithOptions(retainedParse(t, prefix), AnalyzeOptions{})
	if !reflect.DeepEqual(cold.ProofReport, state.result.ProofReport) {
		t.Fatal("accepted default removal changed cold proof report")
	}
	if len(state.a.indexBoundsRuntimeSites) != 0 || len(state.a.currentFunctionGuardedIndexes) != 0 || len(state.a.currentFunctionUsedPermissions) != 0 || len(state.a.currentFunctionUsedPermissionRefs) != 0 {
		t.Fatal("removed default left telemetry or temporary guards")
	}
}

func TestDefaultDeclarationDoesNotViolateOwnBodyBoundsContracts(t *testing.T) {
	const source = `global values: i32[2] = [11, 22]
global cursor: usize = 0
@inbounds
def consume(value: i32 = values[cursor]) -> i32 fulfills NoBoundsChecks:
    return value
`
	result := analyzeTreeTestSource(t, "default_own_body_bounds.elisa", source)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if result.ElisionSummary.ArrayBounds.Runtime == 0 {
		t.Fatal("default telemetry lost while isolating body collector")
	}
}

func TestEvaluatedCallerDefaultStillViolatesBoundsContracts(t *testing.T) {
	const source = `global values: i32[2] = [11, 22]
global cursor: usize = 0
def consume(value: i32 = values[cursor]) -> i32:
    return value
@inbounds
def caller() -> i32 fulfills NoBoundsChecks:
    return consume()
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "default_caller_bounds.elisa", source)
	diagnostics := allDiagnostics(result)
	for _, expected := range []string{"fulfills NoBoundsChecks", "@inbounds function \"caller\""} {
		if !strings.Contains(diagnostics, expected) {
			t.Fatalf("call-site default lost %q authority: %s", expected, diagnostics)
		}
	}
}

func TestBodyStructDefaultStillViolatesBoundsContracts(t *testing.T) {
	const source = `global values: i32[2] = [11, 22]
global cursor: usize = 0
struct Holder:
    value: i32 = values[cursor]
@inbounds
def caller() -> i32 fulfills NoBoundsChecks:
    holder: Holder = Holder{}
    return holder.value
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_default_caller_bounds.elisa", source)
	diagnostics := allDiagnostics(result)
	for _, expected := range []string{"fulfills NoBoundsChecks", "@inbounds function \"caller\""} {
		if !strings.Contains(diagnostics, expected) {
			t.Fatalf("body struct default lost %q authority: %s", expected, diagnostics)
		}
	}
}
