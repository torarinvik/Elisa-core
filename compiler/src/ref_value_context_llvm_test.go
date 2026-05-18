package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIEmitsRefValueContextsToLLVM(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "ref_value_context_llvm.elisa", `def next(index: mutable usize&) -> usize:
    if index >= 2:
        return index + 1
    index <- index + 1
    return index

def read(text: static u8&) -> u8:
    ch: mutable u8 = 0
    ch <- text[0]
    return ch
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected ref value-context llvm fixture to succeed, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i64 @next", "define i8 @read"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llvm output to contain %q, got:\n%s", check, output)
		}
	}
}
