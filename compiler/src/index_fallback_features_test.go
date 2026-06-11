package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIInterpretsIndexFallback(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "index_fallback_interpret.elisa", `def explode() -> i64:
    return 1 / 0

def main() -> i64:
    xs: darray[i64] = [3, 5]
    return (get xs[1] else explode()) + (get xs[9] else 10)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected index fallback interpret fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 15") {
		t.Fatalf("expected interpreter output to report result 15, got:\n%s", stdout.String())
	}
}

func TestRunCLIEmitsIndexFallbackLLVM(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "index_fallback_llvm.elisa", `def read(xs: darray[i64], i: usize) -> i64:
    return get xs[i] else 99
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected index fallback llvm fixture to succeed, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"safe.index.in.range", "safe.index.in_range", "safe.index.fallback"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llvm output to contain %q, got:\n%s", check, output)
		}
	}
}
