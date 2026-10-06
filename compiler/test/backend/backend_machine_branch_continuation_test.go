package backend_test

import (
	"elisacore/src/backend"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMachineBranchContinuationOptimizesWithoutCompletionFlag(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate backend test source")
	}
	fixture := filepath.Join(filepath.Dir(testFile), "..", "fixtures", "machine_transition", "branch_transitions_dynamic.elisa")
	source, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	result := parseAndAnalyze(t, filepath.Base(fixture), string(source))
	output, err := backend.GenerateLLVMIRWithOpt(result, backend.OptimizationLevel2)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt(O2) returned error: %v", err)
	}
	if strings.Contains(output, "__machine_arm_done") {
		t.Fatalf("optimized LLVM IR contains a machine completion flag:\n%s", output)
	}
	if strings.Contains(output, "alloca i1") {
		t.Fatalf("optimized LLVM IR contains a boolean stack slot:\n%s", output)
	}
	// Keep the input-dependent control flow live so the check cannot pass only
	// because the optimizer folded the transition test to a constant.
	if !strings.Contains(output, "icmp eq i64") || !strings.Contains(output, "br i1") {
		t.Fatalf("optimized LLVM IR lost the runtime-driven branch:\n%s", output)
	}
}
