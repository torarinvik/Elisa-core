package semantic

import (
	"strings"
	"testing"
)

// A const lives in read-only storage. Taking a *mutable* reference to it would
// hand out a writable pointer into rodata (crash on write), so it must be a
// compile-time error.
func TestMutableRefToConstRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_ref_to_const.elisa", `const TABLE: u32[3] = [1, 2, 3]
def f() -> u32:
    r: mutable u32[3]& = TABLE.ref[mutable u32[3]&]
    r[0] <- 9
    return TABLE[0]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "mutable reference to a const") {
		t.Fatalf("expected mutable ref to const to be rejected, got:\n%s", allDiagnostics(result))
	}
}

// A mutable borrow rooted in a const element must also be rejected.
func TestMutableRefToConstElementRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_ref_to_const_elem.elisa", `const TABLE: u32[3] = [1, 2, 3]
def f(i: usize) -> u32:
    if i < 3:
        r: mutable u32& = TABLE[i].ref[mutable u32&]
        r <- 9
    return TABLE[0]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "mutable reference to a const") {
		t.Fatalf("expected mutable ref to const element to be rejected, got:\n%s", allDiagnostics(result))
	}
}

// A *read-only* reference to a const is fine (it can be used for zero-copy
// views), and must NOT trigger the const-mutability diagnostic.
func TestReadOnlyRefToConstAllowed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "readonly_ref_to_const.elisa", `const TABLE: u32[3] = [1, 2, 3]
def f() -> u32[3]& :
    return TABLE.ref[u32[3]&]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "mutable reference to a const") {
		t.Fatalf("read-only ref to const should be allowed, got:\n%s", allDiagnostics(result))
	}
}

// A mutable borrow of a non-`mutable` stack local (e.g. for in-place init) must
// remain allowed — it is not a const and lives in writable stack storage.
func TestMutableRefToStackLocalAllowed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_ref_to_local.elisa", `def f() -> u32:
    a: u32[3] = [1, 2, 3]
    r: mutable u32[3]& = a.ref[mutable u32[3]&]
    r[0] <- 9
    return a[0]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "mutable reference to a const") {
		t.Fatalf("mutable ref to a stack local should be allowed, got:\n%s", allDiagnostics(result))
	}
}
