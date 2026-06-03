package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: `adopt child into parent` splices the child region's block chain
// into the parent zero-copy. A darray built in the child stays intact (same
// bytes) after the splice, the child's storage is freed with the parent (not
// twice), and the program runs clean.
func TestRunCLIAdoptSplicesChildRegionIntoParent(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "adopt_runtime_fixture.elisa")
	src := `@test
def adopt_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region parent(4096):
            region child(256):
                v: mutable darray[u8] @child = []
                v.push(42u8)
                v.push(99u8)
                adopt child into parent
                if v[0] != 42u8:
                    panic("adopt corrupted v[0]")
                if v[1] != 99u8:
                    panic("adopt corrupted v[1]")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write adopt fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected adopt runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] adopt_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected adopt output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
