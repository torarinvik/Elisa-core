package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The @property harness synthesizes, for each @property function, a parameterless
// driver that feeds it deterministic pseudo-random inputs (xorshift64, name-seeded)
// and panics on the first counterexample. The driver runs like a @test, so a
// holding property reports OK and a violated one reports a failure. These tests
// compile fixtures to a native test binary via `-emit test` and inspect the report.

func runPropertyProgram(t *testing.T, name, body string) (int, string, string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	path := filepath.Join(t.TempDir(), name+".elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"-emit", "test", "-O0", path}, &stdout, &stderr)
	return exit, stdout.String(), stderr.String()
}

func TestPropertyHarnessHoldingPropertiesPass(t *testing.T) {
	const body = `
@property
def add_commutes(a: i32, b: i32) -> bool:
    return a + b == b + a

@property
def and_idempotent(b: bool) -> bool:
    return (b and b) == b

@property
def abs_nonneg(x: i64) -> bool:
    y: mutable i64 = x
    if y < 0:
        y <- 0 - y
    return y >= 0
`
	exit, stdout, stderr := runPropertyProgram(t, "prop_pass", body)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "passed=3") || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("expected passed=3 failed=0, got:\n%s", stdout)
	}
	for _, n := range []string{"__property_add_commutes", "__property_and_idempotent", "__property_abs_nonneg"} {
		if !strings.Contains(stdout, "[       OK ] "+n) {
			t.Fatalf("expected %q OK, got:\n%s", n, stdout)
		}
	}
}

func TestPropertyHarnessCounterexampleFails(t *testing.T) {
	const body = `
@property
def bogus_always_small(x: i32) -> bool:
    return x < 100

@property
def add_commutes(a: i32, b: i32) -> bool:
    return a + b == b + a
`
	_, stdout, stderr := runPropertyProgram(t, "prop_fail", body)
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected failed=1, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "[       OK ] __property_add_commutes") {
		t.Fatalf("expected holding property to still pass, got:\n%s", stdout)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "bogus_always_small") {
		t.Fatalf("expected counterexample report to name the property, got:\n%s", combined)
	}
}

func TestPropertyHarnessReportsFailingInputs(t *testing.T) {
	const body = `
@property
def bogus(a: i32, flag: bool) -> bool:
    return a < 100
`
	_, stdout, stderr := runPropertyProgram(t, "prop_report", body)
	combined := stdout + stderr
	if !strings.Contains(combined, "counterexample") {
		t.Fatalf("expected a counterexample report, got:\n%s", combined)
	}
	// The report names each parameter with its type and a concrete value.
	if !strings.Contains(combined, "a (i32) = ") {
		t.Fatalf("expected the failing i32 value to be reported by name, got:\n%s", combined)
	}
	if !strings.Contains(combined, "flag (bool) = ") {
		t.Fatalf("expected the failing bool value to be reported by name, got:\n%s", combined)
	}
}

func TestPropertyHarnessGeneratesFloats(t *testing.T) {
	const body = `
@property
def f64_add_commutes(a: f64, b: f64) -> bool:
    return a + b == b + a

@property
def f32_sq_nonneg(x: f32) -> bool:
    return x * x >= 0.0.f32()

@property
def f64_bogus(x: f64) -> bool:
    return x < 100.0
`
	_, stdout, stderr := runPropertyProgram(t, "prop_float", body)
	if !strings.Contains(stdout, "[       OK ] __property_f64_add_commutes") {
		t.Fatalf("expected float commutativity to hold, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[       OK ] __property_f32_sq_nonneg") {
		t.Fatalf("expected f32 square property to hold, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "failed=1") {
		t.Fatalf("expected the bogus float property to fail, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "f64_bogus") {
		t.Fatalf("expected counterexample to name f64_bogus, got:\n%s", stdout+stderr)
	}
}

func TestPropertyHarnessConfigurableCaseCount(t *testing.T) {
	const body = `
@property(1000)
def big_run(a: i32) -> bool:
    return a == a

@property(7)
def small_bogus(x: i32) -> bool:
    return x < 100
`
	_, stdout, stderr := runPropertyProgram(t, "prop_cases", body)
	if !strings.Contains(stdout, "[       OK ] __property_big_run") {
		t.Fatalf("expected reflexive property to hold over 1000 cases, got:\n%s", stdout)
	}
	if !strings.Contains(stdout+stderr, "7 cases") {
		t.Fatalf("expected the panic to report the overridden case count (7 cases), got:\n%s", stdout+stderr)
	}
}

func TestPropertyHarnessRejectsBadCaseCount(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"zero", "@property(0)\ndef z(a: i32) -> bool:\n    return true\n", "positive integer case count"},
		{"nonint", "@property(foo)\ndef z(a: i32) -> bool:\n    return true\n", "positive integer case count"},
	} {
		path := filepath.Join(t.TempDir(), "prop_"+tc.name+".elisa")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		var stdout, stderr bytes.Buffer
		runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
		if out := stdout.String() + stderr.String(); !strings.Contains(out, tc.want) {
			t.Fatalf("%s: expected %q, got:\n%s", tc.name, tc.want, out)
		}
	}
}

func TestPropertyHarnessRejectsUnsupportedSignatures(t *testing.T) {
	const body = `
@property
def bad_return(x: i32) -> i32:
    return x

@property
def bad_param(s: cstr) -> bool:
    return true

@property
def no_params() -> bool:
    return true
`
	path := filepath.Join(t.TempDir(), "prop_bad.elisa")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
	out := stdout.String() + stderr.String()
	for _, want := range []string{
		`must return bool`,
		`harness cannot generate`,
		`must take at least one generated parameter`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected diagnostic %q, got:\n%s", want, out)
		}
	}
}
