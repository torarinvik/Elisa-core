package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIPerfHotLoopTrustedSuppressesLoopPerfWarning(t *testing.T) {
	out := compileAndCaptureStderr(t, "trusted_perf_hot_loop.elisa", `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def acknowledged() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        trusted Perf.HotLoop:
            acc <- acc + spawn1(0, i.i64())
    return acc
`)
	if strings.Contains(out, spawnChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress the local spawn-churn warning, got:\n%s", out)
	}
}

func TestRunCLIPerfHotLoopTrustedSuppressesWholeTrustedLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "trusted_perf_hot_loop_outer.elisa", `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def acknowledged() -> i64:
    acc: mutable i64 = 0
    trusted Perf.HotLoop:
        for i in 0..<4:
            acc <- acc + spawn1(0, i.i64())
    return acc
`)
	if strings.Contains(out, spawnChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress loop perf warnings inside the trusted block, got:\n%s", out)
	}
}

func TestRunCLIPerfHotLoopTrustedAllowsPerfStrictCompile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trusted_perf_hot_loop_strict.elisa")
	src := `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        trusted Perf.HotLoop:
            acc <- acc + spawn1(0, i.i64())
    return acc
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var so, se bytes.Buffer
	if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &so, &se); code != 0 {
		t.Fatalf("trusted Perf.HotLoop should keep -Wperf compile successful, exit=%d stderr:\n%s", code, se.String())
	}
	if strings.Contains(se.String(), spawnChurnWarning) {
		t.Fatalf("trusted Perf.HotLoop should suppress -Wperf spawn-churn diagnostics, got:\n%s", se.String())
	}
}
