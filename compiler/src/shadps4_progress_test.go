package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestShadPS4ElisaHarnessProgressClean(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	tests := []struct {
		name string
		path string
	}{
		{
			name: "main harness",
			path: filepath.Join(repoRoot, "shadPS4", "elisa", "src", "main.elisa"),
		},
		{
			name: "C API fixture tests",
			path: filepath.Join(repoRoot, "shadPS4", "elisa", "test", "shadps4_c_api_tests.elisa"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "progress", tc.path}, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected progress report to succeed, got %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "warnings: 0") {
				t.Fatalf("expected shadPS4 Elisa progress report to stay warning-free, got:\n%s", stdout.String())
			}
			if strings.Contains(stdout.String(), "progress warning:") {
				t.Fatalf("expected no progress warnings, got:\n%s", stdout.String())
			}
			if strings.Contains(stderr.String(), "progress error:") {
				t.Fatalf("expected no progress errors, got:\n%s", stderr.String())
			}
		})
	}
}
