package main

import (
	"strings"
	"testing"
)

func TestRunCLIConcurrencyPerfLintCatalog(t *testing.T) {
	out := compileAndCaptureStderr(t, "concurrency_perf_catalog.elisa", `def pool_new(workers: i64) -> i64:
    return workers

def task_group_new() -> i64:
    return 1

def mutex_lock(mu: i64) -> i64:
    return mu

def fetch_add(slot: i64, value: i64, order: i64) -> i64:
    return slot + value + order

def pool_await(task: i64) -> i64:
    return task

def task_group_wait_all(group: i64):
    _ = group

def catalog() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + pool_new(2)
        acc <- acc + task_group_new()
        acc <- acc + mutex_lock(i.i64())
        acc <- acc + fetch_add(acc, i.i64(), 0)
        acc <- acc + pool_await(i.i64())
        task_group_wait_all(i.i64())
    return acc
`)
	for _, marker := range []string{
		"creates a thread pool on every iteration",
		"creates a task group on every iteration",
		"acquires a lock on every iteration",
		"performs an atomic read-modify-write/compare-exchange on every iteration",
		"waits for a task on every iteration",
		"waits for a task group on every iteration",
	} {
		if !strings.Contains(out, marker) {
			t.Fatalf("expected concurrency perf catalog warning containing %q, got:\n%s", marker, out)
		}
	}
	if got := strings.Count(out, "trusted Perf.HotLoop:"); got < 6 {
		t.Fatalf("expected concurrency perf diagnostics to mention trusted Perf.HotLoop acknowledgement, got %d mention(s):\n%s", got, out)
	}
}
