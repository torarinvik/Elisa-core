//go:build atplsuite
// +build atplsuite

package main

import (
	"bytes"
	"elisacore/src/semantic"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	atplCLIBuildOnce sync.Once
	atplCLIBuildErr  error
	atplCLIBinary    string
)

func buildATPLCLI(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	atplCLIBuildOnce.Do(func() {
		scriptPath := filepath.Join(repoRoot, "compiler", "scripts", "build_atpl_cli.sh")
		cmd := exec.Command("bash", scriptPath)
		cmd.Dir = repoRoot
		output, err := cmd.CombinedOutput()
		if err != nil {
			atplCLIBuildErr = fmt.Errorf("build_atpl_cli.sh failed: %w\n%s", err, string(output))
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				atplCLIBinary = line
			}
		}
		if atplCLIBinary == "" {
			atplCLIBinary = filepath.Join(repoRoot, "compiler", "bin", "atpl")
		}
	})
	if atplCLIBuildErr != nil {
		t.Fatal(atplCLIBuildErr)
	}
	if _, err := os.Stat(atplCLIBinary); err != nil {
		t.Fatalf("expected ATPL CLI binary at %s: %v", atplCLIBinary, err)
	}
	return atplCLIBinary
}

func runATPLCLI(t *testing.T, args []string, stdin string) (string, string, error) {
	t.Helper()
	repoRoot := repoRootFromMainTest(t)
	binPath := buildATPLCLI(t)
	cmd := exec.Command(binPath, args...)
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestATPLCLIExecutesFile(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "sample.atpl")
	if err := os.WriteFile(fixturePath, []byte("x = 40\nx + 2\n"), 0o644); err != nil {
		t.Fatalf("failed to write ATPL fixture: %v", err)
	}

	binPath := buildATPLCLI(t)
	cmd := exec.Command(binPath, fixturePath)
	cmd.Dir = repoRoot
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ATPL CLI file run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "42" {
		t.Fatalf("expected file execution to print 42, got %q", got)
	}
}

func TestATPLCLIExecutesSTDIN(t *testing.T) {
	t.Parallel()
	stdout, stderr, err := runATPLCLI(t, nil, "x = 20\nx + 22\n")
	if err != nil {
		t.Fatalf("ATPL CLI stdin run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr output, got:\n%s", stderr)
	}
	if got := strings.TrimSpace(stdout); got != "42" {
		t.Fatalf("expected stdin execution to print 42, got %q", got)
	}
}

func TestATPLCLIReplCommandsAndState(t *testing.T) {
	t.Parallel()
	stdin := strings.Join([]string{
		"x = 40",
		"",
		"x + 2",
		"",
		":modules",
		":reset",
		"x",
		"",
		":q",
		"",
	}, "\n")
	stdout, stderr, err := runATPLCLI(t, []string{"--repl"}, stdin)
	if err != nil {
		t.Fatalf("ATPL REPL run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, check := range []string{
		"ATPL REPL — blank line evaluates, :help shows commands, :quit exits.",
		"atpl> ....> nil",
		"atpl> ....> 42",
		"available modules: core, control, js, oop, reflect, string",
		"session reset",
	} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected REPL stdout to contain %q, got:\n%s", check, stdout)
		}
	}
	if !strings.Contains(stderr, "name error at 1:1: "+semantic.UndefinedIdentifierMessage("x")) {
		t.Fatalf("expected REPL stderr to contain detailed undefined identifier diagnostic after reset, got:\n%s", stderr)
	}
}

func TestATPLCLIReplLoadAndOpenFiles(t *testing.T) {
	t.Parallel()
	fixtureRoot := filepath.Join(t.TempDir(), "fixtures with spaces")
	if err := os.MkdirAll(fixtureRoot, 0o755); err != nil {
		t.Fatalf("failed to create ATPL REPL fixture dir: %v", err)
	}

	firstPath := filepath.Join(fixtureRoot, "first file.atpl")
	secondPath := filepath.Join(fixtureRoot, "second file.atpl")
	if err := os.WriteFile(firstPath, []byte("loaded = 40\nloaded\n"), 0o644); err != nil {
		t.Fatalf("failed to write first ATPL REPL fixture: %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("second = 10\nsecond\n"), 0o644); err != nil {
		t.Fatalf("failed to write second ATPL REPL fixture: %v", err)
	}

	stdin := strings.Join([]string{
		":load " + firstPath,
		":open " + secondPath,
		"loaded + second",
		"",
		":q",
		"",
	}, "\n")

	stdout, stderr, err := runATPLCLI(t, []string{"--repl"}, stdin)
	if err != nil {
		t.Fatalf("ATPL REPL file-load run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr output, got:\n%s", stderr)
	}
	for _, check := range []string{
		"atpl> 40",
		"atpl> 10",
		"atpl> ....> 50",
	} {
		if !strings.Contains(stdout, check) {
			t.Fatalf("expected REPL stdout to contain %q, got:\n%s", check, stdout)
		}
	}
}

func TestATPLCLIReplReloadResetsLastLoadedFile(t *testing.T) {
	t.Parallel()
	fixturePath := filepath.Join(t.TempDir(), "reload.atpl")
	if err := os.WriteFile(fixturePath, []byte("loaded = 40\nloaded\n"), 0o644); err != nil {
		t.Fatalf("failed to write ATPL reload fixture: %v", err)
	}

	stdin := strings.Join([]string{
		":load " + fixturePath,
		"scratch = 2",
		"",
		":reload",
		"scratch",
		"",
		"loaded",
		"",
		":q",
		"",
	}, "\n")

	stdout, stderr, err := runATPLCLI(t, []string{"--repl"}, stdin)
	if err != nil {
		t.Fatalf("ATPL REPL reload run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "name error at 1:1: "+semantic.UndefinedIdentifierMessage("scratch")) {
		t.Fatalf("expected reload to clear scratch binding, got stderr:\n%s", stderr)
	}
	if strings.Count(stdout, "atpl> 40") < 2 {
		t.Fatalf("expected initial load and reload to both print 40, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "atpl> ....> 40") {
		t.Fatalf("expected loaded binding to remain available after reload, got:\n%s", stdout)
	}
}

func TestATPLCLIReportsDetailedSTDINErrors(t *testing.T) {
	t.Parallel()
	stdout, stderr, err := runATPLCLI(t, nil, "x\n")
	if err == nil {
		t.Fatalf("expected ATPL CLI stdin run to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout output, got:\n%s", stdout)
	}
	if got := strings.TrimSpace(stderr); got != "name error at 1:1: "+semantic.UndefinedIdentifierMessage("x") {
		t.Fatalf("expected detailed undefined identifier diagnostic, got %q", got)
	}
}

func TestATPLExamplesMatchGoldenViaSelfHostedCLI(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	binPath := buildATPLCLI(t)
	examplesRoot := filepath.Join(repoRoot, "Code", "elisacore_atpl", "examples")
	seen := 0

	err := filepath.WalkDir(examplesRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".atpl" {
			return nil
		}

		seen++
		relPath, err := filepath.Rel(examplesRoot, path)
		if err != nil {
			return err
		}

		t.Run(strings.TrimSuffix(filepath.ToSlash(relPath), ".atpl"), func(t *testing.T) {
			goldenPath := strings.TrimSuffix(path, ".atpl") + ".golden"
			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("failed to read golden file %s: %v", goldenPath, err)
			}

			cmd := exec.Command(binPath, path)
			cmd.Dir = repoRoot
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("self-hosted ATPL example failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output for %s, got:\n%s", path, stderr.String())
			}

			got := strings.TrimSpace(stdout.String())
			want := strings.TrimSpace(string(wantBytes))
			if got != want {
				t.Fatalf("self-hosted example output mismatch for %s: got %q, want %q", path, got, want)
			}
		})

		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) failed: %v", examplesRoot, err)
	}
	if seen == 0 {
		t.Fatal("expected at least one ATPL example")
	}
}
