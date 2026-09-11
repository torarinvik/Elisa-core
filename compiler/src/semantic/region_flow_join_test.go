package semantic

import (
	"strings"
	"testing"
)

// A branch-local destroy must survive the control-flow join. Otherwise a region
// that is definitely dead after `if true` can be reused by the caller.
func TestDestroyedRegionFlowJoinsPreserveDeadState(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "destroyed_region_after_if.elisa", `def f() -> i64:
    region r(4096)
    if true:
        destroy r
        region r(4096):
            new[r] 1
    new[r] 2
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot allocate from destroyed region") {
		t.Fatalf("expected the joined flow to preserve the destroyed region state; got: %s", all)
	}
}

// A conditional destroy without a matching live/destroyed path discipline is
// rejected at the join instead of silently choosing the live state.
func TestRegionDestroyMustAgreeAcrossFallthroughPaths(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "conditional_destroy_join.elisa", `def f(flag: bool) -> i64:
    region r(4096)
    if flag:
        destroy r
    new[r] 2
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "destroyed on some control-flow paths") {
		t.Fatalf("expected mixed region ownership at the join to be rejected; got: %s", all)
	}
}
