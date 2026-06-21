package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A region declared without an explicit capacity — `region r:` — defaults to a small
// chained backing that grows as needed, so you never have to guess a number. This
// pushes well past the default initial block to prove it grows rather than just fits.
func TestRunCLIRegionWithoutCapacityGrows(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_optional_capacity_fixture.elisa")
	src := `@test
def region_optional_capacity_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region r:
            xs: mutable darray[i64] @r = []
            for i in 0..<20000:
                xs.push(i.i64())
            if xs[0] != 0 or xs[19999] != 19999:
                panic("no-capacity region: data wrong after growth")
            if xs.count != 20000:
                panic("no-capacity region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write optional-capacity fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected no-capacity region test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_optional_capacity_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected optional-capacity output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
