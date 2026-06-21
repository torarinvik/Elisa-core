package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Mix-and-match error sets: a function may declare a union that combines tags from
// several error sets (with brace-subsets and payloads), e.g.
// `error[FooError{Bad1}, BarError{Bad3, Bad4}]`. A payload-carrying tag can be
// raised directly into that combined set (the value is built in the destination
// set's layout), and a catch can dispatch across both sets, binding payloads.
func TestRunCLIMixedErrorSets(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed_error_sets.elisa")
	src := `error FooError:
    Bad1
    Bad2

error BarError:
    Bad3
    Bad4(n: u32)

def foo(num: u32) -> i64 error[FooError{Bad1}, BarError{Bad3, Bad4}]:
    if num == 0:
        raise FooError.Bad1
    if num == 1:
        raise BarError.Bad3
    if num == 2:
        raise BarError.Bad4(num)
    return 100

def classify(num: u32) -> i64:
    catch foo(num):
        n:
            return n
        FooError.Bad1:
            return 1
        BarError.Bad3:
            return 3
        BarError.Bad4(x):
            return x.i64()
    return -9

@test
def mixed_error_sets_test() -> void:
    can Abort.Panic:
        if classify(0) != 1:
            panic("expected FooError.Bad1 -> 1")
        if classify(1) != 3:
            panic("expected BarError.Bad3 -> 3")
        if classify(2) != 2:
            panic("expected BarError.Bad4 payload -> 2")
        if classify(5) != 100:
            panic("expected ok -> 100")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("mixed error-set test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] mixed_error_sets_test") {
		t.Fatalf("expected OK, got:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
