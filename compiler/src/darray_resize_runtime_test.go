package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `da.resize(n)` presizes a darray to exactly n live elements in one allocation (capacity >= n,
// count := n), so a fill loop writes by index with no per-element push/capacity-check. This is
// the runtime-sized fixed-array primitive: the count is a runtime value, allocated once.
// Region-less (inference-by-default supplies the arena) -- no explicit `in <arena>:` needed.
func TestRunCLIDarrayResizePresizesAndFillsByIndex(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "darray_resize_fixture.elisa")
	src := `@test
def darray_resize_runtime_test() -> void:
    can Memory.Allocate, Abort.Panic:
        n: usize = 1000
        xs: mutable darray[i64] = []
        _ = xs.resize(n)
        if xs.count != n:
            panic("resize: count not sealed to n")
        for i in 0..<n:
            xs[i] <- (i.i64()) * 3
        if xs[0] != 0:
            panic("resize: element 0 wrong")
        if xs[999] != 2997:
            panic("resize: last element wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write resize fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected resize runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] darray_resize_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected resize output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
