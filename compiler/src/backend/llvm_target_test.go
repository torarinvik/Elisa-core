//go:build cgo

package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLLVMObjectFileWithOptO0InitializesTargetMachineForEmission(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "emit_object_o0.elisa", `def main() -> int:
	return 0
`)
	outputPath := filepath.Join(t.TempDir(), "emit_object_o0.o")
	if err := WriteLLVMObjectFileWithOptAndPackedLoweringProfile(result, outputPath, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOptAndPackedLoweringProfile returned error: %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("expected object output %s: %v", outputPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected object output %s to be non-empty", outputPath)
	}
}

func TestWriteLLVMObjectFileForExplicitAArch64Target(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "emit_object_aarch64_machine_cse.elisa", `def main() -> int:
	return 0
`)
	outputPath := filepath.Join(t.TempDir(), "emit_object_aarch64_machine_cse.o")
	if err := WriteLLVMObjectFileWithOptions(result, outputPath, LLVMObjectEmitOptions{
		OptLevel:      OptimizationLevel2,
		PackedProfile: DefaultPackedLoweringProfile(),
		TargetTriple:  "arm64-apple-darwin",
	}); err != nil {
		t.Fatalf("WriteLLVMObjectFileWithOptions returned error: %v", err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("expected object output %s: %v", outputPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected object output %s to be non-empty", outputPath)
	}
}
