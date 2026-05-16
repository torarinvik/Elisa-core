package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitProgressReportsLoopAndRecursionPressure(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "progress_pressure.elisa")
	src := `
def spin(flag: bool) -> void:
    while flag:
        pass

def ping() -> void:
    pong()

def pong() -> void:
    ping()
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write progress fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "progress", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected progress report to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected progress report diagnostics in stdout only, got stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"=== progress ===",
		"warnings: 2",
		"spin: obligations=Loop:1 evidence=none unsafe_nonprogress=false",
		"progress warning: while loop has no progress evidence",
		"progress warning: recursive cycle",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected progress report to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIEmitProgressAcceptsExplicitEvidence(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "progress_safe.elisa")
	src := `
def spin(flag: bool) -> void:
    while flag:
        can Progress.Tick:
            signal Progress.Tick
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write progress fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit=progress", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected progress report to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"warnings: 0",
		"spin: obligations=Loop:1 evidence=progress unsafe_nonprogress=false",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected progress report to contain %q, got:\n%s", check, output)
		}
	}
}
