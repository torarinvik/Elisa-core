//go:build cgo

package backend

import (
	"path/filepath"
	"testing"
)

// LLVM's module identifier must not make code generation depend on how the
// source path was spelled.  In particular, absolute paths used by build
// scripts must produce the same optimized IR as equivalent relative paths.
// This guards the stage0 regression where the module header alone changed,
// but LLVM's Darwin/AArch64 backend then selected different machine code.
func TestModuleIdentifierIsStableAcrossSourcePathForms(t *testing.T) {
	src := `def increment(value: i64) -> i64:
    return value + 1
`

	absolutePath := filepath.Join(t.TempDir(), "module_path_stability.elisa")
	relative := parseAndAnalyzeBackendTest(t, "module_path_stability.elisa", src)
	absolute := parseAndAnalyzeBackendTest(t, absolutePath, src)

	relativeIR, err := GenerateLLVMIRWithOpt(relative, OptimizationLevel2)
	if err != nil {
		t.Fatalf("relative-path IR generation failed: %v", err)
	}
	absoluteIR, err := GenerateLLVMIRWithOpt(absolute, OptimizationLevel2)
	if err != nil {
		t.Fatalf("absolute-path IR generation failed: %v", err)
	}
	if relativeIR != absoluteIR {
		t.Fatalf("module path changed optimized IR; relative and absolute source paths must be stable")
	}
}
