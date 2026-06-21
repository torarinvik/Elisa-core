package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: `promote <value> into <region>` bitwise-relocates the value's own
// backing storage into the longer-lived region and rebinds it, so the value stays
// valid after the source region is destroyed (docs/67 §2). Covers both promotable
// shapes: a reference (pointee relocated) and a darray (buffer relocated, header
// repointed).
//
// The discriminator is `destroy scratch` BEFORE reading the promoted values: if the
// relocation or rebind were missing, value/xs would still point into the freed scratch
// region and read garbage (or fault), tripping the panic.
func TestRunCLIPromoteRelocatesValueIntoLongerLivedRegion(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "promote_runtime_fixture.elisa")
	src := `@test
def promote_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region keep(4096)
        region scratch(4096)
        in scratch:
            value: i32& = new[scratch] 7
            promote value into keep
            xs: mutable darray[i64] = []
            xs.push(11)
            xs.push(22)
            promote xs into keep
            destroy scratch
            if value[0] != 7:
                panic("promote ref: value corrupted after source region destroyed")
            if xs[0] != 11 or xs[1] != 22:
                panic("promote darray: buffer corrupted after source region destroyed")
        destroy keep
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write promote fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected promote runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] promote_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected promote output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
