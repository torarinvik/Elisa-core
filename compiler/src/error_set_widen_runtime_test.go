package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Cross-set `try`-widening with payloads: a function returning a sub-set error
// union (error[IoErr], with a payload tag) is `try`-ed from a function returning a
// wider combined union (error[IoErr, NetErr]). The error code is remapped into the
// combined set's code space AND the active tag's payload is relocated into the
// combined set's struct layout, so the payload survives the widen.
func TestRunCLIErrorSetWidenWithPayload(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "error_set_widen.elisa")
	src := `error IoErr:
    Bad
    WithAddr(a: u64)

error NetErr:
    Down(code: u32)

def sub(n: u32) -> i64 error[IoErr]:
    if n == 0:
        raise IoErr.Bad
    if n == 1:
        raise IoErr.WithAddr(777)
    return 5

def caller(n: u32) -> i64 error[IoErr, NetErr]:
    if n == 9:
        raise NetErr.Down(404)
    v: i64 = try sub(n)
    return v * 2

def classify(n: u32) -> i64:
    catch caller(n):
        ok:
            return ok
        IoErr.Bad:
            return 1
        IoErr.WithAddr(a):
            return a.i64()
        NetErr.Down(c):
            return c.i64()
    return -1

@test
def error_set_widen_test() -> void:
    can Abort.Panic:
        if classify(0) != 1:
            panic("expected IoErr.Bad -> 1")
        if classify(1) != 777:
            panic("expected IoErr.WithAddr payload to survive widen -> 777")
        if classify(2) != 10:
            panic("expected ok path -> 10")
        if classify(9) != 404:
            panic("expected NetErr.Down payload -> 404")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("error-set widen test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] error_set_widen_test") {
		t.Fatalf("expected OK, got:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
