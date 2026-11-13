//go:build !cgo

// Package backendstubcheck verifies the public backend when LLVM/cgo is unavailable.
package backendstubcheck

import (
	"strings"
	"testing"

	"elisacore/src/backend"
	"elisacore/src/semantic"
)

func TestUnavailableLLVMReportsActionableError(t *testing.T) {
	if _, err := backend.GenerateLLVMIR(nil); err == nil || !strings.Contains(err.Error(), "semantic result") {
		t.Fatalf("nil semantic result: got %v", err)
	}
	if _, err := backend.GenerateLLVMIR(&semantic.Result{}); err == nil || !strings.Contains(err.Error(), "requires cgo") {
		t.Fatalf("LLVM availability: got %v", err)
	}
}

func TestPackedProfileAvailableWithoutLLVM(t *testing.T) {
	abi, err := backend.ParsePackedEnumABI("dense_fixed")
	if err != nil || abi != backend.PackedEnumABIDenseFixed {
		t.Fatalf("packed ABI = %q, %v", abi, err)
	}
	if _, err := backend.ExplicitPackedLoweringProfile(abi); err != nil {
		t.Fatalf("packed profile: %v", err)
	}
}
