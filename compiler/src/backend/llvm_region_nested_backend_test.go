//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRThreadsRegionThroughNestedStructReference(t *testing.T) {
	src := `struct Scratch:
    values: mutable darray[i64]

struct Workspace:
    scratch: mutable Scratch

def prepare[@r](scratch: mutable Scratch& @r) -> i64:
    return 42

def verify[@r](workspace: mutable Workspace& @r) -> i64:
    return prepare(&workspace.scratch)
`
	result := parseAndAnalyzeBackendTest(t, "nested_struct_reference_region.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("nested region-carrying struct references should lower: %v", err)
	}
	if strings.Contains(output, "<null operand!") {
		t.Fatalf("nested region-carrying call must lower its hidden Arena argument from the parent reference, got invalid LLVM IR:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @prepare") {
		t.Fatalf("expected nested field call to lower to prepare with a concrete region argument:\n%s", output)
	}
}
