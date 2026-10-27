//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
)

// The first process caches an optimized loop with a scalar fallback warning.
// A fresh second process must reuse the cached module while applying its own
// stricter warning policy, rather than retaining the first process's severity.
func TestOptimizedModuleCacheHitRechecksWarningSeverityAcrossProcesses(t *testing.T) {
	if os.Getenv("ELISA_WARNING_HIT_CHILD") == "1" {
		source := `@inline(never)
def leaf(x: i64) -> i64:
    return x * x

def fold(n: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<n:
        total <- total + leaf(i)
    return total

def main() -> i64:
    return fold(5000)
`
		result := parseAndAnalyzeBackendTest(t, "warning_probe.elisa", source)
		loop := result.ActiveFile().Decls[1].(*ast.FuncDecl).Body[1].(*ast.ForStmt)
		loop.AutovecExpected, loop.AutovecReason = true, "loop"
		strict := os.Getenv("ELISA_WARNING_HIT_STRICT") == "1"
		result.EnforcePerfLints = strict
		session := NewIncrementalObjectSession(2, 8<<20)
		session.EnableDiskCache(filepath.Join(os.Getenv("ELISA_WARNING_HIT_DIR"), "cache"), "warning-hit-v1", 16<<20)
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel3, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { discardLLVMValueNames(g.context) })
		if err != nil {
			t.Fatal(err)
		}
		defer g.dispose()
		if err := g.ensureTargetMachine(); err != nil {
			t.Fatal(err)
		}
		optimized, err := session.optimizeObjectModule(g, OptimizationLevel3)
		if err != nil {
			t.Fatal(err)
		}
		if optimized != g {
			defer optimized.dispose()
		}
		hit := session.Stats().OptimizedModulesReused == 1
		if hit != strict {
			t.Fatalf("cache hit=%v, strict=%v", hit, strict)
		}
		expected := "warning [-Wperf]"
		if strict {
			expected = "error [-Wperf]"
		}
		warnings := optimized.PerfWarnings()
		if len(warnings) == 0 {
			t.Fatal("expected a non-vectorized loop warning")
		}
		for _, warning := range warnings {
			if !strings.Contains(warning, expected) {
				t.Fatalf("warning severity stale: %s", warning)
			}
		}
		return
	}
	root := t.TempDir()
	for _, strict := range []string{"0", "1"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOptimizedModuleCacheHitRechecksWarningSeverityAcrossProcesses$")
		cmd.Env = append(os.Environ(), "ELISA_WARNING_HIT_CHILD=1", "ELISA_WARNING_HIT_DIR="+root, "ELISA_WARNING_HIT_STRICT="+strict)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("strict=%s: %v\n%s", strict, err, out)
		}
	}
}
