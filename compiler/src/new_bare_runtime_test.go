package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: bare `new T(...)` (no `[...]`) is the default inferred-region
// allocation. A function that returns a bare-`new` struct is region-polymorphic
// (the region threads in from the caller); the value is correct at runtime, with
// no `new[auto]` / `in auto:` / explicit region anywhere.
func TestRunCLIBareNewDefaultsToInferredRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "bare_new_runtime_fixture.elisa")
	src := `struct Point:
    x: i64
    y: i64

def make_point(x: i64, y: i64) -> Point&:
    return new Point(x: x, y: y)

@test
def bare_new_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        p: Point& = new Point(x: 3, y: 4)
        if p.x + p.y != 7:
            panic("bare new: local alloc wrong")
        q: Point& = make_point(10, 20)
        if q.x + q.y != 30:
            panic("bare new: region-polymorphic return wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write bare-new fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected bare-new runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] bare_new_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected bare-new output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
