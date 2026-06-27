package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStdAtomicCellAcceptedUnderConcurrencyStrict(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "atomic_cell_strict.elisa")
	src := `include "` + filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + `"

def use_atomic_cell() -> i64 can[Atomics.Load, Atomics.Store, Atomics.Rmw, Atomics.CompareExchange]:
    cell: mutable AtomicCell[i64] = atomic_cell(1)
    atomic_store_release(&cell, 2)
    before: i64 = atomic_fetch_add_acqrel(&cell, 3)
    ok: bool = atomic_compare_exchange_acqrel(&cell, 5, 8)
    after: i64 = atomic_load_acquire(&cell)
    return before + after + (1 if ok else 0)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write atomic cell strict fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-Wconcurrency", "-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected AtomicCell wrapper to pass -Wconcurrency, exit=%d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "strict concurrency error") || strings.Contains(stderr.String(), "legacy raw atomic surface") {
		t.Fatalf("expected AtomicCell wrapper to hide raw atomic surface from user code, got stderr:\n%s", stderr.String())
	}
}
