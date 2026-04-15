package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLICompilesUFCSValueReceiverProbe(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "ufcs_value_receiver_probe.llcontext", `struct Counter:
    value: i64

def score(counter: Counter, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: Counter = Counter(40)
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
	sourcePath := writeImplicitContextFixture(t, "ufcs_explicit_ref_helper_probe.llcontext", `struct Counter:
    value: i64

def score_ref(counter: any Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: mutable Counter = Counter(40)
    return score_ref((&counter).cast[any Counter&], 2)
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

func TestRunCLIRejectsUFCSAutorefRefReceiverProbe(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "ufcs_autoref_ref_receiver_probe.llcontext", `struct Counter:
    value: i64

def score_ref(counter: any Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def main() -> i64:
    counter: mutable Counter = Counter(40)
    return counter.score_ref()
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected receiver-style ref-helper probe to fail, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `has no field`) || !strings.Contains(stderr.String(), `score_ref`) {
		t.Fatalf("expected receiver-style ref-helper diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsOptionalUFCSRefReceiverProbe(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "ufcs_optional_ref_receiver_probe.llcontext", `struct Counter:
    value: i64

def score_ref(counter: any Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def read(maybe_counter: any Counter&?) -> i64:
    if let scored = maybe_counter?.score_ref(2):
        return scored
    return 0

def main() -> i64:
    counter: mutable Counter = Counter(40)
    counter_ref: any Counter& = (&counter).cast[any Counter&]
    maybe_counter: any Counter&? = counter_ref
    return read(maybe_counter)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected optional ref-helper UFCS probe to fail, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `has no field`) || !strings.Contains(stderr.String(), `score_ref`) {
		t.Fatalf("expected optional ref-helper UFCS diagnostic, got:\n%s", stderr.String())
	}
}