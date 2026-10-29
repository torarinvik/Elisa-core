//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The -O3 partitioned path lowers each partition in its own module, so a submit
// capture helper must be defined beside its only caller. This checks that the
// partitioned object runs the same captured-store behavior as a serial whole-module
// build at -O3, and that the partitioned build did not fall back to serial lowering.
func TestPartitionedStoreCapturingSubmitRuntimeO3(t *testing.T) {
	if os.Getenv("ELISA_TEST_SUBMIT_O3_CHILD") == "1" {
		t.Setenv("ELISACORE_PARALLEL_IR", "1")
		t.Setenv("ELISACORE_PARALLEL_OPT", "1")
		dir := t.TempDir()
		serialObject := filepath.Join(dir, "serial.o")
		serial := parseAndAnalyzeBackendTest(t, "submit_o3_serial.elisa", partitionSubmitRuntimeSource())
		if err := WriteLLVMObjectFileWithOptions(serial, serialObject, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: true}); err != nil {
			t.Fatal(err)
		}
		partitionedObject := filepath.Join(dir, "partitioned.o")
		partitioned := parseAndAnalyzeBackendTest(t, "submit_o3_partitioned.elisa", partitionSubmitRuntimeSource())
		session := NewIncrementalObjectSession(2, 32<<20)
		session.minWeight = 0
		options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, PackedProfile: DefaultPackedLoweringProfile()}
		disposalScope := newLLVMDisposalScope(1)
		done, err := session.tryPartitionedObject(partitioned, partitionedObject, options, disposalScope)
		disposalScope.wait()
		if err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Fatal("O3 submit fixture declined the partitioned IR path")
		}
		shim := filepath.Join(dir, "shim.c")
		if err := os.WriteFile(shim, []byte(partitionSubmitRuntimeShim), 0600); err != nil {
			t.Fatal(err)
		}
		for name, object := range map[string]string{"serial": serialObject, "partitioned": partitionedObject} {
			executable := filepath.Join(dir, name)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if output, err := exec.CommandContext(ctx, "clang", shim, object, "-o", executable).CombinedOutput(); err != nil {
				cancel()
				t.Fatalf("link %s submit fixture: %v\n%s", name, err, output)
			}
			if output, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
				cancel()
				t.Fatalf("%s -O3 captured store values or execution differ: %v\n%s", name, err, output)
			}
			cancel()
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPartitionedStoreCapturingSubmitRuntimeO3$")
	cmd.Env = append(os.Environ(), "ELISA_TEST_SUBMIT_O3_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("O3 submit runtime: %v\n%s", err, output)
	}
}
