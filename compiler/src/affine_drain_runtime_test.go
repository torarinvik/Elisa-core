package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A consuming move-drain (`for g in move gs:`) is the sanctioned way to empty a container of
// affine (must-consume) handles. It loads each element by value (codegen identical to value
// iteration) and the body consumes it. This validates that the backend value-binding lowering
// works for an affine struct element type (previously blocked at the semantic layer) and that
// the drained values are observed correctly at runtime.
func TestRunCLIAffineMoveDrainConsumesElements(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "affine_drain_fixture.elisa")
	src := `linear struct Guard:
    token: i64

def make(n: i64) -> Guard:
    return Guard{token: n}

global mutable g_sum: i64 = 0

def consume(g: Guard) -> void:
    g_sum <- g_sum + g.token
    _ = move g

@test
def affine_move_drain_runtime_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make(10))
        gs.push(make(20))
        gs.push(make(12))
        for g in move gs:
            consume(move g)
        if g_sum != 42:
            panic("move-drain: consumed sum wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write drain fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected move-drain runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] affine_move_drain_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected drain output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
