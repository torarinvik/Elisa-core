package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end proof that a `reserve_commit`-backed darray grows in place: the base
// never moves, so an interior reference taken before growth still aliases the live
// element afterward (docs/68 §4). The runtime mechanism is arena_realloc's tail
// extension -- it bumps the single contiguous reservation and returns the SAME
// pointer (zero copy, pages commit on first touch) instead of relocating the buffer.
//
// Discriminator: e0 is captured before 50k pushes. We then overwrite element 0 via
// the darray and read it back through e0. If the buffer had relocated (the chained
// behavior the stability rule forbids for reserve_commit), e0 would point at the
// abandoned pre-growth buffer and read the stale value, tripping the panic.
func TestRunCLIReserveCommitDarrayGrowsInPlace(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "reserve_commit_runtime_fixture.elisa")
	src := `@test
def reserve_commit_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        region big(1048576) using reserve_commit:
            xs: mutable darray[i64] @big = []
            xs.push(100)
            e0: i64& = &xs[0]
            for i in 0..<50000:
                xs.push(i.i64())
            xs[0] <- 777
            if e0[0] != 777:
                panic("reserve_commit relocated: interior ref stale after growth")
            if xs.count != 50001:
                panic("reserve_commit: unexpected count after growth")
            if xs[50000] != 49999:
                panic("reserve_commit: data corrupted across growth")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write reserve_commit fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected reserve_commit runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] reserve_commit_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected reserve_commit output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
