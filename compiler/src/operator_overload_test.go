//go:build cgo

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

// Operator overloading (Stage A: `+` -> `__add__`, value types first). `a + b` on a type that impls a
// protocol declaring `__add__` desugars to the static-impl method call `T.__add__(a, b)` (recorded as
// BinaryExpr.LoweredCall), so the backend emits the call and the effect/region collectors thread the
// callee's obligations to the `+` site — while a type WITHOUT `__add__` keeps the numeric-operands error.

func analyzeOverloadSource(t *testing.T, src string) *semantic.Result {
	t.Helper()
	return semantic.Analyze(parseFStringSource(t, src)) // reuses the parse helper from fstring_test.go
}

func TestOperatorOverloadTypesAndRegression(t *testing.T) {
	ok := analyzeOverloadSource(t, `
struct Vec3:
    x: i64
    y: i64
    z: i64

protocol Add:
    def __add__(self: Self, other: Self) -> Self

impl Add for Vec3:
    def __add__(self: Vec3, other: Vec3) -> Vec3:
        return Vec3{x: self.x + other.x, y: self.y + other.y, z: self.z + other.z}

def use(a: Vec3, b: Vec3) -> Vec3:
    return a + b + a
`)
	if errs := ok.Errors(); len(errs) != 0 {
		t.Fatalf("Vec3 with an __add__ impl must accept `a + b` (and chaining), got: %v", errs)
	}

	// A struct WITHOUT __add__ must still be rejected — no accidental blanket overloading.
	bad := analyzeOverloadSource(t, `
struct P:
    x: i64

def use(a: P, b: P) -> i64:
    c: P = a + b
    return c.x
`)
	if !strings.Contains(strings.Join(bad.Errors(), "\n"), "operator requires numeric operands") {
		t.Fatalf("a struct without __add__ must keep the numeric-operands error, got: %v", bad.Errors())
	}
}

// End-to-end: compile and RUN a value-type `a + b`, asserting the componentwise result.
func TestRunCLIOperatorOverloadNative(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "operator_overload.elisa")
	// Each of the 5 operators contributes a distinct bit; a fully-correct run returns 255.
	src := `
struct V:
    x: i64

protocol Ops:
    def __add__(self: Self, other: Self) -> Self
    def __sub__(self: Self, other: Self) -> Self
    def __mul__(self: Self, other: Self) -> Self
    def __eq__(self: Self, other: Self) -> bool
    def __cmp__(self: Self, other: Self) -> i64

impl Ops for V:
    def __add__(self: V, other: V) -> V:
        return V{x: self.x + other.x}
    def __sub__(self: V, other: V) -> V:
        return V{x: self.x - other.x}
    def __mul__(self: V, other: V) -> V:
        return V{x: self.x * other.x}
    def __eq__(self: V, other: V) -> bool:
        return self.x == other.x
    def __cmp__(self: V, other: V) -> i64:
        return self.x - other.x

def main() -> i64:
    a: V = V{x: 7}
    b: V = V{x: 3}
    r: mutable i64 = 0
    if (a + b).x == 10:
        r <- r + 1
    if (a - b).x == 4:
        r <- r + 2
    if (a * b).x == 21:
        r <- r + 4
    if a == V{x: 7}:
        r <- r + 8
    if a != b:
        r <- r + 16
    if b < a:
        r <- r + 32
    if a <= a:
        r <- r + 64
    if a > b:
        r <- r + 128
    return r
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	objPath := filepath.Join(fixtureDir, "op.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objPath, fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("compile failed (exit %d):\n%s", code, stderr.String())
	}
	exePath := filepath.Join(fixtureDir, "op")
	if out, err := exec.Command("clang", objPath, "-o", exePath).CombinedOutput(); err != nil {
		t.Fatalf("link failed: %v\n%s", err, out)
	}
	// All 5 operators correct -> each contributes a distinct bit -> 255.
	err := exec.Command(exePath).Run()
	got := 0
	if ee, ok := err.(*exec.ExitError); ok {
		got = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got != 255 {
		t.Fatalf("expected all of +,-,*,==,!=,<,<=,> correct (255), got exit %d", got)
	}
}
