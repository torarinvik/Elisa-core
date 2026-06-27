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

func TestRunCLICompilesOptionalUFCSRefReceiverProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "ufcs_optional_ref_receiver_probe.elisa", `struct Counter:
    value: i64

def score_ref(counter: Counter&, delta: i64 = 1) -> i64:
    return counter.value + delta

def read(maybe_counter: Counter&?) -> i64:
    if maybe_counter?.score_ref(2) is scored:
        return scored
    return 0

def main() -> i64:
    counter: mutable Counter = Counter{value: 40}
    counter_ref: Counter& = (&counter).cast[Counter&]
    maybe_counter: Counter&? = counter_ref
    return read(maybe_counter)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected optional ref-helper UFCS probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesOptionalTransformCallProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "optional_transform_call_probe.elisa", `def bump(value: i64) -> i64:
    return value + 2

def read(maybe_value: i64?) -> i64:
    if maybe_value?.(bump) is value:
        return value
    return 0

def main() -> i64:
    return read(40)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected optional transform call probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "safe.transform.present") {
		t.Fatalf("expected optional transform call probe to lower optional control flow, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesOptionalTransformMemberCallProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "optional_transform_member_call_probe.elisa", `struct Checker:
    delta: i64

def adjust(self: Checker&, value: i64) -> i64:
    return value + self.delta

def read(self: Checker&, maybe_value: i64?) -> i64:
    if maybe_value?.(self.adjust) is value:
        return value
    return 0

def main() -> i64:
    checker: mutable Checker = Checker{delta: 2}
    checker_ref: Checker& = (&checker).cast[Checker&]
    return checker_ref.read(40)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected optional member transform call probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesOptionalFieldProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "optional_field_probe.elisa", `struct Counter:
    value: i64

def read(maybe_counter: Counter?) -> i64:
    if maybe_counter?.value is value:
        return value
    return 0

def main() -> i64:
    return read(Counter{value: 40})
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected optional field probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "optional.present") {
		t.Fatalf("expected optional field probe to lower optional control flow, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "optional.payload") {
		t.Fatalf("expected optional field probe to lower optional payload extraction, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "getelementptr inbounds nuw %Counter") {
		t.Fatalf("expected optional field probe to load Counter.value, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "valuephi") {
		t.Fatalf("expected optional field probe to merge optional values through a phi, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ret i64 %") {
		t.Fatalf("expected optional field probe to return the lowered field value, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @read") {
		t.Fatalf("expected optional field probe to emit the read function, got:\n%s", stdout.String())
	}
}

func TestRunCLICompilesOptionalAssignProbe(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "optional_assign_probe.elisa", `struct Counter:
    value: mutable i64

def write(maybe_counter: mutable Counter&?) -> i64:
    maybe_counter?.value ?= 7
    if maybe_counter?.value is value:
        return value
    return 0

def main() -> i64:
    counter: mutable Counter = Counter{value: 0}
    counter_ref: mutable Counter& = (&counter).cast[mutable Counter&]
    maybe_counter: mutable Counter&? = counter_ref
    return write(maybe_counter)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected optional assign probe to compile, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "define i64 @main()") {
		t.Fatalf("expected llvm output to contain main definition, got:\n%s", stdout.String())
	}
}
