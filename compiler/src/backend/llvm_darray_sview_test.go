//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRDArraySViewKeepsBackingNonNullAndSupportsBorrowedReceiver(t *testing.T) {
	src := `def view(items: mutable darray[u8]&) -> sview:
    return items.as_sview()
`
	result := parseAndAnalyzeBackendTest(t, "backend_darray_sview_nonnull.elisa", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, expected := range []string{
		"darray.sview.empty.backing",
		"darray.sview.data.valid",
		"darray.sview.count",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected borrowed darray sview lowering to preserve %q, got:\n%s", expected, output)
		}
	}
}
