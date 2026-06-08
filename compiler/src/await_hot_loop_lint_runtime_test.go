package main

import (
	"strings"
	"testing"
)

const awaitHotLoopWarning = "waits for a task on every iteration"

func TestRunCLIAwaitHotLoopLintFlagsPoolAwaitInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "await_hot_loop.elisa", `def pool_await(task: i64) -> i64:
    return task

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + pool_await(i.i64())
    return acc
`)
	if !strings.Contains(out, awaitHotLoopWarning) {
		t.Fatalf("expected an await-hot-loop warning for `pool_await` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAwaitHotLoopLintFlagsAwaitSugarInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "await_sugar_hot_loop.elisa", `struct Task:
    value: i64

def pool_await(task: Task) -> i64:
    return task.value

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        task: Task = Task(i.i64())
        acc <- acc + await task
    return acc
`)
	if !strings.Contains(out, awaitHotLoopWarning) {
		t.Fatalf("expected an await-hot-loop warning for `await` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAwaitHotLoopLintAllowsSingleAwait(t *testing.T) {
	out := compileAndCaptureStderr(t, "await_once.elisa", `def pool_await(task: i64) -> i64:
    return task

def once() -> i64:
    return pool_await(7)
`)
	if strings.Contains(out, awaitHotLoopWarning) {
		t.Fatalf("a single await outside a loop must not be flagged, got:\n%s", out)
	}
}
