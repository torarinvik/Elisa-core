package main

import (
	"os"
	"path/filepath"
	"testing"

	"elisacore/src/backend"
)

func TestBuildObjectCacheKeysNativeFragmentMerger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	t.Setenv("ELISACORE_PARTITION_CACHE", "1")
	t.Setenv("ELISACORE_NATIVE_FRAGMENTS", "1")
	linker := filepath.Join(dir, "ld")
	t.Setenv("ELISA_LD", linker)
	if err := os.WriteFile(linker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	program := &loadedProgram{
		filename: filepath.Join(dir, "unit.elisa"),
		source:   []byte("def main() -> i64:\n    return 0\n"),
	}
	options := cliOptions{
		emit:        emitObject,
		filename:    program.filename,
		optLevel:    backend.OptimizationLevel3,
		hasOptLevel: true,
	}
	key := func(options cliOptions) string {
		t.Helper()
		artifact, ok := buildCacheObjectArtifactForProgram(options, program)
		if !ok {
			t.Fatal("cache fixture identity unavailable")
		}
		return artifact.key
	}

	first := key(options)
	if err := os.WriteFile(linker, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if key(options) == first {
		t.Fatal("same-path linker replacement did not invalidate native-fragment object")
	}

	// The merger is irrelevant to O0, which does not emit native fragments.
	options.optLevel = backend.OptimizationLevel0
	withoutFragmentsBefore := key(options)
	if err := os.WriteFile(linker, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if key(options) != withoutFragmentsBefore {
		t.Fatal("O0 object cache unnecessarily depends on native-fragment linker")
	}
}
