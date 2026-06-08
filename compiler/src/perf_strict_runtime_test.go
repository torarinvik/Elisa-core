package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Graduated strictness (docs/70): the performance-friction lints are warnings by default
// (prototyping stays fluid, the build succeeds) but `-Wperf` promotes them to hard errors
// so shipped code can ban the anti-pattern outright.
func TestRunCLIPerfStrictPromotesLintsToErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		marker string
	}{
		{
			name: "pointer_graph.elisa",
			src: `struct Node:
    value: i64
    next: heap Node&?

def main() -> i64:
    return 0
`,
			marker: "raw self-referential pointer graph",
		},
		{
			name: "churn.elisa",
			src: `struct Node:
    v: i64

def main() -> i64:
    can Memory.Allocate, Abort.Panic:
        region r:
            acc: mutable i64 = 0
            for i in 0..<4:
                n: Node& @r = new[r] Node(i.i64())
                acc <- acc + n.v
            return acc
`,
			marker: "boxes a value on every iteration",
		},
		{
			name: "uninferred_auto_reserve.elisa",
			src: `def main(src: darray[darray[i64]]&) -> i64:
    can Memory.Allocate, Abort.Panic:
        ys: mutable darray[i64] = []
        for chunk in src:
            for x in chunk:
                ys.push(x)
        return ys[0]
`,
			marker: "cannot infer a safe reserve bound",
		},
		{
			name: "spawn_churn.elisa",
			src: `def spawn1(f: i64, arg: i64) -> i64:
    return arg

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + spawn1(0, i.i64())
    return acc
`,
			marker: "spawns a fresh OS thread on every iteration",
		},
		{
			name: "lock_churn.elisa",
			src: `def mutex_lock(mu: i64) -> i64:
    return mu

def mutex_unlock(g: i64):
    _ = g

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        guard: i64 = mutex_lock(i.i64())
        acc <- acc + guard
        mutex_unlock(guard)
    return acc
`,
			marker: "acquires a lock on every iteration",
		},
		{
			name: "pool_churn.elisa",
			src: `def pool_new(workers: i64) -> i64:
    return workers

def pool_shutdown(pool: i64):
    _ = pool

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        pool: i64 = pool_new(2)
        acc <- acc + pool + i.i64()
        pool_shutdown(pool)
    return acc
`,
			marker: "creates a thread pool on every iteration",
		},
		{
			name: "task_group_churn.elisa",
			src: `def task_group_new() -> i64:
    return 1

def task_group_wait_all(group: i64):
    _ = group

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        group: i64 = task_group_new()
        acc <- acc + group + i.i64()
        task_group_wait_all(group)
    return acc
`,
			marker: "creates a task group on every iteration",
		},
		{
			name: "atomic_hot_loop.elisa",
			src: `def fetch_add(slot: i64, value: i64, order: i64) -> i64:
    return slot + value + order

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + fetch_add(acc, i.i64(), 0)
    return acc
`,
			marker: "performs an atomic read-modify-write/compare-exchange on every iteration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			// Default: a warning — the compile still succeeds.
			var so, se bytes.Buffer
			if code := runCLI([]string{"-emit", "llvm", path}, &so, &se); code != 0 {
				t.Fatalf("expected default (warn) compile to succeed, exit=%d stderr:\n%s", code, se.String())
			}
			if !strings.Contains(se.String(), tc.marker) {
				t.Fatalf("expected a default warning containing %q, got:\n%s", tc.marker, se.String())
			}

			// -Wperf: the same diagnostic becomes a hard error — the compile fails.
			var so2, se2 bytes.Buffer
			if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &so2, &se2); code == 0 {
				t.Fatalf("expected -Wperf to fail the compile, but it succeeded; stderr:\n%s", se2.String())
			}
			if !strings.Contains(se2.String(), tc.marker) {
				t.Fatalf("expected the -Wperf error to contain %q, got:\n%s", tc.marker, se2.String())
			}
		})
	}
}
