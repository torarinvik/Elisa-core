//go:build cgo && (darwin || linux)

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// An external driver supplies runtime operands, so constant folding or frontend
// range checking cannot stand in for executing the generated overflow edge.
func TestSignedOverflowTrapExecutesAndReleaseWraps(t *testing.T) {
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang required for native trap execution")
	}
	result := parseAndAnalyzeBackendTest(t, "native_overflow_trap.elisa", "def add_sig(a: i64, b: i64) -> i64:\n    return a + b\n\nexport fn add_sig(a: i64, b: i64) -> i64 = add_sig\n")
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver.c")
	if err := os.WriteFile(driver, []byte(`#include <limits.h>
extern long long add_sig(long long, long long);
int main(int argc, char **argv) {
    if (argc > 1) return add_sig(LLONG_MAX, 1) == LLONG_MIN ? 0 : 7;
    return add_sig(20, 1) == 21 ? 0 : 8;
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel2} {
		t.Run("O"+strconv.Itoa(int(level)), func(t *testing.T) {
			object := filepath.Join(t.TempDir(), "checked.o")
			if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: level, Serial: true}); err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(t.TempDir(), "checked")
			if output, err := exec.Command(clang, "-O0", driver, object, "-o", executable).CombinedOutput(); err != nil {
				t.Fatalf("link: %v %s", err, output)
			}
			if output, err := exec.Command(executable).CombinedOutput(); err != nil {
				t.Fatalf("non-overflowing addition failed: %v %s", err, output)
			}
			err := exec.Command(executable, "overflow").Run()
			if level == OptimizationLevel2 {
				if err != nil {
					t.Fatalf("release addition did not wrap: %v", err)
				}
				return
			}
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("debug overflow did not trap: %v", err)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || (status.Signal() != syscall.SIGILL && status.Signal() != syscall.SIGTRAP) {
				t.Fatalf("debug overflow exited without an LLVM trap signal: %v", err)
			}
		})
	}
}
