package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// docs/91 S4 end-to-end: growing a container field of a region-param struct ref param, with the
// caller's region threaded to the field growth, runs correctly; and the borrow-out use-after-free
// (returning a region-less view into the grown field, stored past the region) is a COMPILE error,
// not a runtime segfault.

func s4CompileRun(t *testing.T, prog string) (status, output string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	root := repoRootFromMainTest(t)
	std := filepath.Join(root, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	rel, _ := filepath.Rel(dir, std)
	fixture := filepath.Join(dir, "p.elisa")
	if err := os.WriteFile(fixture, []byte("# include \""+filepath.ToSlash(rel)+"\"\n"+prog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixture, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, res, ok := analyzeProgram(fixture, expanded, &stderr)
	if !ok {
		return "REJECTED", strings.TrimSpace(stderr.String())
	}
	exe, cleanup, err := buildNativeExecutable(res, nil, nil, "", backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return "BUILD-FAIL", err.Error()
	}
	out, runErr := exec.Command(exe).CombinedOutput()
	if runErr != nil {
		return "RUNERR", strings.TrimSpace(string(out)) + " " + runErr.Error()
	}
	return "RAN", strings.TrimSpace(string(out))
}

const s4StructHdr = "struct Mod[@owner]:\n    bits: mutable darray[u8]\n"

func TestS4FieldGrowthRunsEndToEnd(t *testing.T) {
	status, out := s4CompileRun(t, s4StructHdr+`def fill[@r](m: mutable Mod& @r) -> void can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    m.bits.push(66)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(4096):
        m: mutable Mod& @a = new[a] Mod(bits: [])
        fill(m) can Memory.Allocate, Abort.Panic
        print((m.bits[0].i64() + m.bits[1].i64()).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "131" {
		t.Fatalf("expected RAN 131 (caller region threaded to field growth), got %s %q", status, out)
	}
}

func TestS4ReturnViewUAFRejectedNotSegfault(t *testing.T) {
	status, _ := s4CompileRun(t, s4StructHdr+`def leak[@r](m: mutable Mod& @r) -> view[u8] can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    return m.bits[0:1]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    escaped: mutable view[u8] = zeroed
    region inner(64):
        m: mutable Mod& @inner = new[inner] Mod(bits: [])
        escaped <- leak(m) can Memory.Allocate, Abort.Panic
    print(escaped[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less view into a grown @r field and storing it past the region must be REJECTED at compile time (was a segfault), got %s", status)
	}
}
