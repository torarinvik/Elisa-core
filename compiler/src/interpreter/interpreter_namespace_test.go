package interpreter_test

import (
	"testing"

	"elisacore/src/interpreter"
)

// The interpreter has no namespace/using context of its own; it consumes the
// analyzer's recorded resolution so namespaced names resolve via `using`,
// `from … import`, and explicit `::` qualification.
func TestInterpreterResolvesNamespacedNames(t *testing.T) {
	result := parseAndAnalyzeInterpreterTest(t, "interpreter_namespace.elisa", `module Foo:
    def bar() -> int:
        return 10

using Foo
from Foo import bar

def run() -> int:
    return bar() + Foo::bar()
`)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "20" {
		t.Fatalf("expected namespaced name resolution result 20, got %s", got)
	}
}
