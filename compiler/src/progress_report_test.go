package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitProgressReportsLoopAndRecursionPressure(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRunCLIEmitProgressRecognizesRuntimeProgressTickEvidence(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "progress_runtime_tick.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`include %q

def spin(flag: bool) -> void:
    budget: mutable ProgressBudget = progress_budget_steps(3)
    while flag:
        progress_tick(&budget) can Progress.Tick, Progress.CheckCancel, Abort.Panic
`, runtimeInclude)
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
		"spin: obligations=Loop:1 evidence=progress unsafe_nonprogress=false",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected progress report to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIEmitProgressReportsBlockingFunctions(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "progress_blocking.elisa")
	src := `
extern wait_for_worker() -> void can[Blocking.Wait]

def on_click() -> void:
    wait_for_worker()
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
		"warnings: 1",
		"on_click: obligations=none evidence=none unsafe_nonprogress=false blocking=true unsafe_block_main=false",
		"progress warning: function may block via Blocking.* permission",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected progress report to contain %q, got:\n%s", check, output)
		}
	}
}
