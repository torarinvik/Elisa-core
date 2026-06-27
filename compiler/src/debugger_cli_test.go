package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const debuggerCLIFixture = `struct Player:
    dead: mutable bool

def main() -> i64:
    player: Player = Player{dead: false}
    player.dead <- true
    return 0
`

func writeDebuggerCLIFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "debugger_cli.elisa")
	if err := os.WriteFile(path, []byte(debuggerCLIFixture), 0o644); err != nil {
		t.Fatalf("failed to write debugger fixture: %v", err)
	}
	return path
}

func TestRunCLIInterpretDebugBreakHuman(t *testing.T) {
	t.Parallel()
	fixture := writeDebuggerCLIFixture(t)
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", "-debug-break", "player.dead == true", fixture}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected debugger halt exit code, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	output := stderr.String()
	for _, want := range []string{"[ debugger ] halted: player.dead == true", "[ diff     ]", "player.dead:", "[ hint     ]"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected debug output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRunCLIInterpretDebugBreakJSONL(t *testing.T) {
	t.Parallel()
	fixture := writeDebuggerCLIFixture(t)
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", "-debug-break", "player.dead == true", "-debug-format", "jsonl", fixture}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected debugger halt exit code")
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected JSONL debug output on stdout only, got stderr:\n%s", stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple JSONL records, got:\n%s", stdout.String())
	}
	var sawHalt bool
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		if record["schema_version"] != "elisacore-debug-v1" {
			t.Fatalf("missing schema version in record %#v", record)
		}
		if record["record_type"] == "halt" {
			sawHalt = true
		}
	}
	if !sawHalt {
		t.Fatalf("expected halt record in JSONL output:\n%s", stdout.String())
	}
}

func TestRunCLIInterpretDebugSavesTrace(t *testing.T) {
	t.Parallel()
	fixture := writeDebuggerCLIFixture(t)
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", "-debug-break", "player.dead == true", "-debug-save-trace", tracePath, fixture}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected debugger halt exit code")
	}
	payload, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("expected saved trace: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatalf("saved trace is not JSON: %v\n%s", err, string(payload))
	}
	if record["schema_version"] != "elisacore-debug-v1" {
		t.Fatalf("expected schema version in saved trace, got %#v", record)
	}
}

func TestRunCLIInterpretWithoutDebugUnchanged(t *testing.T) {
	t.Parallel()
	fixture := writeDebuggerCLIFixture(t)
	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", fixture}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected normal interpret to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 0") {
		t.Fatalf("expected normal result output, got:\n%s", stdout.String())
	}
}
