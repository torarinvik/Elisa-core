package semantic

import (
	"strings"
	"testing"
)

// poolBorrowPrelude is a minimal self-contained prelude for pool interior-borrow tests.
// It defines:
//   - `affine struct Pooled[T]` with a single `ptr: mutable heap T&` field — this is
//     exactly what isRegionPoolHandleType matches (name == "Pooled").
//   - `struct Node` with a mutable i64 field to borrow.
//   - `def release[T]` that consumes the handle via `move`, marking it as consumed in
//     the affine tracker. This mirrors what `pool.release(move h)` does in the stdlib.
const poolBorrowPrelude = `affine struct Pooled[T]:
    ptr: mutable heap T&

struct Node:
    val: mutable i64

def release[T](h: Pooled[T]) -> void:
    _ = move h

`

// helper: run analysis on poolBorrowPrelude + body, expect an error containing "cannot be used".
func expectPoolBorrowRejected(t *testing.T, name, body string) {
	t.Helper()
	src := poolBorrowPrelude + body
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name+".elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !strings.Contains(diag, "cannot be used") {
		t.Fatalf("expected use-after-release diagnostic (\"cannot be used\") for %s, got:\n%s", name, diag)
	}
}

// helper: run analysis on poolBorrowPrelude + body, expect no errors.
func expectPoolBorrowAccepted(t *testing.T, name, body string) {
	t.Helper()
	src := poolBorrowPrelude + body
	result := analyzeFunctionAnalysisTestSource(t, name+".elisa", src)
	if diag := allDiagnostics(result); strings.Contains(diag, "cannot be used") {
		t.Fatalf("expected pool borrow usage to be accepted for %s, got:\n%s", name, diag)
	}
}

// — Vector 1: borrow stored in a darray, then used after release —

// The interior borrow is pushed into a darray literal, then the handle is released.
// Reading the borrow back out of the darray and writing through it must be rejected.
func TestPoolBorrowInDarrayUsedAfterReleaseIsRejected(t *testing.T) {
	expectPoolBorrowRejected(t, "pool_borrow_darray_uaf", `
def darray_uaf() -> void:
    h: Pooled[Node] = zeroed
    xs: mutable darray[mutable heap Node&] = [h.ptr]
    release(move h)
    b: mutable heap Node& = get xs[0] else zeroed
    b.val <- 9
`)
}

// Pushing the borrow into a darray BEFORE release is fine; use of it also before
// release must not trigger a false positive.
func TestPoolBorrowInDarrayUsedBeforeReleaseIsAccepted(t *testing.T) {
	expectPoolBorrowAccepted(t, "pool_borrow_darray_valid", `
def darray_valid() -> void:
    h: Pooled[Node] = zeroed
    xs: mutable darray[mutable heap Node&] = [h.ptr]
    b: mutable heap Node& = get xs[0] else zeroed
    b.val <- 9
    release(move h)
`)
}

// — Vector 2: borrow wrapped in a struct returned from a helper, then released by caller —

// A helper wraps the raw interior pointer in a struct and returns it. After the caller
// calls release on the handle, using the borrow through the returned struct field is
// use-after-free. This tests that struct-embedded borrows propagated through a function
// return keep their taint at the call site.
func TestPoolBorrowInReturnedStructUsedAfterReleaseIsRejected(t *testing.T) {
	expectPoolBorrowRejected(t, "pool_borrow_return_struct_uaf", `
struct Wrapper:
    p: mutable heap Node&

def wrap(p: mutable heap Node&) -> Wrapper:
    return Wrapper(p)

def return_struct_uaf() -> void:
    h: Pooled[Node] = zeroed
    w: Wrapper = wrap(h.ptr)
    release(move h)
    w.p.val <- 42
`)
}

// Using the borrow through the returned struct BEFORE release is safe.
func TestPoolBorrowInReturnedStructUsedBeforeReleaseIsAccepted(t *testing.T) {
	expectPoolBorrowAccepted(t, "pool_borrow_return_struct_valid", `
struct Wrapper:
    p: mutable heap Node&

def wrap(p: mutable heap Node&) -> Wrapper:
    return Wrapper(p)

def return_struct_valid() -> void:
    h: Pooled[Node] = zeroed
    w: Wrapper = wrap(h.ptr)
    w.p.val <- 42
    release(move h)
`)
}

// — Vector 3: borrow threaded through a pass-through call —

// A function that echoes a reference parameter back: the return-borrow summary
// records "returns param 0". At the call site with a pool interior borrow as the
// argument, the summary is instantiated to recover the alias. Calling the passthrough
// must not launder the alias.
func TestPoolBorrowPassthroughCallUsedAfterReleaseIsRejected(t *testing.T) {
	expectPoolBorrowRejected(t, "pool_borrow_passthrough_uaf", `
def passthrough(p: mutable heap Node&) -> mutable heap Node&:
    return p

def passthrough_uaf() -> void:
    h: Pooled[Node] = zeroed
    b: mutable heap Node& = passthrough(h.ptr)
    release(move h)
    b.val <- 7
`)
}

// Passthrough is fine when the borrow is used before release.
func TestPoolBorrowPassthroughCallUsedBeforeReleaseIsAccepted(t *testing.T) {
	expectPoolBorrowAccepted(t, "pool_borrow_passthrough_valid", `
def passthrough(p: mutable heap Node&) -> mutable heap Node&:
    return p

def passthrough_valid() -> void:
    h: Pooled[Node] = zeroed
    b: mutable heap Node& = passthrough(h.ptr)
    b.val <- 7
    release(move h)
`)
}

// — Vector 4: release inside a branch, use after the join —

// The handle is released on only the true branch. At the join point the interior
// borrow must stay tainted (conservative union, not intersection) because on the
// true path the slot is already recycled. Using the borrow after the branch must
// be rejected regardless of whether the release actually ran.
func TestPoolBorrowConditionalReleaseUsedAfterJoinIsRejected(t *testing.T) {
	expectPoolBorrowRejected(t, "pool_borrow_cond_release_uaf", `
def cond_release_uaf(flag: bool) -> void:
    h: Pooled[Node] = zeroed
    b: mutable heap Node& = h.ptr
    if flag:
        release(move h)
    b.val <- 3
`)
}

// When the handle is NOT released on any branch, using the borrow afterwards is fine.
func TestPoolBorrowNoBranchReleaseIsAccepted(t *testing.T) {
	expectPoolBorrowAccepted(t, "pool_borrow_no_release_valid", `
def no_release_valid(flag: bool) -> void:
    h: Pooled[Node] = zeroed
    b: mutable heap Node& = h.ptr
    if flag:
        b.val <- 1
    else:
        b.val <- 2
    release(move h)
`)
}
