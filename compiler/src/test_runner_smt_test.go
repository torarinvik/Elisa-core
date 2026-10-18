//go:build cgo

package main

import (
	"bytes"
	"elisacore/src/backend"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedTestRunnerPreservesSMTSelection(t *testing.T) {
	for _, tool := range []string{"z3", "clang"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " required")
		}
	}
	t.Setenv("ELISACORE_TEST_CACHE", "0")
	source := "ghost def measure(text: sview, index: usize) -> usize:\n    requires index <= text.len\n    decreases text.len - index\n    return 0 if index == text.len\n    return 1 + measure(text, index + 1)\n\n@test\ndef smoke() -> void:\n    assert true\n"
	path := filepath.Join(t.TempDir(), "runner_smt.elisa")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"-smt", "-nosmt"} {
		var output, diagnostics bytes.Buffer
		status := runCLI([]string{"-emit", "test", "-permissive", mode, path}, &output, &diagnostics)
		if mode == "-smt" {
			if status != 0 || !strings.Contains(output.String(), "passed=1") {
				t.Fatalf("generated runner lost SMT: status=%d\n%s\n%s", status, output.String(), diagnostics.String())
			}
		} else if status == 0 || !strings.Contains(diagnostics.String(), "decreases") {
			t.Fatalf("-nosmt was ignored: status=%d\n%s", status, diagnostics.String())
		}
	}
}

func TestTestRunnerCachesSeparateSMTSelection(t *testing.T) {
	program := &loadedProgram{filename: filepath.Join(t.TempDir(), "runner.elisa"), source: []byte("@test\ndef smoke() -> void:\n    assert true\n")}
	var inner, early [2]string
	for i, smt := range []bool{false, true} {
		artifact, err := testRunnerCacheArtifactFor("def main() -> i64:\n    return 0\n", "", nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", smt)
		if err != nil {
			t.Fatal(err)
		}
		inner[i] = artifact.key
		early[i], err = earlyTestCacheKeyForProgram(program, "", nil, nil, nil, backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, smt)
		if err != nil {
			t.Fatal(err)
		}
	}
	if inner[0] == inner[1] || early[0] == early[1] {
		t.Fatal("SMT policy aliases a test executable cache key")
	}
}
