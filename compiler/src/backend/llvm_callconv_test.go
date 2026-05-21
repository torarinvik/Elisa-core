//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRAppliesCallConvToDefinedCallbackFunction(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "defined_callback_callconv.elisa", `
@callconv(winapi)
def thread_entry(arg: void&) -> u32:
	_ = arg
	return 0u32
`)

	g, err := compileLLVMModuleWithTarget(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "i686-pc-windows-msvc")
	if err != nil {
		t.Fatalf("compileLLVMModuleWithTarget returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()
	if !strings.Contains(output, "x86_stdcallcc i32 @thread_entry") {
		t.Fatalf("expected defined callback to use x86 stdcall on 32-bit WinAPI target, got:\n%s", output)
	}
}
