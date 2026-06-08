package main

import (
	"strings"
	"testing"
)

const taskGroupChurnWarning = "creates a task group on every iteration"

func TestRunCLITaskGroupChurnLintFlagsTaskGroupNewInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "task_group_churn.elisa", `def task_group_new() -> i64:
    return 1

def task_group_wait_all(group: i64):
    _ = group

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        group: i64 = task_group_new()
        acc <- acc + group + i.i64()
        task_group_wait_all(group)
    return acc
`)
	if !strings.Contains(out, taskGroupChurnWarning) {
		t.Fatalf("expected a task-group-churn warning for `task_group_new` inside a loop, got:\n%s", out)
	}
}

func TestRunCLITaskGroupChurnLintAllowsGroupOutsideLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "task_group_once.elisa", `def task_group_new() -> i64:
    return 1

def task_group_add(group: i64, task: i64):
    _ = group
    _ = task

def task_group_wait_all(group: i64):
    _ = group

def submits() -> i64:
    group: i64 = task_group_new()
    acc: mutable i64 = 0
    for i in 0..<4:
        task_group_add(group, i.i64())
        acc <- acc + i.i64()
    task_group_wait_all(group)
    return acc
`)
	if strings.Contains(out, taskGroupChurnWarning) {
		t.Fatalf("a task group created outside the loop with additions inside must not be flagged, got:\n%s", out)
	}
}
