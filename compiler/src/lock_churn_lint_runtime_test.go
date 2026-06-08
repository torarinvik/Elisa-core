package main

import (
	"strings"
	"testing"
)

const lockChurnWarning = "acquires a lock on every iteration"

func TestRunCLILockChurnLintFlagsMutexLockInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "lock_churn.elisa", `def mutex_lock(mu: i64) -> i64:
    return mu

def mutex_unlock(g: i64):
    _ = g

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        guard: i64 = mutex_lock(i.i64())
        acc <- acc + guard
        mutex_unlock(guard)
    return acc
`)
	if !strings.Contains(out, lockChurnWarning) {
		t.Fatalf("expected a lock-churn warning for `mutex_lock` inside a loop, got:\n%s", out)
	}
}

func TestRunCLILockChurnLintFlagsLockStmtInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "lock_stmt_churn.elisa", `struct Mutex:
    value: i64

def mutex_lock(mu: Mutex&) -> i64:
    return mu.value

def churns() -> i64:
    mu: Mutex = Mutex(1)
    acc: mutable i64 = 0
    for i in 0..<4:
        lock mu as guard:
            acc <- acc + guard + i.i64()
    return acc
`)
	if !strings.Contains(out, lockChurnWarning) {
		t.Fatalf("expected a lock-churn warning for `lock ... as` inside a loop, got:\n%s", out)
	}
}

func TestRunCLILockChurnLintAllowsSingleMutexLock(t *testing.T) {
	out := compileAndCaptureStderr(t, "lock_once.elisa", `def mutex_lock(mu: i64) -> i64:
    return mu

def mutex_unlock(g: i64):
    _ = g

def once() -> i64:
    guard: i64 = mutex_lock(7)
    mutex_unlock(guard)
    return guard
`)
	if strings.Contains(out, lockChurnWarning) {
		t.Fatalf("a single mutex_lock outside a loop must not be flagged, got:\n%s", out)
	}
}
