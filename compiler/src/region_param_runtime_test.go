package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: a region-parameterized function pushes through a `darray[T] @r`
// parameter with NO ambient `in <arena>:` scope of its own — the growth arena
// is threaded by the compiler as a hidden Arena& param sourced from the
// caller's region. Verifies correct byte values (no arena corruption) across
// multiple capacity growths.
func TestRunCLIRegionParamContainerPushThreadsArenaViaHiddenParam(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "region_param_push_fixture.elisa")
	src := `def fill[region r](out: mutable darray[u8] @r) -> u64:
    for i in 0..<10:
        out.push((65 + i).u8())
    sum: mutable u64 = 0u64
    for i in 0..<out.count.i64():
        sum <- sum + out[i].u64()
    return sum

@test
def region_param_push_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region a(4096):
            v: mutable darray[u8] @a = []
            s: u64 = fill(v)
            if s != 695u64:
                panic("expected sum 695 (65..74)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-param fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-param push test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] region_param_push_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected region-param output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
