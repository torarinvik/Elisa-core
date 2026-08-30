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
// allocation is still the current block's tail. Without the generic tail path,
// the first geometric growth retained both the 256-slot old buffer and the
// 512-slot replacement (and repeated growth retained every predecessor).
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
            if before != 256 or after != 512:
                panic("chained darray retained a dead tail allocation")
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
