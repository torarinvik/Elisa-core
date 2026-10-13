//go:build cgo

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"elisacore/src/backend"
)

// An `extern exit` marked noreturn still exits with the requested status and runs nothing after
// the call, at -O0 and -O3.
func TestRunLibcNoreturnExitStatus(t *testing.T) {
	src := `extern exit(code: i32) -> void
extern puts(s: cstr) -> i32

def main(argc: i64, argv: mutable void&) -> i64 can[Console, Unsafe.PointerCast]:
    _ = puts("before")
    if argc == 1:
        exit(3)
    _ = puts("after")
    return 0
`
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "libc_noreturn.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	for _, opt := range []backend.OptimizationLevel{backend.OptimizationLevel0, backend.OptimizationLevel3} {
		var stderr bytes.Buffer
		expanded, err := readSourceWithIncludes(fixturePath, map[string]bool{})
		if err != nil {
			t.Fatalf("expand includes: %v", err)
		}
		_, result, ok := analyzeProgram(fixturePath, expanded, &stderr)
		if !ok {
			t.Fatalf("analyze failed:\n%s", stderr.String())
		}
		exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
		if err != nil {
			t.Fatalf("build (opt=%v): %v\n%s", opt, err, stderr.String())
		}
		output, runErr := exec.Command(exePath).CombinedOutput()
		cleanup()
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 3 {
			t.Fatalf("opt=%v: want exit status 3, got %v (output %q)", opt, runErr, output)
		}
		if string(output) != "before\n" {
			t.Fatalf("opt=%v: want only \"before\", got %q", opt, output)
		}
	}
}
