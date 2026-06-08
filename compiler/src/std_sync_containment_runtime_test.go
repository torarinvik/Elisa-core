package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Sync-encapsulation: the persistent pool synchronizes internally with mutex/condvar
// (Sync.Lock/Unlock/Wait/Notify), but those effects are DROPPED at the pool boundary
// via `trusted`, so they never leak into caller effect signatures. The fixture's driver
// grants only an explicit Pool.* effect list (NO Sync.*) and drives pool_submit1 +
// task_group_wait_all + pool_shutdown; if any Sync effect escaped, it would fail with
// "missing required effect facts". Guards the frictionless contract for manual-list
// callers, not just `can Pooled:`/`can Parallel:` alias users.
func TestRunCLIStdSyncContainmentRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_sync_containment_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("sync containment runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_sync_containment_smoke") {
		t.Fatalf("expected sync containment smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
