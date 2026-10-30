package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transientNativeSolver is a test-only native SMT protocol fixture. Its marker
// controls whether it reports unknown or execs the real solver, so cache
// admission is exercised even on the transient path.
func transientNativeSolver(t *testing.T, dir string) string {
	t.Helper()
	clang, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang required for native solver fixture")
	}
	source := filepath.Join(dir, "solver.c")
	binary := filepath.Join(dir, "z3")
	const program = `#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
int main(void) {
 const char *marker = getenv("TEST_SOLVER_UNKNOWN_MARKER");
 const char *real = getenv("TEST_SOLVER_REAL");
 if (!marker || !real) return 2;
 if (access(marker, F_OK) != 0) { execl(real, "z3", "-in", (char *)0); return 3; }
 char *line = NULL; size_t capacity = 0;
 while (getline(&line, &capacity, stdin) >= 0) {
   if (strstr(line, "(check-sat)")) puts("unknown");
   if (strstr(line, "(get-info :reason-unknown)")) puts("(:reason-unknown \"timeout\")");
   if (strstr(line, "(get-info :all-statistics)")) puts("(:rlimit-count 0)");
   char *echo = strstr(line, "(echo ");
   if (echo) { char *start = strchr(echo, 34); char *end = start ? strchr(start+1, 34) : NULL; if (end) { *end = 0; puts(start+1); } }
   fflush(stdout);
 }
 free(line); return 0;
}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, clang, "-O0", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build solver fixture: %v: %s", err, out)
	}
	if _, err := toolchainNativeContentStamp(binary); err != nil {
		t.Fatalf("solver fixture is not cache-eligible: %v", err)
	}
	return binary
}

func TestAnalysisDiskTransientSolverDoesNotHideVacuity(t *testing.T) {
	real, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 required")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "unknown")
	solver := transientNativeSolver(t, dir)
	if err := os.WriteFile(marker, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "vacuity.elisa")
	if err := os.WriteFile(source, []byte("def impossible(x: i64) -> i64:\n    requires x > 0\n    requires x < 0\n    return x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISA_Z3": solver, "TEST_SOLVER_UNKNOWN_MARKER": marker, "TEST_SOLVER_REAL": real, "ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "cache"), "ELISACORE_ANALYSIS_CACHE": "1", "ELISACORE_ANALYSIS_CACHE_DEBUG": "0", "ELISACORE_BUILD_CACHE": "1", "ELISACORE_RETAINED_SERVER": ""}
	args := []string{"-emit", "obj", "-O0", "-o", filepath.Join(dir, "out.o"), source}
	first, rc := runDiskCLIChildResult(t, env, args...)
	if rc != 0 {
		t.Fatalf("unknown result did not compile: %d %s", rc, first)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "cache", "analysis", "*.state")); err != nil || len(matches) != 0 {
		t.Fatalf("transient state published: %v %v", matches, err)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "cache", "*", "transcript.json")); err != nil || len(matches) != 0 {
		t.Fatalf("transient object published: %v %v", matches, err)
	}
	control := filepath.Join(dir, "control.elisa")
	if err := os.WriteFile(control, []byte("def plain(x: i64) -> i64:\n    return x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	controlArgs := []string{"-emit", "obj", "-O0", "-o", filepath.Join(dir, "control.o"), control}
	if out, code := runDiskCLIChildResult(t, env, controlArgs...); code != 0 {
		t.Fatalf("control failed: %d %s", code, out)
	}
	if matches, err := filepath.Glob(filepath.Join(dir, "cache", "*", "transcript.json")); err != nil || len(matches) != 1 {
		t.Fatalf("control did not publish object: %v %v", matches, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	second, rc := runDiskCLIChildResult(t, env, args...)
	if rc == 0 || !strings.Contains(second, "preconditions") || !strings.Contains(second, "contradictory") {
		t.Fatalf("fresh retry hid vacuity: %d %s", rc, second)
	}
	if strings.Contains(second, "reuse unchanged revision") {
		t.Fatalf("transient snapshot was reused: %s", second)
	}
}

func TestRunnerCachesDoNotPublishTransientSolverOutcome(t *testing.T) {
	real, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 required")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "unknown")
	solver := transientNativeSolver(t, dir)
	if err := os.WriteFile(marker, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "transient.elisa")
	control := filepath.Join(dir, "control.elisa")
	if err := os.WriteFile(source, []byte("def impossible(x: i64) -> i64:\n    requires x > 0\n    requires x < 0\n    return x\n\n@test\ndef smoke() -> void:\n    assert true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("@test\ndef smoke() -> void:\n    assert true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ELISA_Z3": solver, "TEST_SOLVER_UNKNOWN_MARKER": marker, "TEST_SOLVER_REAL": real, "ELISAC_BUILD_CACHE_DIR": filepath.Join(dir, "build"), "ELISA_TEST_RUNNER_CACHE_DIR": filepath.Join(dir, "runners", "test_runners"), "ELISACORE_TEST_CACHE": "1", "ELISACORE_RETAINED_SERVER": ""}
	out, rc := runDiskCLIChildResult(t, env, "-emit", "test", "-permissive", source)
	if rc != 0 || !strings.Contains(out, "passed=1") {
		t.Fatalf("transient test run failed: %d %s", rc, out)
	}
	for _, pattern := range []string{"test_runners/*/runner", "test_runner_early/*.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, "runners", pattern))
		if err != nil || len(matches) != 0 {
			t.Fatalf("tainted runner published: %v %v", matches, err)
		}
	}
	out, rc = runDiskCLIChildResult(t, env, "-emit", "test", "-permissive", control)
	if rc != 0 || !strings.Contains(out, "passed=1") {
		t.Fatalf("control failed: %d %s", rc, out)
	}
	for _, pattern := range []string{"test_runners/*/runner", "test_runner_early/*.json"} {
		matches, err := filepath.Glob(filepath.Join(dir, "runners", pattern))
		if err != nil || len(matches) != 1 {
			t.Fatalf("clean runner not cached: %v %v", matches, err)
		}
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	out, rc = runDiskCLIChildResult(t, env, "-emit", "test", "-permissive", source)
	if rc != 0 || !strings.Contains(out, "contradictory") || !strings.Contains(out, "passed=1") {
		t.Fatalf("fresh retry lost diagnostic: %d %s", rc, out)
	}
}
