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
