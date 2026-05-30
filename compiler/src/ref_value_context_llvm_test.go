package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsRefValueContextsToLLVM(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "ref_value_context_llvm.elisa", `def next(index: mutable usize&) -> usize:
    if index >= 2:
        return index + 1
    index <- index + 1
    return index

def read(text: static u8&) -> u8:
    ch: mutable u8 = 0
    ch <- text[0]
    return ch
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected ref value-context llvm fixture to succeed, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i64 @next", "define i8 @read"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llvm output to contain %q, got:\n%s", check, output)
		}
	}
}

// An IMMUTABLE scalar reference in +/- arithmetic must use the referent VALUE,
// not the pointer address. Previously only mutable scalar refs were deref'd, so
// `r + n` for `r: i64&` (e.g. the binding from `for ref x in nums`) silently did
// pointer arithmetic on the address — a silent wrong-result. Byte-correct check.
func TestRunCLIImmutableScalarRefArithmeticUsesValue(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "ref_scalar_arith.elisa")
	src := `def add_via_ref(r: i64&, n: i64) -> i64:
    return r + n

@test
def ref_scalar_arith_test() -> void:
    can Memory.Allocate, Abort.Panic:
        x: mutable i64 = 10
        if add_via_ref(x.ref[i64&], 20) != 30:
            panic("immutable i64& + n must be value arithmetic (expected 30)")
        arena: mutable Arena = zeroed
        in arena:
            nums: mutable darray[i64] = []
            nums.push(3)
            nums.push(4)
            nums.push(5)
            sum: mutable i64 = 0
            for ref v in nums:
                sum <- sum + v
            if sum != 12:
                panic("for ref scalar sum must be value arithmetic (expected 12)")
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write ref-scalar-arith fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected ref-scalar-arith test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[       OK ] ref_scalar_arith_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected ref-scalar-arith output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
