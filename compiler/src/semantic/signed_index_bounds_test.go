package semantic

import (
	"strings"
	"testing"
)

// A SIGNED index proven only by an upper bound (i < count) is NOT safe: a
// negative value also satisfies i < count and indexes out of bounds. It must now
// require the Unsafe.UncheckedIndex opt-out.
func TestSignedIndexUpperBoundOnlyRequiresUncheckedIndex(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "signed_index_upper_only.elisa", `def read_at(xs: darray[u8], i: int) -> u8:
    if i < xs.count:
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(all, "unchecked index requires") {
		t.Fatalf("expected signed upper-bound-only index to require UncheckedIndex, got:\n%s", all)
	}
}

// A signed index with BOTH a non-negativity guard and an upper bound is safe.
func TestSignedIndexWithNonNegGuardIsProven(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "signed_index_nonneg_guard.elisa", `def read_at(xs: darray[u8], i: int) -> u8:
    if i >= 0 and i < xs.count:
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "unchecked index requires") {
		t.Fatalf("expected i>=0 && i<count guard to prove a signed index, got:\n%s", all)
	}
}

// An UNSIGNED index proven by an upper bound is non-negative by construction and
// remains proven (no regression for the common case).
func TestUnsignedIndexUpperBoundIsProven(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "unsigned_index_upper.elisa", `def read_at(xs: darray[u8], i: usize) -> u8:
    if i < xs.count:
        return xs[i]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := strings.Join(result.Warnings(), "\n")
	if strings.Contains(all, "unchecked index requires") {
		t.Fatalf("expected unsigned upper-bound index to stay proven, got:\n%s", all)
	}
}
