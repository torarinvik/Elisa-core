package main

import (
	"strings"
	"testing"
)

const churnWarning = "boxes a value on every iteration"

// The allocation-churn lint (docs/70): individual `new` boxing inside a loop is per-object
// allocation and is nudged toward batch allocation. (compileAndCaptureStderr lives in
// pointer_graph_lint_runtime_test.go, same package.)
func TestRunCLIChurnLintFlagsNewInLoop(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "churn_new.elisa", `struct Node:
    v: i64

def churns() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r:
            acc: mutable i64 = 0
            for i in 0..<10:
                n: Node& @r = new[r] Node(i.i64())
                acc <- acc + n.v
            return acc
`)
	if !strings.Contains(out, churnWarning) {
		t.Fatalf("expected a churn warning for `new` inside a loop, got:\n%s", out)
	}
}

// Accumulation is the intended way to build a collection: pushing into a darray inside a
// loop is NOT churn and must not be flagged.
func TestRunCLIChurnLintAllowsPushAccumulation(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "churn_push.elisa", `def builds() -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        for i in 0..<10:
            xs.push(i.i64())
        return xs.count
`)
	if strings.Contains(out, churnWarning) {
		t.Fatalf("expected NO churn warning for push accumulation, got:\n%s", out)
	}
}

// A single `new` outside any loop is not churn — it allocates once.
func TestRunCLIChurnLintAllowsSingleAllocation(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "churn_once.elisa", `struct Node:
    v: i64

def once() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r:
            n: Node& @r = new[r] Node(5)
            return n.v
`)
	if strings.Contains(out, churnWarning) {
		t.Fatalf("expected NO churn warning for a single allocation, got:\n%s", out)
	}
}
