package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A chained arena can reclaim a darray's old backing allocation when that
// allocation is still the current block's tail. The small initial capacity also
// ensures this exercises several geometric growth steps; without reclamation,
// repeated growth retains every predecessor.
func TestRunCLIChainedDarrayGrowsAtArenaTail(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "chained_realloc_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `@test
def chained_realloc_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        arena: mutable Arena = zeroed
        in arena:
            xs: mutable darray[i64] @arena = []
            xs.push(7)
            before: usize = arena_used_slots(&arena)
            for i in 1..<257:
                xs.push(i.i64())
            after: usize = arena_used_slots(&arena)
            if xs.count != 257 or xs.capacity != 512:
                panic("chained darray did not grow geometrically")
            if before != 8:
                panic("chained darray initial capacity was unexpected")
            if after != 512:
                panic("chained darray tail growth accounting failed")
            if xs[0] != 7 or xs[256] != 256:
                panic("chained darray data corrupted across in-place growth")
        arena_free(&arena)
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("failed to write chained realloc fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected chained realloc runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] chained_realloc_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
// A non-tail darray backing allocation must be returned to its owning region
// after the replacement is copied. This is the case that occurs when recursive
// AST construction grows one side table after another allocation has already
// been made. The next allocation should reuse the reclaimed span instead of
// permanently retaining another block from the bump cursor.
func TestRunCLIChainedDarrayReusesNonTailBacking(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixturePath := filepath.Join(t.TempDir(), "chained_non_tail_realloc_runtime_fixture.elisa")
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatalf("resolve std runtime path: %v", err)
	}
	src := `@test
def chained_non_tail_realloc_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        arena: mutable Arena = zeroed
        in arena:
            xs: mutable darray[i64] @arena = []
            xs.push(7)
            filler: mutable darray[i64] @arena = []
            filler.push(11)
            before: usize = arena_used_slots(&arena)
            for i in 1..<9:
                xs.push(i.i64())
            after_grow: usize = arena_used_slots(&arena)
            reused: mutable darray[i64] @arena = []
            reused.push(13)
            after_reuse: usize = arena_used_slots(&arena)
			if before != 16:
				panic("non-tail darray initial capacity was unexpected")
            if xs.capacity != 16:
                panic("non-tail darray did not grow to the expected capacity")
            if after_grow != 32:
                panic("non-tail darray replacement allocation accounting failed")
            if after_reuse != 32:
                panic("non-tail darray backing was not reclaimed and reused")
            if xs[0] != 7 or xs[8] != 8 or filler[0] != 11 or reused[0] != 13:
                panic("non-tail darray reuse corrupted live data")
        arena_free(&arena)
`
	full := "include \"" + std + "\"\n" + src
	if err := os.WriteFile(fixturePath, []byte(full), 0o644); err != nil {
		t.Fatalf("failed to write non-tail realloc fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected non-tail realloc runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] chained_non_tail_realloc_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
