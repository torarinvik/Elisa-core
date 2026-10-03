package backend

import (
	"strings"
	"testing"
)

// A string literal initializing an sview global must lower to the two-field StringView
// {data, len} constant. The bare string pointer used to be handed to LLVM as the initializer
// ("Global variable initializer type does not match global variable type").
func TestGenerateLLVMIRGlobalSViewLiteralInitializer(t *testing.T) {
	src := `global mutable gk: sview = ""
global gc: sview = "abcd"

def main() -> i64:
    can Unsafe.MutableGlobal:
        gk <- "xyz"
        return gk.len.i64() + gc.len.i64()
`
	result := parseAndAnalyzeBackendTest(t, "global_sview_init.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("sview globals initialized from literals should lower: %v", err)
	}
	for _, want := range []string{"@gk = global %StringView { ptr", "i64 0 }", "@gc = ", "i64 4 }"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in LLVM IR:\n%s", want, output)
		}
	}
}
