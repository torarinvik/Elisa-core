package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end region inference: `in auto:` synthesizes a compiler-managed scoped
// region that owns every allocation in the block and is freed (O(1)) at block exit
// (docs/68). A darray grown to 1000 elements inside the block holds correct data and
// the program runs clean — no explicit region declaration or `@r` annotation needed.
func TestRunCLIInAutoSynthesizesScopedRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "in_auto_runtime_fixture.elisa")
	src := `@test
def in_auto_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        in auto:
            xs: mutable darray[i64] = []
            for i in 0..<1000:
                xs.push(i.i64())
            if xs[0] != 0i64 or xs[999] != 999i64:
                panic("auto region: data wrong")
            if xs.count != 1000:
                panic("auto region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write in-auto fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected in-auto runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] in_auto_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected in-auto output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
