//go:build cgo

package backend

import (
	"path/filepath"
	"testing"
)

// Separate candidate witness: fixing function lookup alone must not leave named
// LLVM generic struct bodies aliased. Unrun; no layout miscompile claim yet.
func TestGenericStructNativeLayoutIdentityReview(t *testing.T) {
	const prefix = `struct Box[T] layout(c):
    value: T

`
	for _, referenceFirst := range []bool{false, true} {
		name := "scalar-first"
		queries := "    scalar_size: usize = size_of[Box[u8]]()\n    reference_size: usize = size_of[Box[u8&]]()\n"
		if referenceFirst {
			name = "reference-first"
			queries = "    reference_size: usize = size_of[Box[u8&]]()\n    scalar_size: usize = size_of[Box[u8]]()\n"
		}
		t.Run(name, func(t *testing.T) {
			source := prefix + "def main() -> i64:\n" + queries + `    if scalar_size == size_of[u8]() and reference_size == size_of[u8&]():
        return 42
    return 1
`
			result := parseAndAnalyzeBackendTest(t, "generic_struct_layout.elisa", source)
			ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
			if err != nil {
				t.Fatalf("layout witness did not lower: %v", err)
			}
			t.Logf("layout witness IR:\n%s", ir)
			object := filepath.Join(t.TempDir(), "layout.o")
			if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
				t.Fatal(err)
			}
			if got := incrementalObjectNativeStatus(t, object); got != 42 {
				t.Fatalf("distinct generic layouts produced native=%d, want42", got)
			}
		})
	}
}
