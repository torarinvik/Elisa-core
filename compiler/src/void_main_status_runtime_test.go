package main

import "testing"

// A source-level void main is an ordinary Elisa procedure, but the native C entry
// point must still return a deterministic success status. This catches the ABI bug
// where `ret void` left crt0's observed exit code in an undefined register.
func TestVoidMainReturnsDeterministicZeroStatus(t *testing.T) {
	status, output := s4CompileRun(t, `def main() -> void can[Abort.Panic]:
    return
`)
	if status != "RAN" || output != "" {
		t.Fatalf("void main: got status=%s output=%q, want RAN with no output", status, output)
	}
}
