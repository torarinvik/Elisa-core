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

func TestResolvedTargetIdentityMatchesTargetSelection(t *testing.T) {
	t.Setenv(TargetCPUEnv, "")
	native, err := ResolveTargetIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if native.Triple == "" || native.CPU == "" {
		t.Fatalf("unresolved native target: %+v", native)
	}
	cross, err := ResolveTargetIdentity("x86_64-unknown-linux-gnu")
	if err != nil {
		t.Fatal(err)
	}
	if cross.Triple != "x86_64-unknown-linux-gnu" || cross.CPU != "" || cross.Features != "" {
		t.Fatalf("cross target inherited host settings: %+v", cross)
	}
	t.Setenv(TargetCPUEnv, "native")
	tuned, err := ResolveTargetIdentity(cross.Triple)
	if err != nil {
		t.Fatal(err)
	}
	if tuned.CPU != native.CPU || tuned.Features != native.Features {
		t.Fatalf("explicit native CPU differs: %+v / %+v", tuned, native)
	}
	t.Setenv(TargetCPUEnv, "generic")
	generic, err := ResolveTargetIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if generic.Triple != native.Triple || generic.CPU != "generic" || generic.Features != "" {
		t.Fatalf("generic inherited host features: %+v", generic)
	}
}
