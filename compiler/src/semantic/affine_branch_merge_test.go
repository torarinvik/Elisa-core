package semantic

import (
	"strings"
	"testing"
)

// Seam 3 (branch-join meet) of the region/affine soundness work.
//
// A linear value consumed on EVERY arm of an if/else must be seen as consumed
// after the join. The earlier flow code seeded the post-if affine state with the
// pre-branch (entry) state and only ever *added* liveness when merging branches,
// so "consumed on all arms" was mis-reported as "must be consumed before scope
// exit" — a false positive that pushed real code toward Unsafe escapes.
func TestAffineConsumedOnAllBranchesIsClean(t *testing.T) {
	analyzeTreeTestSource(t, "affine_all_branches.elisa", `linear struct Guard:
    active: bool

def make() -> Guard:
    return Guard{active: true}

def release(g: Guard) -> void:
    _ = move g

def probe(c: bool) -> void:
    g: Guard = make()
    if c:
        release(move g)
    else:
        release(move g)
`)
}

// Conversely, consuming on only SOME arms must still error: the not-consumed
// path leaks the linear value. This is the soundness side that must not regress
// when the false positive above is fixed.
func TestAffinePartialConsumeStillErrors(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "affine_partial.elisa", `linear struct Guard:
    active: bool

def make() -> Guard:
    return Guard{active: true}

def release(g: Guard) -> void:
    _ = move g

def probe(c: bool) -> void:
    g: Guard = make()
    if c:
        release(move g)
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "must be consumed before scope exit") {
		t.Fatalf("expected partial consume to error with must-be-consumed; got: %s", all)
	}
}
