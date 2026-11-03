//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestOmittedArrayInitializerProducesZeroValue(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "omitted_array.elisa", `
def kernel(index: usize) -> u8:
    values: mutable u8[8]
    return values[index % 8]
export fn omitted_array_kernel(index: usize) -> u8 = kernel
`)
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		ir, err := GenerateLLVMIRWithOpt(result, level)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(ir, "ret i8 undef") || strings.Contains(ir, "ret i8 poison") {
			t.Fatalf("omitted initializer returned an undefined value at %v:\n%s", level, ir)
		}
		if !strings.Contains(ir, "zeroinitializer") && !strings.Contains(ir, "ret i8 0") && !strings.Contains(ir, "store i64 0, ptr %values") {
			t.Fatalf("missing zero initialization at %v:\n%s", level, ir)
		}
	}
}
