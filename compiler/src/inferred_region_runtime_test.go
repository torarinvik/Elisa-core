package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end region inference: a function body with region-less allocations gets a
// synthesized auto region that owns every allocation and is freed (O(1)) at scope exit
// (docs/68; `in auto:` was hard-removed in favor of this default). A darray grown to
// 1000 elements holds correct data and the program runs clean — no explicit region
// declaration, block, or `@r` annotation needed.
func TestRunCLIInferredRegionSynthesizesScopedRegion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inferred_region_runtime_fixture.elisa")
	src := `@test
def inferred_region_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[i64] = []
        for i in 0..<1000:
            xs.push(i.i64())
        if xs[0] != 0 or xs[999] != 999:
            panic("auto region: data wrong")
        if xs.count != 1000:
            panic("auto region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inferred-region fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inferred-region runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] inferred_region_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inferred-region output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
