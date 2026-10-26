//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every revision runs in a new process: there are no surviving LLVM handles or
// session entries. Compare optimized IR and linked native behavior to cold O3.
func TestOptimizedModuleDiskCacheAcrossProcesses(t *testing.T) {
	if os.Getenv("ELISA_TEST_OPTIMIZED_CACHE_CHILD") == "1" {
		root := os.Getenv("ELISA_TEST_OPTIMIZED_CACHE_ROOT")
		revision := os.Getenv("ELISA_TEST_OPTIMIZED_CACHE_REVISION")
		t.Setenv(TargetCPUEnv, "native")
		if revision == "generic" {
			t.Setenv(TargetCPUEnv, "generic")
		}
		debug := revision == "debug" || revision == "debug-lines"
		source := incrementalObjectFixture(32)
		want := 54
		switch revision {
		case "lines", "debug-lines":
			source = "\n\n" + source
		case "body":
			source = strings.Replace(source, "return value + 1\n", "return value + 2\n", 1)
			want = 55
		}
		level := OptimizationLevel3
		if os.Getenv("ELISA_TEST_OPTIMIZED_CACHE_REVISION") == "O2" {
			level = OptimizationLevel2
		}
		result := parseAndAnalyzeBackendTest(t, "optimized_cache.elisa", source)
		session := NewIncrementalObjectSession(4, 32<<20)
		session.minWeight = 0
		identity := "test-executable"
		if revision == "compiler" {
			identity += "-changed"
		}
		session.EnableDiskCache(root, identity, 64<<20)
		// Compare the exact optimized IR, including its metadata, to a fresh pipeline.
		compileInput := func() (*llvmGenerator, error) {
			return compileLLVMModuleConfigured(result, level, DefaultPackedLoweringProfile(), "", debug, false, func(g *llvmGenerator) {
				if !debug {
					discardLLVMValueNames(g.context)
				}
			})
		}
		g, err := compileInput()
		if err != nil {
			t.Fatal(err)
		}
		defer g.dispose()
		if err := g.ensureTargetMachine(); err != nil {
			t.Fatal(err)
		}
		optimized, err := session.optimizeObjectModule(g, level)
		if err != nil {
			t.Fatal(err)
		}
		if optimized != g {
			defer optimized.dispose()
		}
		hit := session.Stats().OptimizedModulesReused == 1
		if hit != (os.Getenv("ELISA_TEST_OPTIMIZED_CACHE_HIT") == "1") {
			t.Fatalf("unexpected cache hit=%v", hit)
		}
		cold, err := compileInput()
		if err != nil {
			t.Fatal(err)
		}
		defer cold.dispose()
		if err := cold.optimizeModule(level); err != nil {
			t.Fatal(err)
		}
		if optimized.printModule() != cold.printModule() {
			t.Fatal("cached optimized IR differs from fresh pipeline")
		}
		object := filepath.Join(t.TempDir(), "cached.o")
		beforeWriteReuse := session.Stats().OptimizedModulesReused
		if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: level, DebugInfo: debug}); err != nil {
			t.Fatal(err)
		}
		if reused := session.Stats().OptimizedModulesReused - beforeWriteReuse; reused != 1 {
			t.Fatalf("WriteObject reused %d optimized modules, want exactly one", reused)
		}
		if got := incrementalObjectNativeStatus(t, object); got != want {
			t.Fatalf("native=%d want=%d", got, want)
		}
		return
	}
	root := t.TempDir()
	for _, revision := range []struct {
		name string
		hit  bool
	}{{"base", false}, {"repeat", true}, {"lines", true}, {"body", false}, {"body", true}, {"base", true}, {"O2", false}, {"O2", true}, {"compiler", false}, {"compiler", true}, {"generic", false}, {"generic", true}, {"debug", false}, {"debug", true}, {"debug-lines", false}, {"debug-lines", true}} {
		t.Run(revision.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestOptimizedModuleDiskCacheAcrossProcesses$", "-test.timeout=30s")
			hit := "0"
			if revision.hit {
				hit = "1"
			}
			cmd.Env = append(os.Environ(), "ELISA_TEST_OPTIMIZED_CACHE_CHILD=1", "ELISA_TEST_OPTIMIZED_CACHE_ROOT="+root, "ELISA_TEST_OPTIMIZED_CACHE_REVISION="+revision.name, "ELISA_TEST_OPTIMIZED_CACHE_HIT="+hit)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		})
	}
}

func TestOptimizedModuleDiskCacheRechecksPerformanceWarnings(t *testing.T) {
	source := `@inline(never)
def leaf(x: i64) -> i64:
    return x * x

@inline(never)
def fold(n: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<n:
        total <- total + leaf(i)
    return total

def main() -> i64:
    return fold(5000)
`
	result := parseAndAnalyzeBackendTest(t, "cached_warnings.elisa", source)
	// Model an expected-to-vectorize loop, as emitted for comprehensions and
	// Vectorizes obligations. The opaque call prevents actual vectorization.
	loop := result.ActiveFile().Decls[1].(*ast.FuncDecl).Body[1].(*ast.ForStmt)
	loop.AutovecExpected = true
	loop.AutovecReason = "loop"
	session := NewIncrementalObjectSession(2, 8<<20)
	session.EnableDiskCache(t.TempDir(), "test-executable", 16<<20)
	for index := 0; index < 2; index++ {
		result.EnforcePerfLints = index == 1
		g, err := compileLLVMModule(result, OptimizationLevel3, DefaultPackedLoweringProfile())
		if err != nil {
			t.Fatal(err)
		}
		if err := g.ensureTargetMachine(); err != nil {
			g.dispose()
			t.Fatal(err)
		}
		optimized, err := session.optimizeObjectModule(g, OptimizationLevel3)
		if err != nil {
			g.dispose()
			t.Fatal(err)
		}
		warnings := append([]string(nil), optimized.PerfWarnings()...)
		if optimized != g {
			optimized.dispose()
		}
		g.dispose()
		if len(warnings) == 0 {
			t.Fatal("fixture did not exercise scalar fallback warnings")
		}
		severity := "warning [-Wperf]"
		if index == 1 {
			severity = "error [-Wperf]"
		}
		for _, warning := range warnings {
			if !strings.Contains(warning, severity) {
				t.Fatalf("stale performance warning: %s", warning)
			}
		}
	}
	if session.Stats().OptimizedModulesReused != 1 {
		t.Fatal("warning enforcement change did not reuse exact optimized input")
	}
}
