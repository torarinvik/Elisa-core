package backend

import (
	"strings"
	"testing"
)

func TestWhileTrueExitIsNotAFallThrough(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "while_true_exit.elisa", `def find(n: i64) -> i64:
    current: mutable i64 = 0
    while true |current|:
        return current if current * current >= n
        current <- current + 1

def main() -> i32:
    return find(50).i32()
`)
	if _, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0); err != nil {
		t.Fatalf("a body ending in a `while true` that leaves only by return was rejected: %v", err)
	}
}

func TestMissingReturnStillFallsThrough(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "missing_return.elisa", `def pick(n: i64) -> i64:
    match n:
        0:
            return 1 if n == 0
        _:
            return 2

def main() -> i32:
    return pick(0).i32()
`)
	_, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err == nil || !strings.Contains(err.Error(), "may fall through") {
		t.Fatalf("expected a fall-through error for an arm that can end without returning, got %v", err)
	}
}

func TestModuleRegionSpecializedCallLowers(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "module_region_call.elisa", `module M:
    public:
        def first[@r](xs: darray[u8]& @r) -> u8:
            return 0 if xs.count == 0
            return xs[0]

        def via[@q](xs: darray[u8]& @q) -> u8:
            return first[q](xs)

def main() -> i32:
    xs: darray[u8] = [7, 8]
    return M::via(xs).i32()
`)
	if _, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0); err != nil {
		t.Fatalf("a module-scoped region-specialized call did not lower: %v", err)
	}
}
