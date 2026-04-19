//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersMembershipExpr(t *testing.T) {
	src := `def keep(value: i64) -> bool:
    return value in [1, 2, 3]
`

	result := parseAndAnalyzeBackendTest(t, "backend_membership.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i64 ", "membership.next.0", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected membership lowering to include %q, got:\n%s", check, output)
		}
	}
}
