package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/backend"
)

func TestBuildObjectCacheBypassesExplicitMerger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	linker := filepath.Join(dir, "ld")
	t.Setenv("ELISA_LD", linker)
	if err := os.WriteFile(linker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var source strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&source, "def f%d() -> i64:\n    return %d\n", i, i)
	}
	program := &loadedProgram{
		filename: filepath.Join(dir, "unit.elisa"),
		source:   []byte(source.String()),
	}
	baseOptions := cliOptions{
		emit:        emitObject,
		filename:    program.filename,
		hasOptLevel: true,
	}
	checkCacheability := func(options cliOptions, wantCacheable bool) {
		t.Helper()
		_, cacheable := buildCacheObjectArtifactForProgram(options, program)
		if cacheable != wantCacheable {
			t.Fatalf("object-cache admission = %t, want %t", cacheable, wantCacheable)
		}
	}

	cases := []struct {
		name, jobs, partitionCache, nativeFragments, parallelIR, parallelOpt string
		optLevel                                                          backend.OptimizationLevel
		wantCacheable                                                     bool
	}{
		{"native fragments", "1", "1", "1", "0", "0", backend.OptimizationLevel3, false},
		{"parallel opt whitespace", "1", "0", "0", "0", " 1 ", backend.OptimizationLevel3, false},
		{"ordinary parallel optimized", "2", "0", "0", "0", "0", backend.OptimizationLevel3, false},
		{"O0 partitioned IR", "1", "0", "0", "1", "0", backend.OptimizationLevel0, false},
		{"O0 ordinary parallel", "2", "0", "0", "0", "0", backend.OptimizationLevel0, false},
		{"serial optimized", "1", "0", "0", "0", "0", backend.OptimizationLevel3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ELISACORE_CODEGEN_JOBS", tc.jobs)
			t.Setenv("ELISACORE_PARTITION_CACHE", tc.partitionCache)
			t.Setenv("ELISACORE_NATIVE_FRAGMENTS", tc.nativeFragments)
			t.Setenv("ELISACORE_PARALLEL_IR", tc.parallelIR)
			t.Setenv("ELISACORE_PARALLEL_OPT", tc.parallelOpt)
			options := baseOptions
			options.optLevel = tc.optLevel
			checkCacheability(options, tc.wantCacheable)
		})
	}

	t.Run("non-system default linker", func(t *testing.T) {
		linkerDir := filepath.Join(dir, "path-linker")
		if err := os.Mkdir(linkerDir, 0o700); err != nil {
			t.Fatal(err)
		}
		pathLinker := filepath.Join(linkerDir, "ld")
		nativeMagic := []byte("\x7fELF")
		if runtime.GOOS == "darwin" {
			nativeMagic = []byte("\xcf\xfa\xed\xfe")
		}
		if err := os.WriteFile(pathLinker, nativeMagic, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ELISA_LD", "")
		t.Setenv("PATH", linkerDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("ELISACORE_CODEGEN_JOBS", "2")
		t.Setenv("ELISACORE_PARTITION_CACHE", "0")
		t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "0")
		t.Setenv("ELISACORE_PARALLEL_IR", "0")
		t.Setenv("ELISACORE_PARALLEL_OPT", "0")
		options := baseOptions
		options.optLevel = backend.OptimizationLevel3
		checkCacheability(options, false)
	})
}

func TestBuildCacheMayUseObjectMergerTrimsParallelOpt(t *testing.T) {
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_CODEGEN_JOBS", "1")
	t.Setenv("ELISACORE_PARTITION_CACHE", "0")
	t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "0")
	t.Setenv("ELISACORE_PARALLEL_IR", "0")
	t.Setenv("ELISACORE_PARALLEL_OPT", " 1 ")

	options := cliOptions{
		optLevel:    backend.OptimizationLevel3,
		hasOptLevel: true,
	}
	if !buildCacheMayUseObjectMerger(options) {
		t.Fatal("whitespace-padded parallel-opt opt-in omitted the merger identity")
	}
}
