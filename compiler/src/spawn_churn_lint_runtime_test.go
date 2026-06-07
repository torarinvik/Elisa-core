package main

import (
	"strings"
	"testing"
)

const spawnChurnWarning = "spawns a fresh OS thread on every iteration"

// The thread-spawn-churn lint: a raw `spawn1`/`spawn_raw` inside a loop creates an OS thread
// per iteration (the dominant cost in fine-grained parallel loops) and is nudged toward a
// pool or nursery. Self-contained dummy `spawn1` so the AST-walk + name match is exercised
// without the concurrency runtime. (compileAndCaptureStderr lives in
// pointer_graph_lint_runtime_test.go, same package.)
func TestRunCLISpawnChurnLintFlagsSpawnInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "spawn_churn.elisa", `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + spawn1(0, i.i64())
    return acc
`)
	if !strings.Contains(out, spawnChurnWarning) {
		t.Fatalf("expected a spawn-churn warning for `spawn1` inside a loop, got:\n%s", out)
	}
}

// Pooled submission reuses worker threads — it is the batch tool, the analogue of `push`,
// and must NOT be flagged even inside a loop.
func TestRunCLISpawnChurnLintAllowsPoolSubmitInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "pool_submit_loop.elisa", `def pool_submit1(pool: i64, f: i64, arg: i64) -> i64:
    return arg

def submits() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + pool_submit1(0, 0, i.i64())
    return acc
`)
	if strings.Contains(out, spawnChurnWarning) {
		t.Fatalf("pool_submit1 in a loop must not be flagged as spawn churn, got:\n%s", out)
	}
}

// A single spawn outside any loop is the normal fork-join shape and must not be flagged.
func TestRunCLISpawnChurnLintAllowsSingleSpawn(t *testing.T) {
	out := compileAndCaptureStderr(t, "spawn_once.elisa", `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def once() -> i64:
    return spawn1(0, 7)
`)
	if strings.Contains(out, spawnChurnWarning) {
		t.Fatalf("a single spawn outside a loop must not be flagged, got:\n%s", out)
	}
}
