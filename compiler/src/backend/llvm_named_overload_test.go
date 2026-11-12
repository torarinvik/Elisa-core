package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNamedOverloadCodegenUsesSelectedFunction(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	source := `def choose(code: i64, flag: bool) -> i64:
    return code

def choose(value: u64, flag: bool) -> bool:
    return flag

def main() -> i32:
    return 0 if choose(flag: true, value: 7.u64()) else 1
`
	result := parseAndAnalyzeBackendTest(t, "named_overload.elisa", source)
	ir, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "named.ll"), filepath.Join(dir, "named")
	if err := os.WriteFile(input, []byte(ir), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("clang", "-x", "ir", input, "-o", output).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := exec.Command(output).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
}
