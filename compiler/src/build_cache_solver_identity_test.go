package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildObjectCacheKeysSMTSolverArtifact(t *testing.T) {
	dir := t.TempDir()
	solver := filepath.Join(dir, "solver")
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
	enabled, disabled := key(true), key(false)
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if key(true) == enabled {
		t.Fatal("same-path SMT solver replacement did not invalidate build cache")
	}
	if key(false) != disabled {
		t.Fatal("disabled SMT unnecessarily depends on solver bytes")
	}
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
