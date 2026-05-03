package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIInterpretsDefaultArgs(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "default_args_interpret.llcontext", `def add(x: i64, y: i64 = 7) -> i64:
    return x + y

def main() -> i64:
    f = add
    return add(5) + f(5)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected default-args interpret fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 24") {
		t.Fatalf("expected interpreter output to report result 24, got:\n%s", stdout.String())
	}
}
