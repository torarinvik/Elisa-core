package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests use a per-iteration lock (lock-churn lint, still live) to exercise
// `trusted Perf.HotLoop` suppression. The spawn-churn lint that previously stood
// in here was removed along with the raw `spawn1` surface. lockChurnWarning is
// declared in lock_churn_lint_runtime_test.go.

func TestRunCLIPerfHotLoopTrustedSuppressesLoopPerfWarning(t *testing.T) {
	out := compileAndCaptureStderr(t, "trusted_perf_hot_loop.elisa", `def mutex_lock(mu: i64) -> i64:
    return mu

def acknowledged() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        trusted Perf.HotLoop:
            acc <- acc + mutex_lock(i.i64())
    return acc
`)
	if strings.Contains(out, lockChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress the local lock-churn warning, got:\n%s", out)
	}
}

func TestRunCLIPerfHotLoopTrustedSuppressesWholeTrustedLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "trusted_perf_hot_loop_outer.elisa", `def mutex_lock(mu: i64) -> i64:
    return mu

def acknowledged() -> i64:
    acc: mutable i64 = 0
    trusted Perf.HotLoop:
        for i in 0..<4:
            acc <- acc + mutex_lock(i.i64())
    return acc
`)
	if strings.Contains(out, lockChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress loop perf warnings inside the trusted block, got:\n%s", out)
	}
}

func TestRunCLIPerfHotLoopTrustedAllowsPerfStrictCompile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trusted_perf_hot_loop_strict.elisa")
	src := `def mutex_lock(mu: i64) -> i64:
    return mu

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        trusted Perf.HotLoop:
            acc <- acc + mutex_lock(i.i64())
    return acc
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var so, se bytes.Buffer
	if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &so, &se); code != 0 {
		t.Fatalf("trusted Perf.HotLoop should keep -Wperf compile successful, exit=%d stderr:\n%s", code, se.String())
	}
	if strings.Contains(se.String(), lockChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress -Wperf lock-churn diagnostics, got:\n%s", se.String())
	}
}
