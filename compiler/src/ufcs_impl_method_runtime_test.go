package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// UFCS protocol-impl dispatch end-to-end: `value.method(args)` on a concrete receiver lowers
// to the conforming impl method (and a protocol default method) and runs correctly.
func TestRunCLIUFCSImplMethodDispatch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "ufcs_impl_method_fixture.elisa")
	src := `struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool
    def neq(self: Self, other: Self) -> bool:
        return not self.eq(other)

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def both_equal[T: Eq](a: T, b: T) -> bool:
    return a.eq(b)

@test
def ufcs_impl_method_test() -> void:
    can Abort.Panic:
        p: Point = Point{x: 3}
        q: Point = Point{x: 3}
        r: Point = Point{x: 9}
        if not p.eq(q):
            panic("UFCS impl method eq wrong")
        if p.neq(q):
            panic("UFCS default method neq wrong on equal")
        if not p.neq(r):
            panic("UFCS default method neq wrong on differing")
        if not both_equal[Point](p, q):
            panic("UFCS bounded-generic dispatch wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write ufcs impl fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected ufcs impl method test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] ufcs_impl_method_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected ufcs impl output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
