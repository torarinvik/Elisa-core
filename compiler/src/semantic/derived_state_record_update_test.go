//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestDerivedStateRecordUpdateRechecksPredicate(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"cannot_keep_alive_after_zero", "def invalid(p: Player[Alive]) -> Player[Alive]:\n    p{health = 0}\n", true},
		{"can_establish_dead", "def valid(p: Player[Alive]) -> Player[Dead]:\n    p{health = 0}\n", false},
		{"can_preserve_alive", "def valid(p: Player[Alive]) -> Player[Alive]:\n    p{health = 1}\n", false},
		{"unknown_update_cannot_keep_alive", "def invalid(p: Player[Alive], health: i64) -> Player[Alive]:\n    p{health = health}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", derivedStatePlayerPreamble+tc.body, AnalyzeOptions{})
			if got := len(result.Errors()) != 0; got != tc.wantError {
				t.Fatalf("error=%v, want %v: %v", got, tc.wantError, result.Errors())
			}
		})
	}
}

func TestDerivedStateRecordUpdateAtomicFieldsAndUnchangedPredicate(t *testing.T) {
	prelude := `struct Pair[state Low | High]:
    left: i64
    right: i64
    tag: i64
    derive state:
        Low when self.left < self.right
        High when self.left >= self.right
`
	for _, body := range []string{
		"def valid(p: Pair[Low]) -> Pair[Low]:\n    p{left = 5, right = 10}\n",
		"def valid(p: Pair[Low]) -> Pair[High]:\n    p{left = 10, right = 5}\n",
		"def valid(p: Pair[Low]) -> Pair[Low]:\n    p{tag = 10}\n",
	} {
		analyzeTreeTestSource(t, "record_snapshot.elisa", prelude+body)
	}
}

func TestDerivedStateSingleCaseUnknownIsNotProof(t *testing.T) {
	prelude := `struct Positive[state Valid]:
    value: i64
    derive state:
        Valid when self.value > 0
`
	for _, body := range []string{
		"def invalid(p: Positive[Valid], value: i64) -> Positive[Valid]:\n    p{value = value}\n",
		"def invalid(value: i64) -> Positive[Valid]:\n    Positive{value: value}\n",
	} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unknown_single_state.elisa", prelude+body, AnalyzeOptions{})
		if len(result.Errors()) == 0 {
			t.Fatal("unknown predicate was accepted as the sole derived state")
		}
	}
	analyzeTreeTestSource(t, "known_single_state.elisa", prelude+"def valid(p: Positive[Valid]) -> Positive[Valid]:\n    p{value = 1}\ndef initial() -> Positive[Valid]:\n    Positive{value: 1}\n")
}

func TestDerivedStateRecordUpdateRejectsGapAndOverlap(t *testing.T) {
	for _, tc := range []struct{ name, first, second, message string }{
		{"gap", "> 0", "< 0", "does not satisfy any derived state"},
		{"overlap", ">= 0", "<= 0", "satisfies multiple derived states"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "struct Number[state First | Second]:\n    value: i64\n    derive state:\n        First when self.value " + tc.first + "\n        Second when self.value " + tc.second + "\ndef invalid(p: Number[First]) -> Number:\n    p{value = 0}\n"
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+".elisa", source, AnalyzeOptions{})
			if !strings.Contains(allDiagnostics(result), tc.message) {
				t.Fatal(allDiagnostics(result))
			}
		})
	}
}
