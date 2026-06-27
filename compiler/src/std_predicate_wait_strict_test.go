package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStdPredicateWaitAcceptedUnderConcurrencyStrict(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "predicate_wait_strict.elisa")
	src := `include "` + filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + `"

def ready() -> bool:
    return true

def use_predicate_wait() -> void:
    can Memory.Allocate, Memory.Release, Sync.Lock, Sync.Unlock, Sync.Wait, Sync.Notify, Abort.Panic:
        mu: mutable Mutex = mutex()
        cv: mutable CondVar = condvar()
        guard: mutable MutexGuard[Held] = mutex_lock(&mu)
        guard <- predicate_wait(&cv, move guard, ready)
        predicate_notify_all(&cv)
        mutex_unlock(move guard)
        condvar_dispose(&cv)
        mutex_dispose(&mu)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write predicate wait strict fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-Wconcurrency", "-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected predicate wait wrapper to pass -Wconcurrency, exit=%d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "strict concurrency error") || strings.Contains(stderr.String(), "legacy raw condition-variable surface") || strings.Contains(stderr.String(), "legacy raw notification surface") {
		t.Fatalf("expected predicate wait wrapper to hide raw condvar surface from user code, got stderr:\n%s", stderr.String())
	}
}
