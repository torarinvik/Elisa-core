package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Large value payloads used to make every fallible helper allocate an inline
// multi-megabyte error-union workspace on the stack. A short forwarding chain
// was therefore enough to overflow the native stack at -O0. Keep this as an
// end-to-end regression: the successful payload must survive all forwarding
// calls, while the error path must still propagate correctly.
func TestRunCLILargeErrorUnionForwardingDoesNotExhaustStack(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "large_error_union_temp.elisa")
	src := `error BuildErr:
    Rejected

struct LargeResult:
    storage: array[u64, 131072]
    marker: mutable i64

enum Choice:
    First
    Second

def choose_large(choice: Choice) -> LargeResult:
    return match choice:
        Choice.First:
            first: mutable LargeResult = zeroed
            first.marker <- 101
            first
        Choice.Second:
            second: mutable LargeResult = zeroed
            second.marker <- 202
            second

def nested_large_match() -> i64:
    result: LargeResult = choose_large(Choice.Second)
    return result.marker

def leaf(reject: bool) -> LargeResult error[BuildErr]:
    if reject:
        raise BuildErr.Rejected
    result: mutable LargeResult = zeroed
    result.marker <- 73
    return result

def forward1(reject: bool) -> LargeResult error[BuildErr]:
    return try leaf(reject)

def forward2(reject: bool) -> LargeResult error[BuildErr]:
    return try forward1(reject)

def forward3(reject: bool) -> LargeResult error[BuildErr]:
    return try forward2(reject)

def classify(reject: bool) -> i64:
    catch forward3(reject):
        value:
            return value.marker
        BuildErr.Rejected:
            return -1
    return -2

def large_source_locals() -> i64:
    a: mutable LargeResult = zeroed
    b: mutable LargeResult = zeroed
    c: mutable LargeResult = zeroed
    d: mutable LargeResult = zeroed
    e: mutable LargeResult = zeroed
    f: mutable LargeResult = zeroed
    g: mutable LargeResult = zeroed
    h: mutable LargeResult = zeroed
    i: mutable LargeResult = zeroed
    j: mutable LargeResult = zeroed
    a.marker <- 1
    b.marker <- 2
    c.marker <- 3
    d.marker <- 4
    e.marker <- 5
    f.marker <- 6
    g.marker <- 7
    h.marker <- 8
    i.marker <- 9
    j.marker <- 10
    return a.marker + b.marker + c.marker + d.marker + e.marker + f.marker + g.marker + h.marker + i.marker + j.marker

@test
def large_error_union_forwarding_test() -> void:
    can Abort.Panic:
        if classify(false) != 73:
            panic("large successful payload was corrupted")
        if classify(true) != -1:
            panic("large error payload did not propagate")
        if large_source_locals() != 55:
            panic("large source locals were corrupted")
        if nested_large_match() != 202:
            panic("large match-expression result was corrupted")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", "-O0", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("large error-union forwarding test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] large_error_union_forwarding_test") {
		t.Fatalf("expected OK, got:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
