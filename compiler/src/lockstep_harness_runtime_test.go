package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @lockstep is a STATE-LEVEL differential oracle, the stateful sibling of @differential.
// A @lockstep function is a "step-and-project" stepper: given fuzzed scalar seed inputs
// plus a trailing `step` parameter, it builds a state from the seeds, advances it `step`
// times, and returns an i64 projection of the observable state. The harness pairs it with
// a same-signature reference stepper named `<name>__ref`, drives both from step 0 upward,
// and on the first step at which their projections diverge reports the step index plus the
// shrunk seeds — reusing the @property fuzz/shrink/counterexample machinery.
//
// Like @property/@differential, a passing @lockstep means "no mismatch found over the
// fuzzed seeds x steps", NOT a proof of equivalence.

// A matching impl/reference pair holds: the harness finds no diverging step and reports OK.
func TestLockstepHarnessMatchingPairPasses(t *testing.T) {
	t.Parallel()
	const body = `
@lockstep
def counter(start: i64, inc: i64, step: int) -> i64:
    acc: mutable i64 = start
    for i in 0..<step:
        _ = i
        acc <- acc + inc
    return acc

def counter__ref(start: i64, inc: i64, step: int) -> i64:
    acc: mutable i64 = start
    for i in 0..<step:
        _ = i
        acc <- acc + inc
    return acc
`
	exit, stdout, stderr := runPropertyProgram(t, "lockstep_pass", body)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "[       OK ] __property_counter") {
		t.Fatalf("expected matching lockstep to pass, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "passed=1") || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected passed=1 failed=0, got:\n%s", stdout)
	}
}

// An injected divergence (impl drifts once the running total exceeds 50) must be caught,
// the seeds shrunk to a minimal diverging pair, and the FIRST diverging step index named.
func TestLockstepHarnessDivergingPairCaughtAndStepReported(t *testing.T) {
	t.Parallel()
	const body = `
@lockstep
def counter(start: i64, inc: i64, step: int) -> i64:
    acc: mutable i64 = start
    for i in 0..<step:
        _ = i
        acc <- acc + inc
        if acc > 50:
            acc <- acc + 1
    return acc

def counter__ref(start: i64, inc: i64, step: int) -> i64:
    acc: mutable i64 = start
    for i in 0..<step:
        _ = i
        acc <- acc + inc
    return acc
`
	_, stdout, stderr := runPropertyProgram(t, "lockstep_fail", body)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected failed=1, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "lockstep counter counterexample") {
		t.Fatalf("expected a lockstep counterexample report, got:\n%s", combined)
	}
	// The harness must report the step index at which the steppers first diverged.
	if !strings.Contains(combined, "first diverging step = ") {
		t.Fatalf("expected the first diverging step to be reported, got:\n%s", combined)
	}
	// With inc shrunk to 1 (smallest non-zero step that still reaches the >50 threshold)
	// the running total crosses 50 at step 51, so the reported step is exactly 51 — proof
	// the shrinker drove the seeds to the minimal diverging witness.
	if !strings.Contains(combined, "first diverging step = 51") {
		t.Fatalf("expected the shrunk witness to report step 51, got:\n%s", combined)
	}
	// The diverging seed inputs are reported by name and type.
	if !strings.Contains(combined, "inc (i64) = ") {
		t.Fatalf("expected the diverging i64 seed to be reported, got:\n%s", combined)
	}
}

// The analyzer rejects @lockstep functions whose shape the harness cannot drive.
func TestLockstepHarnessRejectsUnsupportedSignatures(t *testing.T) {
	t.Parallel()
	const body = `
@lockstep
def bad_return(x: i64, step: int) -> bool:
    return true

@lockstep
def missing_step(x: i64, y: i64) -> i64:
    return x

@lockstep
def bad_seed(s: cstr, step: int) -> i64:
    return 0
`
	path := filepath.Join(t.TempDir(), "lockstep_bad.elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
	out := stdout.String() + stderr.String()
	for _, want := range []string{
		`@lockstep function "bad_return" must return i64`,
		`final parameter must be named "step"`,
		`seed parameter "s" has type cstr, which the harness cannot generate`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected diagnostic %q, got:\n%s", want, out)
		}
	}
}
