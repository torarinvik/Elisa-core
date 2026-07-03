//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// `return void_call()` in a void function is a legal tail-style return: the call
// runs for its side effects and the function returns void. This used to reach
// LLVMBuildRet with no value and die in the module verifier ("Found return instr
// that returns non-void in Function of void return type!").
func TestGenerateLLVMIRAcceptsReturnOfVoidCallInVoidFunction(t *testing.T) {
	src := `def helper() -> void:
    return

def v() -> void:
    return helper()
`
	result := parseAndAnalyzeBackendTest(t, "backend_return_void_call.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "call void @helper()") {
		t.Fatalf("expected v to call helper for its side effects, got:\n%s", output)
	}
	if !strings.Contains(output, "ret void") {
		t.Fatalf("expected v to lower `return helper()` to ret void, got:\n%s", output)
	}
}
