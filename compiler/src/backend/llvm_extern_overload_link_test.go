//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// Native extern overloads use a source-level mangled name to select the Elisa
// signature, but both declarations still name one C symbol. The linker spelling
// must survive the source mangle, otherwise a call through the second overload
// becomes an undefined `__ovl__...` import.
func TestGenerateLLVMIRPreservesNativeExternOverloadLinkName(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "native_extern_overload_link.elisa", `
extern bridge(value: u8&) -> int
extern bridge(value: cstr) -> int

def main() -> int:
    trusted Unsafe.RawExtern:
        return bridge("PATH")
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected native extern overload fixture to analyze, got:\n%s", strings.Join(errs, "\n"))
	}
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "@bridge") {
		t.Fatalf("expected native C symbol declaration/call, got:\n%s", output)
	}
	if strings.Contains(output, "@__ovl__bridge__") {
		t.Fatalf("expected source overload spelling not to leak into native linker symbol, got:\n%s", output)
	}
}
