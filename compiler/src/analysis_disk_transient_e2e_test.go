package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisDiskTransientSolverDoesNotHideVacuity(t *testing.T) {
	real, err := exec.LookPath("z3")
	if err != nil {
		t.Skip("z3 required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "unknown")
	solver := filepath.Join(dir, "z3")
	script := "#!" + python + "\n" + `import os,sys,re
if not os.path.exists(os.environ['TEST_SOLVER_UNKNOWN_MARKER']):
 os.execv(os.environ['TEST_SOLVER_REAL'], ['z3','-in'])
for line in sys.stdin:
 if '(check-sat)' in line: print('unknown',flush=True)
 if '(get-info :reason-unknown)' in line: print('(:reason-unknown "timeout")',flush=True)
 if '(get-info :all-statistics)' in line: print('(:rlimit-count 0)',flush=True)
 m=re.search(r'\(echo "([^"]+)"\)',line)
 if m: print(m.group(1),flush=True)
`
	if err := os.WriteFile(solver, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
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
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang required")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "unknown")
	solver := filepath.Join(dir, "z3")
	script := "#!" + python + "\n" + `import os,sys,re
if not os.path.exists(os.environ['TEST_SOLVER_UNKNOWN_MARKER']):
 os.execv(os.environ['TEST_SOLVER_REAL'], ['z3','-in'])
for line in sys.stdin:
 if '(check-sat)' in line: print('unknown',flush=True)
 if '(get-info :reason-unknown)' in line: print('(:reason-unknown "timeout")',flush=True)
 if '(get-info :all-statistics)' in line: print('(:rlimit-count 0)',flush=True)
 m=re.search(r'\(echo "([^"]+)"\)',line)
 if m: print(m.group(1),flush=True)
`
	if err := os.WriteFile(solver, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
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
