package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The @hot fast contract (docs/70) is enforcement, not just a hint: a hot kernel that obeys
// it (no allocation, no raw-pointer chasing — only indexed arithmetic over preallocated
// data) still compiles and runs correctly. The allocation that fills the inputs lives in
// the ordinary (non-@hot) caller, exactly the intended split.
func TestRunCLIHotKernelRuns(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "hot_kernel_fixture.elisa")
	src := `@hot
def dot(xs: darray[i64]&, ys: darray[i64]&, n: usize) -> i64:
    acc: mutable i64 = 0
    for i in 0..<n:
        acc <- acc + xs[i] * ys[i]
    return acc

@test
def hot_kernel_test() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        ys: mutable darray[i64] = []
        for i in 0..<4:
            xs.push(i.i64())
            ys.push(2)
        if dot(xs, ys, 4) != 12:
            panic("hot dot kernel wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write hot-kernel fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected @hot kernel test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] hot_kernel_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected hot-kernel output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
