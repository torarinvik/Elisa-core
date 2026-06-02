package semantic

import (
	"strings"
	"testing"
)

// A signed-returning function with a `-1` not-found sentinel, whose result is cast to
// an unsigned index without any guard, is flagged: the negative sentinel becomes a huge
// out-of-bounds index. (The ge_find_native_region -> ge_native_regions[i.usize()] class.)
func TestSentinelIndexUnguardedFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sentinel_index_bad.elisa", `def find(target: i32) -> int:
    if target > 0:
        return 0
    return -1

def use_bad(xs: darray[i32]&, target: i32) -> i32:
    idx: int = find(target)
    return xs[idx.usize()]
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "negative not-found sentinel") {
		t.Fatalf("expected sentinel-index warning, got:\n%s", allDiagnostics(result))
	}
}

// The same sentinel result, but guarded with `if idx >= 0`, must NOT be flagged —
// comparing the value is the correct idiom and clears the taint.
func TestSentinelIndexGuardedNotFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sentinel_index_ok.elisa", `def find(target: i32) -> int:
    if target > 0:
        return 0
    return -1

def use_ok(xs: darray[i32]&, target: i32) -> i32:
    idx: int = find(target)
    if idx >= 0:
        return xs[idx.usize()]
    return 0
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "negative not-found sentinel") {
		t.Fatalf("guarded sentinel index must not be flagged, got:\n%s", allDiagnostics(result))
	}
}

// An index whose value comes from a non-sentinel source (no negative literal in the
// callee) must NOT be flagged, even unguarded — keeps the lint low-noise.
func TestSentinelIndexNonSentinelNotFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sentinel_index_nonsentinel.elisa", `def pick(target: i32) -> int:
    if target > 0:
        return 1
    return 0

def use_plain(xs: darray[i32]&, target: i32) -> i32:
    idx: int = pick(target)
    return xs[idx.usize()]
`, AnalyzeOptions{})
	if strings.Contains(allDiagnostics(result), "negative not-found sentinel") {
		t.Fatalf("non-sentinel index must not be flagged, got:\n%s", allDiagnostics(result))
	}
}
