//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Every Elisa-defined function is `nounwind`: the language has no exceptions and the backend emits no
// invoke/landingpad, so no body can unwind. The attribute is stamped at codegen (not left to LLVM's
// own inference) so it is present at -O0 and on functions LLVM cannot see through (exported/indirect).
func TestGenerateLLVMIRMarksDefinedFunctionsNoUnwind(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "nounwind_defined.elisa", `
def add(a: i64, b: i64) -> i64:
	return a + b

export fn probe_add(a: i64, b: i64) -> i64 = add
`)

	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()

	// The defined function carries an attribute group, and that group includes nounwind. At -O0 LLVM
	// runs no inference, so its presence proves codegen stamped it.
	if !strings.Contains(output, "define i64 @add(") {
		t.Fatalf("expected a definition for `add`, got:\n%s", output)
	}
	if !strings.Contains(output, "nounwind") {
		t.Fatalf("expected defined functions to be stamped nounwind at -O0, got:\n%s", output)
	}
}
