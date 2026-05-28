package semantic

import (
	"strings"
	"testing"
)

const threadRegionPrelude = `def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
    return zeroed
extern new_arena(cap: usize) -> Arena
struct Node in owner:
    value: i32
def worker(r: owned Arena) -> i64:
    n: r Node[r]& = new[r] Node(7)
    out: i64 = n.value.i64()
    destroy r
    return out
`

// #4: moving an owned region into a worker transfers ownership across the
// thread boundary — the caller relinquishes it, the worker owns and consumes
// it. This is exclusive transfer (not sharing), so the un-shareable Arena is
// permitted when moved.
func TestSpawnMovesOwnedRegionIntoWorker(t *testing.T) {
	analyzeTreeTestSource(t, "thread_move_ok.elisa", threadRegionPrelude+`
def start() -> Thread[i64, Joinable]:
    r: owned Arena = new_arena(64)
    return spawn1(worker, move r)
`)
}

// Passing an owned region to a worker WITHOUT `move` would copy the allocator
// handle into the thread while the caller still owns it — rejected.
func TestSpawnRequiresMoveForOwnedRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "thread_no_move.elisa", threadRegionPrelude+`
def start() -> Thread[i64, Joinable]:
    r: owned Arena = new_arena(64)
    return spawn1(worker, r)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be moved") {
		t.Fatalf("expected spawn of un-moved owned region to require move; got: %s", all)
	}
}

// After moving the region into the thread, the caller may no longer use or
// destroy it (no double-free / use-after-transfer).
func TestRegionUnavailableAfterMovedToThread(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "thread_after.elisa", threadRegionPrelude+`
def start() -> void:
    r: owned Arena = new_arena(64)
    _ = spawn1(worker, move r)
    destroy r
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "already been destroyed") && !strings.Contains(all, "cannot") {
		t.Fatalf("expected region to be unavailable after transfer to thread; got: %s", all)
	}
}
