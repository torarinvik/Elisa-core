package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end region inference: a function body with region-less allocations gets a
// synthesized auto region that owns every allocation and is freed (O(1)) at scope exit
// (docs/68; `in auto:` was hard-removed in favor of this default). A darray grown to
// 1000 elements holds correct data and the program runs clean — no explicit region
// declaration, block, or `@r` annotation needed.
func TestRunCLIInferredRegionSynthesizesScopedRegion(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "inferred_region_runtime_fixture.elisa")
	src := `@test
def inferred_region_runtime_test() -> void:
    can Abort.Panic, Memory.Allocate:
        xs: mutable darray[i64] = []
        for i in 0..<1000:
            xs.push(i.i64())
        if xs[0] != 0 or xs[999] != 999:
            panic("auto region: data wrong")
        if xs.count != 1000:
            panic("auto region: count wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write inferred-region fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected inferred-region runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "error") {
		t.Fatalf("unexpected error on stderr:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] inferred_region_runtime_test",
		"passed=1",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected inferred-region output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

// A container allocated in a callee, stored into a struct FIELD, and returned with the struct
// must outlive the call: the callee has to thread the caller's region (`__region_auto`) rather
// than allocate into a per-call arena it frees before `ret`.
//
// This is a runtime test because the failure was silent. The classifier recorded a local as
// region-backed only when the assignment target was a bare ident, so `board.cells <- zeros(n)`
// left `board` unclassified; `board_new` got a local `__auto_*` region, called `arena_free` on
// it, and returned a struct whose buffer pointer aimed into the freed arena. It compiled with no
// diagnostic, `.count` read back fine (the header is copied by value), and only element reads
// segfaulted. The struct-literal spelling of the same function was already classified correctly,
// so the two spellings disagreed — which is what made it a bug and not a policy choice.
func TestRunCLIStructFieldAssignedContainerSurvivesReturn(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "struct_field_escape_fixture.elisa")
	src := `struct Board:
    cells: mutable darray[i64]

def zeros(n: i64) -> darray[i64]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[i64] = []
        for _i in 0..<n:
            out.push(0)
        return out

# A zeroed struct plus field assignment, NOT a struct literal: the shape that used to dangle.
def board_new(n: i64) -> Board:
    can Memory.Allocate, Abort.Panic:
        board: mutable Board = zeroed
        board.cells <- zeros(n)
        return board

@test
def struct_field_escape_test() -> void:
    can Abort.Panic, Memory.Allocate:
        b: mutable Board = board_new(64)
        if b.cells.count != 64:
            panic("field-assigned container: count wrong")
        # The read that segfaulted: the buffer, not the by-value header.
        b.cells[7] <- 9
        b.cells[63] <- 5
        if b.cells[7] != 9 or b.cells[63] != 5 or b.cells[0] != 0:
            panic("field-assigned container: data wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write struct-field-escape fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected struct-field-escape runtime test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] struct_field_escape_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
