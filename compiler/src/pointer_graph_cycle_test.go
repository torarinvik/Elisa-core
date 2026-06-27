package main

import (
	"strings"
	"testing"
)

const pointerGraphCycleWarning = "raw pointer-graph cycle"

// A raw mutual-recursion cycle (A → B → A, both edges bare `heap _&?` refs) is the
// pointer-jungle anti-pattern spread across types — flagged, same as a direct self-ref.
func TestRunCLIPointerGraphLintFlagsRawMutualCycle(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "mutual_cycle.elisa", `struct A:
    next: heap B&?

struct B:
    back: heap A&?

def main() -> i64:
    return 0
`)
	if !strings.Contains(out, pointerGraphCycleWarning) {
		t.Fatalf("expected a pointer-graph cycle warning for a raw A<->B cycle, got:\n%s", out)
	}
}

// The same A <-> B cycle made sound with `@owner`/`[@owner]` provenance is a single-region
// graph whose lifetime is one decision — NOT flagged.
func TestRunCLIPointerGraphLintAllowsRegionUnifiedCycle(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "region_cycle.elisa", `struct A[@owner]:
    next: B&? @owner

struct B[@owner]:
    back: A&? @owner

def main() -> i64:
    return 0
`)
	if strings.Contains(out, pointerGraphCycleWarning) {
		t.Fatalf("expected NO cycle warning for an @owner-unified A<->B graph, got:\n%s", out)
	}
}

// `@intrusive` on one node is an acknowledged boundary that breaks the cycle for all
// participants — the loop can only close through the intrusive node, so nothing is flagged.
func TestRunCLIPointerGraphLintIntrusiveBreaksCycle(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "intrusive_cycle.elisa", `@intrusive
struct A:
    next: heap B&?

struct B:
    back: heap A&?

def main() -> i64:
    return 0
`)
	if strings.Contains(out, pointerGraphCycleWarning) {
		t.Fatalf("expected NO cycle warning when one node is @intrusive, got:\n%s", out)
	}
}

// A non-cyclic raw chain (A → B, but B has no ref back) is not a cycle — NOT flagged.
func TestRunCLIPointerGraphLintAllowsAcyclicRawChain(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "acyclic_chain.elisa", `struct A:
    next: heap B&?

struct B:
    value: i64

def main() -> i64:
    return 0
`)
	if strings.Contains(out, pointerGraphCycleWarning) {
		t.Fatalf("expected NO cycle warning for an acyclic A->B raw chain, got:\n%s", out)
	}
}
