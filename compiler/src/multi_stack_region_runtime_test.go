package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Multi-stack regions (Phase B1b): two unreserved growables in one inferred region are routed to
// separate parallel arenas, so interleaved growth never relocates either — each is its own arena
// tail. End-to-end: both fill correctly under interleaving (which, in a single arena, would force
// constant relocation), and both arenas free with the region.
func TestRunCLIMultiStackTwoGrowablesSeparateArenas(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "multi_stack_fixture.elisa")
	src := `@test
def multi_stack_runtime_test() -> void:
    can Memory.Allocate, Abort.Panic:
        foo: mutable darray[i64] = []
        bar: mutable darray[i64] = []
        for i in 0..<1000:
            foo.push(i.i64() * 2i64)
            bar.push(i.i64() * 3i64)
        if foo.count != 1000u:
            panic("multi-stack: foo length wrong")
        if bar.count != 1000u:
            panic("multi-stack: bar length wrong")
        if foo[0] != 0i64 or foo[999] != 1998i64:
            panic("multi-stack: foo values wrong")
        if bar[0] != 0i64 or bar[999] != 2997i64:
            panic("multi-stack: bar values wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write multi-stack fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected multi-stack runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] multi_stack_runtime_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected multi-stack output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
