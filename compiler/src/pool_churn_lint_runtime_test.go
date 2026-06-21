package main

import (
	"strings"
	"testing"
)

const poolChurnWarning = "creates a thread pool on every iteration"

func TestRunCLIPoolChurnLintFlagsPoolNewInLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "pool_churn.elisa", `def pool_new(workers: i64) -> i64:
    return workers

def pool_shutdown(pool: i64):
    _ = pool

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        pool: i64 = pool_new(2)
        acc <- acc + pool + i.i64()
        pool_shutdown(pool)
    return acc
`)
	if !strings.Contains(out, poolChurnWarning) {
		t.Fatalf("expected a pool-churn warning for `pool_new` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIPoolChurnLintFlagsPoolScopeInLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "pool_scope_churn.elisa", `struct ThreadPool:
    value: i64

extern pool_new(workers: usize) -> ThreadPool
extern pool_shutdown(pool: ThreadPool&) -> void

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        pool workers(2):
            acc <- acc + i.i64()
    return acc
`)
	if !strings.Contains(out, poolChurnWarning) {
		t.Fatalf("expected a pool-churn warning for `pool ...:` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIPoolChurnLintAllowsPoolOutsideLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "pool_once.elisa", `def pool_new(workers: i64) -> i64:
    return workers

def pool_submit1(pool: i64, f: i64, arg: i64) -> i64:
    return pool + arg

def pool_shutdown(pool: i64):
    _ = pool

def submits() -> i64:
    pool: i64 = pool_new(2)
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + pool_submit1(pool, 0, i.i64())
    pool_shutdown(pool)
    return acc
`)
	if strings.Contains(out, poolChurnWarning) {
		t.Fatalf("a pool created outside the loop with submissions inside must not be flagged, got:\n%s", out)
	}
}
