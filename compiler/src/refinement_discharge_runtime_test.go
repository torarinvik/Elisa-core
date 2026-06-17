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

// buildRunRefinement compiles `program` at `opt` and returns (output, runErr).
func buildRunRefinement(t *testing.T, program string, opt backend.OptimizationLevel) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	if _, err := os.Stat(std); err != nil {
		t.Skipf("std runtime not found: %v", err)
	}
	dir := t.TempDir()
	rel, err := filepath.Rel(dir, std)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	src := "# include \"" + filepath.ToSlash(rel) + "\"\n" + program
	fixture := filepath.Join(dir, "refine.elisa")
	if err := os.WriteFile(fixture, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixture, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, result, ok := analyzeProgram(fixture, expanded, &stderr)
	if !ok {
		t.Fatalf("analyze failed:\n%s", stderr.String())
	}
	exe, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, stderr.String())
	}
	defer cleanup()
	out, runErr := exec.Command(exe).CombinedOutput()
	return strings.TrimSpace(string(out)), runErr
}

const refinementProgram = `
law Positive(self: i64) = self > 0

def neg() -> i64:
    return 0 - 3

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    x: i64 is Positive = %s
    print(x.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`

// A satisfying init passes the debug discharge check and runs normally.
func TestRefinementDischargeSatisfiedPasses(t *testing.T) {
	out, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "5", 1), backend.OptimizationLevel0)
	if err != nil {
		t.Fatalf("satisfying refinement should run cleanly: %v (out=%q)", err, out)
	}
	if out != "5" {
		t.Fatalf("want output 5, got %q", out)
	}
}

// A violating init TRAPS at debug — the refinement is enforced.
func TestRefinementDischargeViolatedTrapsInDebug(t *testing.T) {
	out, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "neg()", 1), backend.OptimizationLevel0)
	if err == nil {
		t.Fatalf("violating refinement must trap at debug, but program exited 0 (out=%q)", out)
	}
}

// The same violating init is ELIDED at release — "debug verifies what release assumes."
// -fbounds-check (ELISACORE_FORCE_BOUNDS_CHECK) legitimately forces the check even in release, so
// clear it here to assert the pure-release elision (other tests in the suite may leave it set).
func TestRefinementDischargeElidedInRelease(t *testing.T) {
	for _, key := range []string{"ELISACORE_FORCE_BOUNDS_CHECK", "ELISACORE_NOALIAS_MUTABLE_REFS"} {
		if prev, had := os.LookupEnv(key); had {
			_ = os.Unsetenv(key)
			t.Cleanup(func() { _ = os.Setenv(key, prev) })
		}
	}
	_, err := buildRunRefinement(t, strings.Replace(refinementProgram, "%s", "neg()", 1), backend.OptimizationLevel3)
	if err != nil {
		t.Fatalf("release build should elide the refinement check (no trap), got: %v", err)
	}
}
