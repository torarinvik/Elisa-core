package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Generic protocol-method dispatch through a by-REFERENCE receiver (`m: mutable M&`)
// calling a protocol method whose `self` is by VALUE (`self: Self`). The semantic layer
// accepts this (rewriteBoundTypeParamMethodCall unwraps the receiver reference), but the
// BACKEND previously passed the receiver pointer straight to the by-value parameter,
// tripping LLVM's verifier ("Call parameter type does not match function signature").
// emitCallArg now auto-dereferences a reference argument fed to a by-value struct param.
func TestRunCLIBoundTypeParamRefToValueSelfDispatch(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "bound_typeparam_ref_dispatch_fixture.elisa")
	src := `protocol Reader:
    def val(self: Self) -> u64

struct Box:
    n: u64

impl Reader for Box:
    def val(self: Self) -> u64:
        return self.n

# generic by-ref receiver calling a by-value-self protocol method
def get[R: Reader](r: mutable R&) -> u64:
    return r.val()

@test
def bound_typeparam_ref_dispatch_test() -> void:
    can Abort.Panic:
        b: mutable Box = Box{n: 7}
        if get(&b) != 7:
            panic("by-ref to value-self dispatch wrong")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected by-ref value-self dispatch test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"[       OK ] bound_typeparam_ref_dispatch_test", "passed=1"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
