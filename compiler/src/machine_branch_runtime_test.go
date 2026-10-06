package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"elisacore/src/backend"
)

func TestMachineBranchTransitionsRuntimeAtO0AndO2(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang is unavailable")
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	fixture := filepath.Join(filepath.Dir(testFile), "..", "test", "fixtures", "machine_transition", "branch_transitions_dynamic.elisa")
	source, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	var stderr bytes.Buffer
	_, result, ok := analyzeProgram(fixture, source, &stderr)
	if !ok {
		t.Fatalf("analyze fixture failed:\n%s", stderr.String())
	}

	for _, opt := range []backend.OptimizationLevel{backend.OptimizationLevel0, backend.OptimizationLevel2} {
		name := map[backend.OptimizationLevel]string{backend.OptimizationLevel0: "O0", backend.OptimizationLevel2: "O2"}[opt]
		t.Run(name, func(t *testing.T) {
			exe, cleanup, err := buildNativeExecutable(result, nil, nil, "", opt, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
			if err != nil {
				t.Fatalf("build %s fixture: %v\n%s", name, err, stderr.String())
			}
			defer cleanup()
			for _, args := range [][]string{nil, {"extra"}} {
				cmd := exec.Command(exe, args...)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("run %s with args %v: %v\n%s", name, args, err, output)
				}
			}
		})
	}
}
