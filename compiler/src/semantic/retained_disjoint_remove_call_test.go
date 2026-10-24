//go:build cgo

// Copy into src/semantic/ when the Core owner schedules this regression.
// Reuses disjointInfoForFunc, retainedParse, and
// assertRetainedLiveResultParity from the existing semantic test package.
package semantic

import "testing"

func TestRetainedFuncDisjointCallSiteRemovedAndRestoredAcrossDiskReload(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const before = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    axpy(&a, &b)
`
	const removed = retainedDisjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
`

	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	assertPair := func(tb testing.TB, label string, result *Result, want bool) {
		tb.Helper()
		info := disjointInfoForFunc(result, "axpy")
		got := info != nil && info.PairDistinct(0, 1)
		if got != want {
			tb.Fatalf("%s: axpy params 0,1 distinct = %v, want %v (info=%+v)", label, got, want, info)
		}
	}

	initial := AnalyzeWithOptions(retainedParse(t, before), AnalyzeOptions{})
	assertPair(t, "initial", initial, true)

	for _, step := range []struct {
		name   string
		source string
		want   bool
	}{
		{name: "remove sole fresh-anchor call", source: removed, want: false},
		{name: "restore sole fresh-anchor call", source: before, want: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			encoded, err := state.MarshalState()
			if err != nil {
				t.Fatalf("marshal retained state: %v", err)
			}
			state, err = RestoreAnalysisState(encoded)
			if err != nil {
				t.Fatalf("restore retained state: %v", err)
			}

			warm, err := state.Update(retainedParse(t, step.source), retainedParse(t, step.source), []byte(step.source))
			if err != nil {
				t.Fatalf("retained update: %v", err)
			}
			cold := AnalyzeWithOptions(retainedParse(t, step.source), AnalyzeOptions{})
			// Check the disjointness oracle before comprehensive Result metadata parity.
			assertPair(t, "cold "+step.name, cold, step.want)
			assertPair(t, "warm "+step.name, warm, step.want)
			assertRetainedDisjointProductParity(t, cold, warm)
		})
	}
}
