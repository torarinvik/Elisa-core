package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestProgressPressureFixtures(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := filepath.Join(repoRoot, "compiler", "test", "progress")
	tests := []struct {
		name         string
		file         string
		wantExit     int
		wantStdout   []string
		wantStderr   []string
		forbidStdout []string
		forbidStderr []string
	}{
		{
			name:       "unbudgeted loop warns",
			file:       "unbudgeted_loop.elisa",
			wantExit:   0,
			wantStdout: []string{"warnings: 1", "progress warning: while loop has no progress evidence"},
		},
		{
			name:         "budgeted loop is clean",
			file:         "budgeted_loop.elisa",
			wantExit:     0,
			wantStdout:   []string{"warnings: 0", "spin: obligations=Loop:1 evidence=progress"},
			forbidStdout: []string{"progress warning:"},
		},
		{
			name:       "recursive cycle warns",
			file:       "recursive_cycle.elisa",
			wantExit:   0,
			wantStdout: []string{"warnings: 1", "progress warning: recursive cycle"},
		},
		{
			name:       "main thread blocking errors",
			file:       "main_thread_blocking.elisa",
			wantExit:   1,
			wantStderr: []string{"progress error: @main_thread function may block", "path: on_click -> wait_for_worker"},
		},
		{
			name:         "trusted escape hatches are explicit",
			file:         "trusted_escape_hatches.elisa",
			wantExit:     0,
			wantStdout:   []string{"warnings: 0", "unsafe_nonprogress=true", "unsafe_block_main=true"},
			forbidStdout: []string{"progress warning:"},
			forbidStderr: []string{"progress error:"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "progress", filepath.Join(fixtureDir, tc.file)}, &stdout, &stderr)
			if exitCode != tc.wantExit {
				t.Fatalf("expected exit %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantExit, exitCode, stdout.String(), stderr.String())
			}
			for _, check := range tc.wantStdout {
				if !strings.Contains(stdout.String(), check) {
					t.Fatalf("expected stdout to contain %q, got:\n%s", check, stdout.String())
				}
			}
			for _, check := range tc.wantStderr {
				if !strings.Contains(stderr.String(), check) {
					t.Fatalf("expected stderr to contain %q, got:\n%s", check, stderr.String())
				}
			}
			for _, check := range tc.forbidStdout {
				if strings.Contains(stdout.String(), check) {
					t.Fatalf("expected stdout not to contain %q, got:\n%s", check, stdout.String())
				}
			}
			for _, check := range tc.forbidStderr {
				if strings.Contains(stderr.String(), check) {
					t.Fatalf("expected stderr not to contain %q, got:\n%s", check, stderr.String())
				}
			}
		})
	}
}
