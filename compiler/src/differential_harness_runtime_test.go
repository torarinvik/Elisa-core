package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @differential is a first-class differential-oracle harness: it reuses the @property
// fuzz machinery (deterministic xorshift64 draws, shrinking, counterexample reporting)
// but is meant to assert bit-equality between an implementation and a reference over
// randomized inputs. A holding differential reports OK; a diverging one panics with the
// reproducible input that first diverged, labelled "differential ... counterexample".
//
// Like @property, a passing differential means "no mismatch found over N fuzzed inputs",
// NOT a proof of equivalence.

func TestDifferentialHarnessMatchingPairPasses(t *testing.T) {
	t.Parallel()
	const body = `
def reference_rotl8(x: u8, n: u8) -> u8:
    k: u8 = (n % 8).u8()
    return ((x.u32() << k.u32()) | (x.u32() >> (8 - k).u32())).u8()

def impl_rotl8_good(x: u8, n: u8) -> u8:
    k: u8 = (n % 8).u8()
    return ((x.u32() << k.u32()) | (x.u32() >> (8 - k).u32())).u8()

@differential
def diff_rotl8_good(x: u8, n: u8) -> bool:
    return impl_rotl8_good(x, n) == reference_rotl8(x, n)
`
	exit, stdout, stderr := runPropertyProgram(t, "diff_pass", body)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "[       OK ] __property_diff_rotl8_good") {
		t.Fatalf("expected matching differential to pass, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "passed=1") || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected passed=1 failed=0, got:\n%s", stdout)
	}
}

func TestDifferentialHarnessDivergingPairFails(t *testing.T) {
	t.Parallel()
	// impl diverges from the reference whenever a > 100 (off-by-one), so the harness
	// must find and report a diverging input.
	const body = `
def reference_add(a: i32, b: i32) -> i32:
    return a + b

def impl_add_bad(a: i32, b: i32) -> i32:
    if a > 100:
        return a + b + 1
    return a + b

@differential
def diff_add_bad(a: i32, b: i32) -> bool:
    return impl_add_bad(a, b) == reference_add(a, b)
`
	_, stdout, stderr := runPropertyProgram(t, "diff_fail", body)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected failed=1, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	combined := stdout + stderr
	// Report is labelled as a differential (not a plain property) and names the function.
	if !strings.Contains(combined, "differential diff_add_bad counterexample") {
		t.Fatalf("expected a differential counterexample report, got:\n%s", combined)
	}
	// The diverging input is reported by name and type.
	if !strings.Contains(combined, "a (i32) = ") {
		t.Fatalf("expected the diverging i32 input to be reported, got:\n%s", combined)
	}
}

func TestDifferentialHarnessMixedPairs(t *testing.T) {
	t.Parallel()
	// One matching, one diverging differential in the same file: matching passes, the
	// other fails. Confirms selection + reporting work alongside each other.
	const body = `
def reference_id(x: i64) -> i64:
    return x

def impl_id_good(x: i64) -> i64:
    return x

def impl_id_bad(x: i64) -> i64:
    if x > 5:
        return x + 1
    return x

@differential
def diff_id_good(x: i64) -> bool:
    return impl_id_good(x) == reference_id(x)

@differential
def diff_id_bad(x: i64) -> bool:
    return impl_id_bad(x) == reference_id(x)
`
	_, stdout, stderr := runPropertyProgram(t, "diff_mixed", body)
	if !strings.Contains(stdout, "[       OK ] __property_diff_id_good") {
		t.Fatalf("expected the matching differential to pass, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected exactly one failure, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "differential diff_id_bad counterexample") {
		t.Fatalf("expected the diverging differential to be reported, got:\n%s", stdout+stderr)
	}
}

func TestDifferentialHarnessConfigurableCaseCount(t *testing.T) {
	t.Parallel()
	const body = `
def reference_v(x: i32) -> i32:
    return x

def impl_v_bad(x: i32) -> i32:
    return x + 1

@differential(9)
def diff_v_bad(x: i32) -> bool:
    return impl_v_bad(x) == reference_v(x)
`
	_, stdout, stderr := runPropertyProgram(t, "diff_cases", body)
	if !strings.Contains(stdout+stderr, "9 cases") {
		t.Fatalf("expected the panic to report the overridden case count (9 cases), got:\n%s", stdout+stderr)
	}
}

func TestDifferentialHarnessRejectsUnsupportedSignatures(t *testing.T) {
	t.Parallel()
	const body = `
@differential
def bad_return(x: i32) -> i32:
    return x

@differential
def bad_param(s: cstr) -> bool:
    return true

@differential
def no_params() -> bool:
    return true
`
	path := filepath.Join(t.TempDir(), "diff_bad.elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
	out := stdout.String() + stderr.String()
	for _, want := range []string{
		`@differential function "bad_return" must return bool`,
		`harness cannot generate`,
		`must take at least one generated parameter`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected diagnostic %q, got:\n%s", want, out)
		}
	}
}
