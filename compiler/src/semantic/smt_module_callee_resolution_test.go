//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func terminationLintCount(errs []string) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e, "loop `decreases` measure could not be proven") {
			n++
		}
	}
	return n
}

// A loop body calling a module-scoped function resolves the callee (by the visibility lookup
// ordinary expression analysis uses), so a by-value call is admitted in the termination capture.
func TestLoopTerminationResolvesModuleCallee(t *testing.T) {
	good := `
module M:
    public:
        def gb(left: i64, right: i64) -> i64:
            return left if left > right else right

        def lb(n: usize) -> i64:
            greatest: mutable i64 = 0
            index: mutable usize = 0
            while index < n |index, greatest|:
                decreases n - index
                greatest <- gb(greatest, 5)
                index <- index + 1
            return greatest
`
	result := analyzeContractStrict(t, "mod_callee.elisa", good)
	if n := terminationLintCount(result.Errors()); n != 0 {
		t.Fatalf("expected termination proof, got %v", result.Errors())
	}
	// SOUNDNESS: the resolved callee may write the measure variable through a mutable reference.
	bad := `
module M:
    public:
        def bump(x: mutable usize&) -> i64:
            x <- 0
            return 1

        def lb(n: usize) -> i64:
            greatest: mutable i64 = 0
            index: mutable usize = 0
            while index < n |index, greatest|:
                decreases n - index
                greatest <- bump(index)
                index <- index + 1
            return greatest
`
	result = analyzeContractStrict(t, "mod_callee_bad.elisa", bad)
	if terminationLintCount(result.Errors()) == 0 {
		t.Fatalf("bad: termination must stay unproven; got %v", result.Errors())
	}
}
