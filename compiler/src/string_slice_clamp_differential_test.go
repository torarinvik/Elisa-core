package main

import (
	"bytes"
	"strings"
	"testing"
)

// Plain string slices clamp (runtime string_view_slice / sview): an inverted or
// out-of-range slice is an empty or shortened view, never a trap. The
// interpreter used to trap on text[3:1] while compiled code returned "", and
// the SMT view-length model assumes the clamp. Both paths must return 42.
const stringSliceClampProgram = `def main() -> i64:
    text: sview = "abcdef"
    lit: cstr = "abcdef"
    lo: i64 = 3
    hi: i64 = 1
    if text[lo:hi] != "":
        return 1
    if text[4:99] != "ef" or text[99:100] != "":
        return 2
    if text[1:3] != "bc" or text[0:6] != "abcdef":
        return 3
    if lit[lo:hi] != "" or lit[2:4] != "cd":
        return 4
    return 42
`

func TestStringSliceClampCompiledRuntime(t *testing.T) {
	status, out := s4CompileRun(t, stringSliceClampProgram)
	if status != "RUNERR" || !strings.Contains(out, "exit status 42") {
		t.Fatalf("expected exit 42, got %s: %s", status, out)
	}
}

func TestStringSliceClampInterpreted(t *testing.T) {
	t.Parallel()
	sourcePath := writeImplicitContextFixture(t, "string_slice_clamp_interpret.elisa", stringSliceClampProgram)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("interpret failed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected result 42, got:\n%s", stdout.String())
	}
}
