package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"elisacore/src/backend"
)

// Consecutive-index check merging must not change behaviour: in-range reads give the same value,
// a read that the original would skip (the other arm of a conditional) must not trap even though
// it is out of range, and an out-of-range access still traps. Checked at -O0 and -O3.
func TestRunIndexCheckMergeBehaviour(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "index_check_merge.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `
@inline(never)
def pack3(a: darray[u8]&, i: usize) -> u32:
    return (a[i].u32() << 16) | (a[i + 1].u32() << 8) | a[i + 2].u32()

@inline(never)
def pick(a: darray[u8]&, i: usize, near: bool) -> u32:
    return a[i].u32() if near else a[i + 9].u32() + a[i + 1].u32()

def main(argc: i64, argv: mutable void&) -> i64 can[Console, Memory.Allocate, Abort.Panic]:
    a: mutable darray[u8] = []
    for k in 0..<6 |a|:
        a.push((k * 3 + 1).u8())
    mode: i64 = argc - 1
    if mode == 0:
        # in range everywhere: 1,4,7 -> 0x010407
        return (pack3(&a, 0) % 251).i64()
    if mode == 1:
        # near=true never touches a[i + 9]: must NOT trap
        return pick(&a, 5, true).i64()
    # i = 4: a[4], a[5] in range, a[6] is not -> trap at the a[i + 2] access
    return pack3(&a, 4).i64()
`
	if err := os.WriteFile(fixturePath, []byte("include \""+std+"\"\n"+src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	for _, opt := range []backend.OptimizationLevel{backend.OptimizationLevel0, backend.OptimizationLevel3} {
		var diag bytes.Buffer
		expanded, err := readSourceWithIncludes(fixturePath, map[string]bool{})
		if err != nil {
			t.Fatalf("expand includes: %v", err)
		}
		_, result, ok := analyzeProgram(fixturePath, expanded, &diag)
		if !ok {
			t.Fatalf("analyze failed:\n%s", diag.String())
		}
		exePath, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &diag)
		if err != nil {
			t.Fatalf("build (opt=%v): %v\n%s", opt, err, diag.String())
		}
		run := func(args ...string) (int, bool) {
			runErr := exec.Command(exePath, args...).Run()
			if runErr == nil {
				return 0, false
			}
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) {
				t.Fatalf("run (opt=%v): %v", opt, runErr)
			}
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return -1, true
			}
			return exitErr.ExitCode(), false
		}
		// mode 0: pack3(a, 0) = 0x010407 = 66567; 66567 % 251 = 52.
		if code, trapped := run(); trapped || code != 52 {
			t.Fatalf("opt=%v in-range: got status %d trapped=%v, want 52", opt, code, trapped)
		}
		// mode 1: pick(a, 5, near=true) = a[5] = 16; a[14] is never evaluated.
		if code, trapped := run("x"); trapped || code != 16 {
			t.Fatalf("opt=%v conditional: got status %d trapped=%v, want 16 (no trap)", opt, code, trapped)
		}
		// mode 2: pack3(a, 4) reads a[6] of 6 elements: must trap.
		if code, trapped := run("x", "y"); !trapped {
			t.Fatalf("opt=%v out-of-range: want a trap, got status %d", opt, code)
		}
		cleanup()
	}
}
