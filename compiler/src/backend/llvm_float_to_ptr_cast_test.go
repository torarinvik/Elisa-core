//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersFloatToPointerCastViaInteger(t *testing.T) {
	src := `
def cast_ptr(v: f64) -> heap u8& can[Unsafe.PointerCast]:
    return v.cast[heap u8&]
`
	result := parseAndAnalyzeBackendTest(t, "backend_float_to_ptr_cast.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "fptoui double") {
		t.Fatalf("expected float-to-uintptr lowering via fptoui, got:\n%s", output)
	}
	if !strings.Contains(output, "inttoptr i64") {
		t.Fatalf("expected integer-to-pointer lowering after fptoui, got:\n%s", output)
	}
	if strings.Contains(output, "inttoptr double") {
		t.Fatalf("unexpected direct inttoptr from floating-point operand, got:\n%s", output)
	}
}
