package semantic

import (
	"strings"
	"testing"
)

// spawn_daemon is the blessed detached, process-lifetime thread surface. Its
// safety reuses the existing thread-share analysis: a daemon may capture only
// provably-shareable state (scalars, static refs, atomics, Mutex/CondVar,
// frozen stores), exactly like spawn1 — a thread-unsafe capture demands an
// explicit Unsafe.ThreadShare vouch. These tests pin both directions through a
// local spawn_daemon declaration (mirroring the thread_region_transfer prelude).
const spawnDaemonPrelude = `def spawn_daemon[A, permission P](fn: func(A) -> void can[P], arg: A):
    return
`

// A daemon capturing a plain scalar by value copies safely — accepted with no
// Unsafe.ThreadShare vouch.
func TestSpawnDaemonScalarCaptureNeedsNoVouch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "spawn_daemon_scalar.elisa",
		spawnDaemonPrelude+`
def worker(start: i64) -> void:
    return
def go() -> void:
    spawn_daemon(worker, 41)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "thread share") {
		t.Fatalf("scalar daemon capture must need no thread-share vouch, got:\n%s", allDiagnostics(result))
	}
}

// A daemon capturing a non-static mutable reference aliases shared mutable state
// across the thread boundary — it must require an explicit Unsafe.ThreadShare vouch.
func TestSpawnDaemonMutableRefCaptureRequiresVouch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "spawn_daemon_mutref.elisa",
		spawnDaemonPrelude+`
struct Box:
    value: mutable i64
def worker(b: mutable heap Box&) -> void:
    b.value <- 99
def go(b: mutable heap Box&) -> void:
    spawn_daemon(worker, b)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "thread share requires") {
		t.Fatalf("mutable-heap-ref daemon capture must require an Unsafe.ThreadShare vouch, got:\n%s", allDiagnostics(result))
	}
}
