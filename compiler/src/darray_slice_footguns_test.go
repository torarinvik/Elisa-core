package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIExecutesEnumPayloadDArraySlice(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "enum_payload_darray_slice.elisa")
	// MiniStmt is recursive only THROUGH its darray payload (Repeat holds darray[MiniStmt]). Per the
	// computeRecursiveEnumSet container-descent rule this promotes to the AoS handle store (docs/76:
	// flat node storage, the darray is a side-table of handles), so nodes are built with `new` in a
	// region-poly builder and the darray child is adopted across the return. Slicing the payload darray
	// (the footgun this test guards) still works: the slice is over a view of handles.
	src := `enum MiniStmt:
    Empty
    Repeat(body: darray[MiniStmt])

def repeat_len(stmt: MiniStmt) -> usize:
    match stmt:
        MiniStmt.Repeat(body):
            view: view[MiniStmt] = body[0:body.count]
            return view.len
        _:
            return 0

def build_repeat() -> MiniStmt:
    can Memory.Allocate, Abort.Panic:
        body: mutable darray[MiniStmt] = []
        body.push(new MiniStmt.Empty)
        return new MiniStmt.Repeat(body)

@test
def enum_payload_darray_slice_case() -> void:
    can Memory.Allocate, Abort.Panic:
        stmt: MiniStmt = build_repeat()
        if repeat_len(stmt) != 1:
            panic("bad enum payload darray slice")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write enum payload darray slice fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected enum payload darray slice execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] enum_payload_darray_slice_case",
		"[       OK ] enum_payload_darray_slice_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enum payload darray slice output to contain %q, got:\n%s", check, output)
		}
	}
}
