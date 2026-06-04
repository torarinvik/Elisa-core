package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pointerGraphWarning = "raw self-referential pointer graph"

func compileAndCaptureStderr(t *testing.T, name, src string) string {
	t.Helper()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, name)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	return stderr.String()
}

// The pointer-graph lint (docs/70): a struct that references itself through a ref field with
// no region provenance — a raw `heap Node&?` or a bare `Node&?` — is the raw linked-node /
// tree-of-pointers anti-pattern and is warned. Friction lands on the slow/unsafe pattern.
func TestRunCLIPointerGraphLintFlagsRawSelfRef(t *testing.T) {
	heapRef := compileAndCaptureStderr(t, "raw_heap.elisa", `struct Node:
    value: i64
    next: heap Node&?

def main() -> i64:
    return 0
`)
	if !strings.Contains(heapRef, pointerGraphWarning) {
		t.Fatalf("expected a pointer-graph warning for a raw heap self-ref, got:\n%s", heapRef)
	}

	bareRef := compileAndCaptureStderr(t, "raw_bare.elisa", `struct Bare:
    value: i64
    next: Bare&?

def main() -> i64:
    return 0
`)
	if !strings.Contains(bareRef, pointerGraphWarning) {
		t.Fatalf("expected a pointer-graph warning for a bare self-ref, got:\n%s", bareRef)
	}
}

// A region-tracked self-ref (`@owner` in a `[region owner]` struct) is a sound single-region
// graph whose whole lifetime is one decision — NOT flagged. Friction only on the raw kind.
func TestRunCLIPointerGraphLintAllowsRegionProvenance(t *testing.T) {
	out := compileAndCaptureStderr(t, "region_graph.elisa", `struct SafeNode[region owner]:
    value: i64
    next: SafeNode&? @owner

def main() -> i64:
    return 0
`)
	if strings.Contains(out, pointerGraphWarning) {
		t.Fatalf("expected NO pointer-graph warning for a @owner-tracked self-ref, got:\n%s", out)
	}
}

// `@intrusive` is the explicit acknowledgment — friction, but possible. An acknowledged raw
// node (the form the runtime's free-lists use) is not warned.
func TestRunCLIPointerGraphLintRespectsIntrusiveAcknowledgment(t *testing.T) {
	out := compileAndCaptureStderr(t, "intrusive.elisa", `@intrusive
struct FreeNode:
    next: heap FreeNode&?

def main() -> i64:
    return 0
`)
	if strings.Contains(out, pointerGraphWarning) {
		t.Fatalf("expected NO pointer-graph warning for an @intrusive struct, got:\n%s", out)
	}
}
