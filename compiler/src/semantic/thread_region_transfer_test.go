package semantic

import (
	"strings"
	"testing"
)

// The thread-share / region-transfer safety net keys on the `pool_submit1`
// name (the lowering target of the safe `submit` sugar). Raw `spawn1` was
// removed from the public surface, so these tests drive the net through
// `submit[&pool] worker(arg)` — exercising the same checker branch.
const threadRegionPrelude = `def pool_submit1[A, R](pool: mutable ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending]:
    return zeroed
extern new_arena(cap: usize) -> Arena
struct Node[region owner]:
    value: i32
def worker(r: owned Arena) -> i64:
    n: Node[r]& @r = new[r] Node(7)
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
def start() -> Task[i64, Pending]:
    pool: mutable ThreadPool = zeroed
    r: owned Arena = new_arena(64)
    return submit[&pool] worker(move r)
`)
}

// Passing an owned region to a worker WITHOUT `move` would copy the allocator
// handle into the thread while the caller still owns it — rejected.
func TestSpawnRequiresMoveForOwnedRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "thread_no_move.elisa", threadRegionPrelude+`
def start() -> Task[i64, Pending]:
    pool: mutable ThreadPool = zeroed
    r: owned Arena = new_arena(64)
    return submit[&pool] worker(r)
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
    pool: mutable ThreadPool = zeroed
    r: owned Arena = new_arena(64)
    _ = submit[&pool] worker(move r)
    destroy r
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "already been destroyed") && !strings.Contains(all, "cannot") {
		t.Fatalf("expected region to be unavailable after transfer to thread; got: %s", all)
	}
}
