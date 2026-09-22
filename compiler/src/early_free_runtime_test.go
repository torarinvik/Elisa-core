package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Phase B2 (early per-object reclamation): an own-stack growable that dies before the function
// returns and is never aliased has its arena freed early (right after its last use) rather than at
// region exit. End-to-end: the program still computes correctly (the early free is idempotent with
// the region-exit cleanup, and the object is genuinely dead at the free point).
func TestRunCLIEarlyFreeReclaimsDeadObject(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "early_free_fixture.elisa")
	src := `def f(n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        scratch: mutable darray[i64] = []
        scratch.push(10)
        scratch.push(20)
        summary: mutable i64 = scratch[0] + scratch[1]
        kept: mutable darray[i64] = []
        kept.push(summary)
        kept.push(summary * 2)
        return kept[0] + kept[1]
@test
def early_free_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if f(0) != 90:
            panic("early-free changed the result")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected early-free test to compile and pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] early_free_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// Two own-stack objects whose last use is the SAME statement are one lifetime class: both arenas
// are freed right after it. Codegen used to keep one arena per statement, so only one of the two
// was freed early, and a map walk picked which: the same binary emitted a different
// `arena_free` from run to run, and the other arena waited for the region exit. Each dying stack
// is freed twice (early, then the idempotent region-exit cleanup); the survivor once.
func TestEarlyFreeFreesEveryStackDyingAtOneStatement(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join(t.TempDir(), "early_free_shared_death.elisa")
	src := `def f(n: usize) -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        a.push(10)
        b.push(20)
        summary: mutable i64 = a[0] + b[0]
        kept: mutable darray[i64] = []
        kept.push(summary)
        kept.push(summary * 2)
        return kept[0] + kept[1]
@test
def shared_death_test() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if f(0) != 90:
            panic("freeing both dead stacks early changed the result")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "llvm", "-O0", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("emit llvm failed (%d):\n%s", exitCode, stderr.String())
	}
	ir := stdout.String()
	start := strings.Index(ir, "define i64 @f(")
	if start < 0 {
		t.Fatalf("no @f in the IR:\n%s", ir)
	}
	body := ir[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	freed := map[string]int{}
	var order []string
	for _, m := range regexp.MustCompile(`call void @arena_free\(ptr %"__auto_\d+#(\d+)"\)`).FindAllStringSubmatch(body, -1) {
		if freed[m[1]] == 0 {
			order = append(order, m[1])
		}
		freed[m[1]]++
	}
	if freed["1"] != 2 || freed["2"] != 2 || freed["3"] != 1 {
		t.Fatalf("want stacks #1 and #2 (a, b) freed early and at region exit, #3 (kept) only at exit; got %v in:\n%s", freed, body)
	}
	if strings.Join(order, ",") != "1,2,3" {
		t.Fatalf("want the early frees in stack-id order, then the survivor; got first frees in order %v", order)
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	stdout.Reset()
	stderr.Reset()
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 || !strings.Contains(stdout.String(), "passed=1") {
		t.Fatalf("expected the shared-death test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
