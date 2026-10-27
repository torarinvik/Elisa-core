//go:build cgo

package backend

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The production-configured generic submit module has changed predecessor
// order on bitcode reload with LLVM's default writer. The optimized cache must
// preserve exactly the freshly optimized IR across a fresh-process disk hit.
func TestOptimizedModuleDiskCachePreservesUseListOrder(t *testing.T) {
	if os.Getenv("ELISA_USELIST_CACHE_CHILD") == "1" {
		root, mode := os.Getenv("ELISA_USELIST_CACHE_ROOT"), os.Getenv("ELISA_USELIST_CACHE_MODE")
		result := parseAndAnalyzeBackendTest(t, "uselist.elisa", partitionSubmitGenericRuntimeSource())
		session := NewIncrementalObjectSession(2, 32<<20)
		session.EnableDiskCache(filepath.Join(root, "cache"), "uselist-regression", 64<<20)
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
		if mode == "cold" {
			exact, err := optimizedModulePreservedBitcodeRoundTripExact(optimized)
			if err != nil {
				t.Fatal(err)
			}
			if !exact {
				if err := os.WriteFile(filepath.Join(root, "lossy"), []byte("LLVM reader changed the optimized IR"), 0600); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		hits := session.Stats().OptimizedModulesReused
		if (mode == "warm" && hits != 1) || (mode == "cold" && hits != 0) {
			t.Fatalf("%s optimized disk hits=%d", mode, hits)
		}
		if err := os.WriteFile(filepath.Join(root, mode+".ll"), []byte(optimized.printModule()), 0600); err != nil {
			t.Fatal(err)
		}
		object, shim, executable := filepath.Join(root, mode+".o"), filepath.Join(root, mode+".c"), filepath.Join(root, mode+".exe")
		if err := session.emitDirectObject(optimized, object); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shim, []byte(partitionSubmitRuntimeShim), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "clang", "-DGENERIC_FIXTURE", shim, object, "-o", executable).CombinedOutput(); err != nil {
			t.Fatalf("link %s: %v\n%s", mode, err, output)
		}
		if output, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil {
			t.Fatalf("run %s: %v\n%s", mode, err, output)
		}
		return
	}
	root := t.TempDir()
	for _, mode := range []string{"cold", "warm"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOptimizedModuleDiskCachePreservesUseListOrder$")
		cmd.Env = append(os.Environ(), "ELISA_USELIST_CACHE_CHILD=1", "ELISA_USELIST_CACHE_ROOT="+root, "ELISA_USELIST_CACHE_MODE="+mode)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s child: %v\n%s", mode, err, output)
		}
		if mode == "cold" {
			if _, err := os.Stat(filepath.Join(root, "lossy")); err == nil {
				t.Skip("LLVM reader changed optimized IR even with use-list preservation")
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	cold, err := os.ReadFile(filepath.Join(root, "cold.ll"))
	if err != nil {
		t.Fatal(err)
	}
	warm, err := os.ReadFile(filepath.Join(root, "warm.ll"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cold, warm) {
		t.Fatal("disk replay changed optimized IR")
	}
}
