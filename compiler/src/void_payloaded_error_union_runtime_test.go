package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression (runtime): a `void error[E]` union whose error set E carries payloads
// is lowered to the {code, payloads...} struct, not a bare code. Every return path
// (explicit return, implicit end, raise via coerce) must build that struct, and a
// catch must read the payload back. Exercises success, raise-with-payload, and
// payload binding end to end in a compiled binary.
func TestRunCLIVoidPayloadedErrorUnion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "void_payloaded_error_union.elisa")
	src := `error E:
    Plain
    WithAddr(addr: u64)

def act(mode: i64) -> void error[E]:
    if mode == 1:
        raise E.Plain
    if mode == 2:
        raise E.WithAddr(4242)
    return

def run(mode: i64) -> i64:
    catch act(mode):
        n:
            true
        E.Plain:
            return 1
        E.WithAddr(a):
            return a.i64()
    return 0

@test
def void_payloaded_error_union_test() -> void:
    can Abort.Panic:
        if run(0) != 0:
            panic("expected ok -> 0")
        if run(1) != 1:
            panic("expected Plain -> 1")
        if run(2) != 4242:
            panic("expected WithAddr payload -> 4242")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("void-payloaded error union test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] void_payloaded_error_union_test") {
		t.Fatalf("expected OK, got:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
