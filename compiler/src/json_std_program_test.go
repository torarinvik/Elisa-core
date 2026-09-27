package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// json_std.elisa is the JSON conformance program for the public
// region-indexed handle API. Compiling and running it here keeps it from
// rotting silently when the JSON runtime surface changes.
func TestRunCLIExecutesJsonStdProgram(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "json_std.elisa")
	outputDir := t.TempDir()
	objectPath := filepath.Join(outputDir, "json_std.o")
	exePath := filepath.Join(outputDir, "json_std")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected json_std to compile, stderr:\n%s", stderr.String())
	}
	// The runtime carries its own pre-existing warnings; hold only the
	// program itself to a clean compile.
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.Contains(line, "json_std.elisa:") {
			t.Fatalf("expected no diagnostics against json_std.elisa, got:\n%s", stderr.String())
		}
	}

	compileOutput, err := compileNativeTestExecutable(t, clangPath, outputDir, []string{objectPath, "-o", exePath})
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runOutput, err := exec.Command(exePath).CombinedOutput()
	if err != nil {
		t.Fatalf("json_std program failed: %v\n%s", err, string(runOutput))
	}
}
