package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Regression (runtime) for the ref-param method-call codegen bug fixed in
// 8db14b00: calling a numeric-conversion method directly on a `mutable T&`
// reference parameter (`off.u64()`) used to run on the pointer bits and trap at
// runtime. The backend IR test (TestGenerateLLVMIRDereferencesRefForNumericConversion)
// covers IR emission; this one proves the compiled binary computes the value and
// does not abort.
func TestRunCLIRefParamMethodCallDoesNotTrap(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ref_param_method_call.elisa")
	src := `def doubleRef(off: mutable usize&) -> u64:
    return off.u64() * 2

@test
def ref_param_method_call_test() -> void:
    can Abort.Panic:
        x: mutable usize = 21
        if doubleRef(x) != 42:
            panic("ref-param .u64() miscompiled")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("ref-param method-call test failed, exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] ref_param_method_call_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected %q, got:\n%s\nstderr:\n%s", check, stdout.String(), stderr.String())
		}
	}
}
