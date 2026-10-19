//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"path/filepath"
	"strings"
	"testing"
)

// Expected-red pure identity witness; does not itself prove native miscompilation.
func TestGenericSymbolDistinguishesTypeConstructorsReview(t *testing.T) {
	scalar := &semantic.BuiltinType{Name: "u8"}
	variants := []semantic.Type{scalar, &semantic.RefType{Elem: scalar}, &semantic.OptionalType{Value: scalar}}
	seen := map[string]string{}
 g := &llvmGenerator{}
	for _, variant := range variants {
		symbol, err := g.mangleGenericType("width", []semantic.Type{variant})
		if err != nil {
			t.Fatal(err)
		}
		if previous, ok := seen[symbol]; ok {
			t.Fatalf("type arguments %q and %q share specialization symbol %q", previous, variant.String(), symbol)
		}
		seen[symbol] = variant.String()
	}
}

// Candidate native witness; a fixture/typechecking failure is not a wrong-result proof.
func TestGenericSymbolNativeTypeWidthReview(t *testing.T) {
	const prefix = `@inline(never)
def width[T]() -> usize:
    return size_of[T]()

`
	for _, referenceFirst := range []bool{false, true} {
		name := "scalar-first"
		calls := "    scalar: usize = width[u8]()\n    reference: usize = width[u8&]()\n"
		if referenceFirst {
			name = "reference-first"
			calls = "    reference: usize = width[u8&]()\n    scalar: usize = width[u8]()\n"
		}
		t.Run(name, func(t *testing.T) {
			source := prefix + "def main() -> i64:\n" + calls + `    if scalar == size_of[u8]() and reference == size_of[u8&]():
        return 42
    return 1
`
			result := parseAndAnalyzeBackendTest(t, "generic_type_width.elisa", source)
			ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
			if err != nil {
				t.Fatalf("native witness did not lower: %v", err)
			}
			// Retain textual IR for diagnosis: two type-specific bodies are required.
			t.Logf("width definitions/calls:\n%s", ir)
			if !strings.Contains(ir, "width") {
				t.Fatal("fixture contains no width specialization")
			}
			object := filepath.Join(t.TempDir(), "width.o")
			if err := WriteLLVMObjectFileWithOptions(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
				t.Fatal(err)
			}
			if got := incrementalObjectNativeStatus(t, object); got != 42 {
				t.Fatalf("distinct type arguments returned inconsistent sizes: native=%d, want42", got)
			}
		})
	}
}
