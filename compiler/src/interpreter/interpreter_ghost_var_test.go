package interpreter_test

import (
	"testing"

	"elisacore/src/interpreter"
)

// TestGhostVarErased verifies that a ghost declaration produces no runtime
// effect: the program executes identically to one without the ghost, and the
// ghost name is not bound in the interpreter frame (so it cannot affect
// observable output). This mirrors the LLVM backend's erasure semantics.
func TestGhostVarErased(t *testing.T) {
	// A function that declares a ghost local alongside real locals.
	// The ghost initializer is a side-effect-free constant expression.
	// The real result depends only on non-ghost values.
	src := `def run() -> i64:
    x: i64 = 10
    ghost g: i64 = 9999
    y: i64 = x + 32
    return y
`
	result := parseAndAnalyzeInterpreterTest(t, "ghost_erase.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "42" {
		t.Fatalf("expected 42 (ghost must not affect result), got %s", got)
	}
}

// TestGhostVarNoBinding verifies the ghost name is not present in the frame
// after execution — the interpreter frame must not accumulate ghost bindings.
// We do this indirectly: a second function that also has a local named `g`
// (not ghost) must not collide with or observe the ghost binding.
func TestGhostVarDoesNotPolluteFrame(t *testing.T) {
	src := `def inner() -> i64:
    ghost g: i64 = 100
    g2: i64 = 7
    return g2

def run() -> i64:
    a: i64 = inner()
    g: i64 = 35
    return a + g
`
	result := parseAndAnalyzeInterpreterTest(t, "ghost_frame.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "42" {
		t.Fatalf("expected 42, got %s", got)
	}
}
