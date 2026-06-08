package main

import (
	"strings"
	"testing"
)

const awaitHotLoopWarning = "waits for a task on every iteration"
const joinHotLoopWarning = "joins a thread on every iteration"
const waitAllHotLoopWarning = "waits for a task group on every iteration"

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

func TestRunCLIAwaitHotLoopLintFlagsJoinInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "join_hot_loop.elisa", `def spawn1(f: i64, arg: i64) -> Thread[i64, Joinable]:
    _ = f
    return Thread[i64, Joinable](arg, 0)

def join(t: Thread[i64, Joinable]) -> i64:
    _ = t
    return 1

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        t: Thread[i64, Joinable] = spawn1(0, i.i64())
        acc <- acc + join(t)
    return acc
`)
	if !strings.Contains(out, joinHotLoopWarning) {
		t.Fatalf("expected a join-hot-loop warning for `join` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAwaitHotLoopLintDoesNotFlagNonThreadJoin(t *testing.T) {
	out := compileAndCaptureStderr(t, "non_thread_join_loop.elisa", `struct Span:
    value: i64

def join(left: Span, right: Span) -> Span:
    return Span(left.value + right.value)

def ok() -> i64:
    acc: mutable Span = Span(0)
    for i in 0..<4:
        acc <- join(acc, Span(i.i64()))
    return acc.value
`)
	if strings.Contains(out, joinHotLoopWarning) {
		t.Fatalf("a non-thread function named join must not be flagged, got:\n%s", out)
	}
}

func TestRunCLIAwaitHotLoopLintFlagsWaitAllInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "wait_all_hot_loop.elisa", `def task_group_wait_all(group: i64):
    _ = group

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        task_group_wait_all(i.i64())
        acc <- acc + i.i64()
    return acc
`)
	if !strings.Contains(out, waitAllHotLoopWarning) {
		t.Fatalf("expected a wait-all-hot-loop warning for `task_group_wait_all` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAwaitHotLoopLintFlagsWaitAllSugarInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "wait_all_sugar_hot_loop.elisa", `struct TaskGroup:
    value: i64

def task_group_wait_all(group: TaskGroup&):
    _ = group

def churns() -> i64:
    group: TaskGroup = TaskGroup(0)
    acc: mutable i64 = 0
    for i in 0..<4:
        wait all group
        acc <- acc + i.i64()
    return acc
`)
	if !strings.Contains(out, waitAllHotLoopWarning) {
		t.Fatalf("expected a wait-all-hot-loop warning for `wait all` inside a loop, got:\n%s", out)
	}
}
