package semantic

import (
	"reflect"
	"strings"
	"testing"
)

// A replay that defers proof coordinates must differ from a relocating replay
// only in the withheld report, and must not be reusable afterwards.
func TestDeferredProofCoordinatesOnlyWithholdTheReport(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = `law Positive(self: i64) = self > 0

def changed(n: i64) -> i64 is Positive:
    return 5

def other(n: i64) -> i64 is Positive:
    return 7
`
	// The added line moves every later proof fact.
	after := strings.Replace(before, "    return 5\n", "    x: i64 = 5\n    return 5\n", 1)
	replay := func(deferred bool) (*Result, *RetainedAnalysis) {
		initial, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
		if len(initial.ProofReport) == 0 {
			t.Fatal("fixture produced no proof report")
		}
		state.DeferProofReportCoordinates = deferred
		result, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
		if err != nil {
			t.Fatal(err)
		}
		return result, state
	}
	relocated, relocatedState := replay(false)
	deferred, deferredState := replay(true)
	if !deferredState.ProofCoordinatesDeferred() || relocatedState.ProofCoordinatesDeferred() {
		t.Fatal("deferral state does not match the request")
	}
	if len(relocated.ProofReport) == 0 || deferred.ProofReport != nil {
		t.Fatalf("report: relocated %d, deferred %v", len(relocated.ProofReport), deferred.ProofReport)
	}
	if !reflect.DeepEqual(relocated.Diagnostics, deferred.Diagnostics) || relocated.ElisionSummary != deferred.ElisionSummary {
		t.Fatal("deferred replay changed diagnostics or elision telemetry")
	}
	if _, err := deferredState.MarshalState(); err == nil {
		t.Fatal("deferred state was published")
	}
	if _, err := relocatedState.MarshalState(); err != nil {
		t.Fatal(err)
	}
	if _, err := deferredState.Update(retainedParse(t, before), retainedParse(t, before), []byte(before)); err == nil {
		t.Fatal("deferred state accepted another update")
	}
}
