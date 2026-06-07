package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Per-stack strategy inference (Phase C, end-to-end): a darray whose footprint is provably bounded
// (sole growth is a single counting-loop push) and which has an interior reference taken into it is
// automatically backed by a reserve_commit arena — its base never moves. An interior reference taken
// during the fill stays valid across the remaining growth (which a chained backing would reject and
// relocate), with no manual `using reserve_commit`. The reservation is overflow-proof by the bound.
func TestRunCLIReserveCommitInferenceKeepsInteriorRefStable(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "reserve_commit_inference_fixture.elisa")
	src := `def build(n: usize) -> i64:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        anchor: mutable i64&? = null
        for i in 0..<n:
            xs.push(i.i64() * 10i64)
            if i == 0:
                anchor <- &xs[0]
        if anchor != null:
            return anchor[0]
        return -1i64
@test
def reserve_commit_inference_test() -> void:
    can Memory.Allocate, Abort.Panic:
        if build(500u) != 0i64:
            panic("inferred reserve_commit: interior ref stale after growth")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected reserve_commit-inference test to compile and pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] reserve_commit_inference_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIReserveCommitInferenceCountsListPushElements(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "reserve_commit_list_push_fixture.elisa")
	src := `def build(n: usize) -> i64:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        anchor: mutable i64&? = null
        for i in 0..<n:
            xs.push([i.i64(), (i.i64() * 10)])
            if i == 0:
                anchor <- &xs[0]
        if anchor != null:
            return anchor[0] + gap
        return -1
@test
def reserve_commit_list_push_bound_test() -> void:
    can Memory.Allocate, Abort.Panic:
        if build(500) != 0:
            panic("inferred reserve_commit: list-push bound under-reserved")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write list-push reserve_commit fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected list-push reserve_commit inference test to compile and pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] reserve_commit_list_push_bound_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
