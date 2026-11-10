package interpreter_test

import (
	"testing"

	"elisacore/src/interpreter"
)

func TestExecuteResolvedNamedArguments(t *testing.T) {
	const source = `def encode(first: i64, second: i64 = 2, third: i64 = 3) -> i64:
    return 100 * first + 10 * second + third

def run() -> i64:
    return encode(third: 7, first: 1)
`
	result := parseAndAnalyzeInterpreterTest(t, "named_arguments.elisa", source)
	execution, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.Return.String(); got != "127" {
		t.Fatalf("expected 127, got %s", got)
	}
}
