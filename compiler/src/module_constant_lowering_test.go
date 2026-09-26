package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLLVMUsesResolvedNameForUnqualifiedModuleConstant(t *testing.T) {
	src := `module Limits:
    public:
        const SCRATCH_BYTES: usize = 4096

using Limits

def main() -> i64:
    return SCRATCH_BYTES.i64()
`
	dir := t.TempDir()
	path := filepath.Join(dir, "module_constant.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	objectPath := filepath.Join(dir, "module_constant.o")
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "obj", "-o", objectPath, path}, &stdout, &stderr); code != 0 {
		t.Fatalf("LLVM lowering failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if info, err := os.Stat(objectPath); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty object after lowering imported module constant, stat=%v", err)
	}
}
