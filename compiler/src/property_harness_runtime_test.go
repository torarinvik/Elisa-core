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
