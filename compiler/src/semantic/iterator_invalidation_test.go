package semantic

import (
	"strings"
	"testing"
)

// Iterator invalidation (the one genuine memory-safety gap vs Rust found in the audit):
// mutating a relocatable darray while iterating it would move its buffer out from under the
// live loop. It is rejected at the universal relocating-mutation chokepoint
// (invalidateStorageViewsForSource), reusing the same machinery that invalidates interior
// references after a push.
func TestAnalyzeRejectsMutationOfIteratedDArray(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_mutate.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        for v in xs:
            xs.push(v)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "while it is being iterated") {
		t.Fatalf("expected an iterator-invalidation rejection, got:\n%s", all)
	}
}

// Pushing into a DIFFERENT darray while iterating is fine — accumulation into a separate
// collection is the normal pattern and never touches the iterand's buffer.
func TestAnalyzeAllowsPushIntoOtherDArrayDuringIteration(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_other.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(1)
        ys: mutable darray[i64] = []
        for v in xs:
            ys.push(v)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected pushing into a different darray during iteration to be allowed, got:\n%s", strings.Join(errs, "\n"))
	}
}

// More precise than Rust: a stable-backed darray (reserve_commit / fixed) never relocates
// its buffer on growth, so mutating it during iteration is provably safe and is allowed.
// Elisa rejects only what is actually unsafe (relocation), not all mutation-during-iteration.
func TestAnalyzeAllowsMutationOfStableBackedIteratedDArray(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "iter_stable.elisa", `def build() -> void:
    can Memory.Allocate, Abort.Panic:
        region buf(4096) using reserve_commit:
            xs: mutable darray[i64] @buf = []
            xs.push(1)
            for v in xs:
                xs.push(v)
                return
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected mutation of a stable-backed iterand to be allowed (no relocation), got:\n%s", strings.Join(errs, "\n"))
	}
}
