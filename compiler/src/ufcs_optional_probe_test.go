package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLICompilesUFCSValueReceiverProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "ufcs_value_receiver_probe.elisa", `struct Counter:
    value: i64

def score(counter: Counter, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: Counter = Counter{value: 40}
    return counter.score()
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected value-receiver UFCS probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesExplicitRefHelperProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "ufcs_explicit_ref_helper_probe.elisa", `struct Counter:
    value: i64

def score_ref(counter: Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: mutable Counter = Counter{value: 40}
    return score_ref((&counter).cast[Counter&], 2)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected explicit ref-helper probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesUFCSAutorefRefReceiverProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "ufcs_autoref_ref_receiver_probe.elisa", `struct Counter:
    value: i64

def score_ref(counter: Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: mutable Counter = Counter{value: 40}
    return counter.score_ref()
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected receiver-style ref-helper probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}
