package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Auto-reservation (region inference, Phase A): a fresh inferred-region darray filled by a
// `for i in 0..<n:` push loop is auto-presized with an inserted `reserve(n)`, so the fill never
// reallocates. End-to-end: the program compiles and runs with correct length and values, and the
// reserve is transparent (pure optimization — no observable behavior change).
func TestRunCLIAutoReserveBoundedFillRunsCorrectly(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "auto_reserve_fixture.elisa")
	src := `@test
def auto_reserve_runtime_test() -> void:
    can Memory.Allocate, Abort.Panic:
        n: usize = 5000u
        xs: mutable darray[i64] = []
        for i in 0..<n:
            xs.push((i.i64()) * 2i64)
        if xs.count != n:
            panic("auto-reserve: wrong length")
        if xs[0] != 0i64:
            panic("auto-reserve: element 0 wrong")
        if xs[4999] != 9998i64:
            panic("auto-reserve: last element wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write auto-reserve fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected auto-reserve runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] auto_reserve_runtime_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected auto-reserve output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
