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
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "enum_payload_darray_slice.llcontext")
	src := `enum MiniStmt:
    Empty
    Repeat(body: darray[MiniStmt])

def repeat_len(stmt: MiniStmt) -> usize:
    match stmt:
        MiniStmt.Repeat(body):
            view: dview[MiniStmt] = body[0:body.count]
            return view.len
        _:
            return 0

@test
def enum_payload_darray_slice_case() -> void:
    can Memory.Allocate, Abort.Panic:
        arena: Arena = zeroed
        in arena:
            body: darray[MiniStmt] = [MiniStmt.Empty]
            stmt: MiniStmt = MiniStmt.Repeat(body)
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
