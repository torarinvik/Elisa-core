//go:build cgo

package backend

import "testing"

func TestGenerateLLVMIRRejectsNilSemanticResult(t *testing.T) {
	if _, err := GenerateLLVMIR(nil); err == nil {
		t.Fatal("GenerateLLVMIR(nil) unexpectedly succeeded")
	}
}
