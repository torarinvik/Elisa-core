//go:build cgo

package backend

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func assertPartitionSubmitSafetyMode(t *testing.T, result *semantic.Result, wantUnsafe bool) {
	t.Helper()
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	shared := newSharedCodegenLookups(result)
	work := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
	for part := range 2 {
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
			g.partBody = func(name string) bool { return partitionOwner(name, 2) == part }
			g.partSynthNS = fmt.Sprintf("test.submit.%d.", part)
			g.partitionModuleDecls = work[part]
			g.hasPartitionModuleDecls = true
			g.sharedResult = true
		}, shared)
		if err != nil {
			t.Fatal(err)
		}
		if g.partUnsafeHelpers != wantUnsafe {
			t.Errorf("partition %d unsafe helper flag = %t, want %t", part, g.partUnsafeHelpers, wantUnsafe)
		}
		if !strings.Contains(g.printModule(), "__submit_capture_") {
			t.Errorf("partition %d did not generate the store-capture helper", part)
		}
		g.dispose()
	}
}

// The supported store-capturing submit shape participates in IR partitions by default.
func TestPartitionedSubmitUsesIRPartitionsByDefault(t *testing.T) {
	t.Setenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT", "")
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	for _, tc := range []struct {
		name   string
		source string
	}{{"ordinary", partitionSubmitRuntimeSource()}, {"generic", partitionSubmitGenericRuntimeSource()}} {
		t.Run(tc.name, func(t *testing.T) {
			result := parseAndAnalyzeBackendTest(t, "experimental_submit_"+tc.name+".elisa", tc.source)
			assertPartitionSubmitSafetyMode(t, result, false)
			session := NewIncrementalObjectSession(2, 32<<20)
			session.minWeight = 0
			session.EnableDiskCache(t.TempDir(), "submit-prototype", 64<<20)
			if err := session.WriteObject(result, filepath.Join(t.TempDir(), "out.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
				t.Fatal(err)
			}
			if stats := session.Stats(); stats.PartitionsEmitted != 2 || stats.SerialFallbacks != 0 {
				t.Fatalf("did not use partitioned IR: %+v", stats)
			}
		})
	}
}

func TestPartitionedSubmitCanFallBackToWholeModule(t *testing.T) {
	t.Setenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT", "0")
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	result := parseAndAnalyzeBackendTest(t, "submit_opt_out.elisa", partitionSubmitRuntimeSource())
	assertPartitionSubmitSafetyMode(t, result, true)
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "submit-opt-out", 64<<20)
	if err := session.WriteObject(result, filepath.Join(t.TempDir(), "out.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentalSubmitGenericCannotBeInterposed(t *testing.T) {
	t.Setenv("ELISACORE_EXPERIMENT_PARTITION_SUBMIT", "1")
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	t.Setenv("ELISA_PARALLEL_BODIES", "1")
	source := partitionSubmitRuntimeSource()
	filename := "submit_interpose.elisa"
	result := parseAndAnalyzeBackendTest(t, filename, source)
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatal(err)
	}
	unit := sha256.Sum256([]byte(abs))
	var symbol string
	result.BeginSharedCodegen()
	func() {
		defer result.EndSharedCodegen()
		shared := newSharedCodegenLookups(result)
		work := partitionIRDeclarationWorklists(result, shared.symbolsByNode, 2)
		for part := 0; part < 2 && symbol == ""; part++ {
			g, e := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) {
				g.partBody = func(name string) bool { return partitionOwner(name, 2) == part }
				g.partSynthNS = fmt.Sprintf("elisa.partition.%x.%d.", unit, part)
				g.partitionModuleDecls = work[part]
				g.hasPartitionModuleDecls = true
				g.sharedResult = true
			}, shared)
			if e != nil {
				t.Fatal(e)
			}
			for name, key := range g.genericSymbolKeys {
				if strings.HasPrefix(name, "pool_submit1$generic_v1$") && strings.Contains(key, g.partSynthNS) {
					symbol = name
					break
				}
			}
			g.dispose()
		}
	}()
	if symbol == "" {
		t.Fatal("capture-dependent submit specialization missing")
	}
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.EnableDiskCache(t.TempDir(), "generic-interpose", 64<<20)
	dir := t.TempDir()
	out := filepath.Join(dir, "program.o")
	if err := session.WriteObject(result, out, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 {
		t.Fatalf("partitioned IR not used: %+v", stats)
	}
	assemblerName := symbol
	if runtime.GOOS == "darwin" {
		assemblerName = "_" + assemblerName
	}
	foreign := fmt.Sprintf("#include <stdint.h>\nstruct task { uintptr_t handle; void *state; };\nstruct task evil(void) __asm__(%q);\nstruct task evil(void) { return (struct task){0,0}; }\n", assemblerName)
	c := filepath.Join(dir, "shim.c")
	if err := os.WriteFile(c, []byte(partitionSubmitRuntimeShim+"\n"+foreign), 0600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "exe")
	if data, err := exec.Command("clang", c, out, "-o", exe).CombinedOutput(); err != nil {
		t.Fatalf("link: %v %s", err, data)
	}
	if data, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("submit was interposed: %v %s", err, data)
	}
	// A source-level native symbol may also intentionally choose the generated
	// specialization name. The compiler must decline partitioning and still
	// produce the correct program, even when that declaration appears last.
	collision := source + fmt.Sprintf("\n@link_name(%q)\ndef collision() -> i64:\n    return 3\n", symbol)
	other := parseAndAnalyzeBackendTest(t, filename, collision)
	fallback := NewIncrementalObjectSession(2, 32<<20)
	fallback.minWeight = 0
	fallback.EnableDiskCache(t.TempDir(), "source-generic-collision", 64<<20)
	fallbackOut := filepath.Join(dir, "fallback.o")
	if err := fallback.WriteObject(other, fallbackOut, LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(fallback.disk.root, "*.schedule"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("collision did not trigger partition fallback: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Contains(data, []byte("partition-helper-declined-v1")) {
		t.Fatalf("missing recorded fallback: %v", err)
	}
	plain := filepath.Join(dir, "plain.c")
	if err := os.WriteFile(plain, []byte(partitionSubmitRuntimeShim), 0600); err != nil {
		t.Fatal(err)
	}
	fallbackExe := filepath.Join(dir, "fallback-exe")
	if data, err := exec.Command("clang", plain, fallbackOut, "-o", fallbackExe).CombinedOutput(); err != nil {
		t.Fatalf("fallback link: %v %s", err, data)
	}
	if data, err := exec.Command(fallbackExe).CombinedOutput(); err != nil {
		t.Fatalf("source collision changed result: %v %s", err, data)
	}
}
