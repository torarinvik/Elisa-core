package semantic

import (
	"strings"
	"testing"
)

// A ghost function declared in `extend M:` calls its siblings through the resolver-qualified name
// `M.f`. Ghost validation runs outside any function scope, so the callee must still resolve through
// the global scope; otherwise every recursive module ghost was rejected as not pure and total.
func TestGhostModuleQualifiedRecursiveCallIsPureAndTotal(t *testing.T) {
	source := `module Backend:
    public:
        const PRIME: u64 = 1099511628211u64

extend Backend:
    private:
        ghost def suffix(owner: sview, index: usize, hash: u64) -> u64:
            requires index <= owner.len
            decreases owner.len - index
            return hash if index == owner.len
            return suffix(owner, index + 1.usize(), (hash ^ owner[index].u64()) * PRIME)

        ghost def outer(owner: sview) -> u64:
            return suffix(owner, 0.usize(), PRIME)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_module.elisa", source, ensureDiagOpts)
	if diags := allDiagnostics(result); strings.Contains(diags, "ghost function") {
		t.Fatalf("module ghost rejected: %s", diags)
	}
}

// Resolving the qualified callee must not make an impure callee acceptable: a module ghost that
// calls a non-ghost function with a loop is still rejected.
func TestGhostModuleQualifiedCallToImpureFunctionStillRejected(t *testing.T) {
	source := `extend Backend:
    private:
        def looping(n: i64) -> i64:
            total: mutable i64 = 0
            for i in 0..<n |total|:
                total += i
            return total

        ghost def wrapper(n: i64) -> i64:
            return looping(n)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_module_impure.elisa", source, ensureDiagOpts)
	if diags := allDiagnostics(result); !strings.Contains(diags, `ghost function "wrapper" must be pure and total`) {
		t.Fatalf("impure module ghost accepted: %s", diags)
	}
}
