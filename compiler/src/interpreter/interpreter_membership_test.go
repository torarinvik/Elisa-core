package interpreter_test

import (
	"testing"

	"llcontext/src/interpreter"
)

func TestExecuteMembershipExprShortCircuitsListCandidates(t *testing.T) {
	src := `def run() -> bool:
    return 1 in [1, 1 / 0]
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_short_circuit.llcontext", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "true" {
		t.Fatalf("expected membership short-circuit result true, got %s", got)
	}
}

func TestExecuteMembershipExprHandlesEmptyList(t *testing.T) {
	src := `def run() -> bool:
    return 1 in []
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_empty.llcontext", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "false" {
		t.Fatalf("expected empty membership result false, got %s", got)
	}
}
