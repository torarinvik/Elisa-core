package interpreter_test

import (
	"testing"

	"elisacore/src/interpreter"
)

func TestExecuteMachineIgnoresCoverageObligation(t *testing.T) {
	src := `def run() -> i64:
    n: mutable i64 = 0
    machine over n while n < 3:
        state Scan(step: i64)
        start Scan(0)
        Scan(step), _:
            n <- n + 1
            -> Scan(step + 1)
    return n
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_machine_coverage.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "3" {
		t.Fatalf("expected machine result 3, got %s", got)
	}
}
