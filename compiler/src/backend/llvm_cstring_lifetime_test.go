//go:build cgo

package backend

import "testing"

// LLVM keeps some C API names alive until the containing module is disposed. The
// backend must release that ownership at the generator boundary, otherwise a
// sequence of self-hosting modules retains every generated instruction name.
func TestModuleCStringLifetimeEndsWithGenerator(t *testing.T) {
	for iteration := 0; iteration < 3; iteration++ {
		result := parseAndAnalyzeBackendTest(t, "cstring_lifetime.elisa", `
def step(value: i64) -> i64:
		first: i64 = value + 1
		second: i64 = first * 2
		return second

def main() -> i64:
	return step(20)
`)
		g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
		if err != nil {
			t.Fatalf("compileLLVMModule returned error on iteration %d: %v", iteration, err)
		}
		g.dispose()

		moduleCStringState.Lock()
		users := moduleCStringState.users
		owned := len(moduleCStringState.owned)
		byName := len(moduleCStringState.byName)
		moduleCStringState.Unlock()
		if users != 0 || owned != 0 || byName != 0 {
			t.Fatalf("module C-string ownership survived dispose on iteration %d: users=%d owned=%d names=%d", iteration, users, owned, byName)
		}
	}
}
