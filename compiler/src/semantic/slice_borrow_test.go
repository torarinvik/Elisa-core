package semantic

import (
	"strings"
	"testing"
)

// A Slice[T] borrows a darray's backing. Growing the darray afterward can relocate the
// buffer, dangling the slice -- so using the slice after a push must be flagged as a stale
// reference, exactly like an interior darray ref used after push. Slice routes through the
// same storage-view invalidation chokepoint (slice() recorded as a dependency on the source
// darray), so this is the borrow guarantee, not a bespoke checker.
func TestSliceInvalidatedByPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "slice_push.elisa", `struct Slice[T]:
    base: uintptr
    count: usize

def slice[T](da: mutable darray[T]&) -> Slice[T]:
    return Slice[T] { base: (&da[0.usize()]).cast[uintptr], count: da.count }

def get[T](s: Slice[T], i: usize) -> T:
    p: T& = s.base.cast[T&]
    return p[i]

def f(owner: mutable Arena&) -> u8:
    xs: mutable darray[u8] = []
    in owner:
        xs.push(1)
        s: Slice[u8] = slice(&xs)
        xs.push(2)
        return s.get(0.usize())
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected Slice used after a relocating push to be a stale reference, got:\n%s", allDiagnostics(result))
	}
}

// Slicing and using the slice WITHOUT growing the source in between is the normal data-parallel
// pattern and must NOT be flagged -- the backing never relocated.
func TestSliceNotInvalidatedWithoutGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "slice_nogrow.elisa", `struct Slice[T]:
    base: uintptr
    count: usize

def slice[T](da: mutable darray[T]&) -> Slice[T]:
    return Slice[T] { base: (&da[0.usize()]).cast[uintptr], count: da.count }

def get[T](s: Slice[T], i: usize) -> T:
    p: T& = s.base.cast[T&]
    return p[i]

def f(owner: mutable Arena&) -> u8:
    xs: mutable darray[u8] = []
    in owner:
        xs.push(1)
        xs.push(2)
        s: Slice[u8] = slice(&xs)
        return s.get(0.usize())
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected a Slice used without growing the source to stay valid, got:\n%s", allDiagnostics(result))
	}
}
