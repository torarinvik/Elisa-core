package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBuildObjectCacheSMTSolverIdentityAndScriptBypass(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "freebsd" && runtime.GOOS != "linux" {
		t.Skip("cache selector admission currently recognizes ELF and Mach-O images")
	}
	dir := t.TempDir()
	solver := filepath.Join(dir, "solver")
	t.Setenv("ELISACORE_BUILD_CACHE", "0")
	t.Setenv("PATH", "")
	t.Setenv("ELISA_Z3", solver)
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	key := func(enabled bool) string {
		t.Helper()
		artifact, ok := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename, enableSMT: enabled}, program)
		if !ok {
			t.Fatal("cache fixture identity unavailable")
		}
		return artifact.key
	}
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, ok := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename, enableSMT: true}, program); ok {
		t.Fatal("script-based SMT solver was admitted to the build cache")
	}
	nativeMagic := []byte("\x7fELF")
	if runtime.GOOS == "darwin" {
		nativeMagic = []byte("\xcf\xfa\xed\xfe")
	}
	writeNativeSolver := func(marker byte) {
		t.Helper()
		contents := append(append([]byte(nil), nativeMagic...), marker)
		if err := os.WriteFile(solver, contents, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeNativeSolver(0)
	enabled, disabled := key(true), key(false)
	writeNativeSolver(1)
	if key(true) == enabled {
		t.Fatal("same-path SMT solver replacement did not invalidate build cache")
	}
	if key(false) != disabled {
		t.Fatal("disabled SMT unnecessarily depends on solver bytes")
	}
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, ok := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename, enableSMT: true}, program); ok {
		t.Fatal("native-to-script SMT solver replacement was admitted to the build cache")
	}
	writeNativeSolver(2)
	replacement := key(true)
	if err := os.Remove(solver); err != nil {
		t.Fatal(err)
	}
	if key(true) == replacement {
		t.Fatal("missing solver retained executable identity")
	}
	if key(false) != disabled {
		t.Fatal("disabled SMT depends on solver availability")
	}

}

func TestBuildObjectCacheBypassesScriptClangLauncher(t *testing.T) {
	dir := t.TempDir()
	clang := filepath.Join(dir, "clang")
	if err := os.WriteFile(clang, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELISACORE_BUILD_CACHE", "0")
	t.Setenv("PATH", dir)
	program := &loadedProgram{filename: filepath.Join(dir, "unit.elisa"), source: []byte("def main() -> i64:\n    return 0\n")}
	if _, ok := buildCacheObjectArtifactForProgram(cliOptions{emit: emitObject, filename: program.filename}, program); ok {
		t.Fatal("script-based clang launcher was admitted to the build cache")
	}
}
