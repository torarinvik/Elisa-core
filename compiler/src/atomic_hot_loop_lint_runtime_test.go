package main

import (
	"strings"
	"testing"
)

const atomicHotLoopWarning = "performs an atomic read-modify-write/compare-exchange on every iteration"

func TestRunCLIAtomicHotLoopLintFlagsFetchAddInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "atomic_hot_loop.elisa", `def fetch_add(slot: i64, value: i64, order: i64) -> i64:
    return slot + value + order

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + fetch_add(acc, i.i64(), 0)
    return acc
`)
	if !strings.Contains(out, atomicHotLoopWarning) {
		t.Fatalf("expected an atomic hot-loop warning for `fetch_add` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAtomicHotLoopLintFlagsAtomicCellRmwInLoop(t *testing.T) {
	out := compileAndCaptureStderr(t, "atomic_cell_hot_loop.elisa", `def atomic_fetch_add_acqrel(slot: i64, value: i64) -> i64:
    return slot + value

def churns() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + atomic_fetch_add_acqrel(acc, i.i64())
    return acc
`)
	if !strings.Contains(out, atomicHotLoopWarning) {
		t.Fatalf("expected an atomic hot-loop warning for `atomic_fetch_add_acqrel` inside a loop, got:\n%s", out)
	}
}

func TestRunCLIAtomicHotLoopLintAllowsSingleRmw(t *testing.T) {
	out := compileAndCaptureStderr(t, "atomic_once.elisa", `def fetch_add(slot: i64, value: i64, order: i64) -> i64:
    return slot + value + order

def once() -> i64:
    return fetch_add(1, 2, 0)
`)
	if strings.Contains(out, atomicHotLoopWarning) {
		t.Fatalf("a single atomic RMW outside a loop must not be flagged, got:\n%s", out)
	}
}
