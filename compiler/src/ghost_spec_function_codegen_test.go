//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGhostFunctionErasedFromLLVM(t *testing.T) {
	src := `
ghost def abs(x: i64) -> i64:
    if x < 0:
        return -x
    return x

def keep(x: i64) -> i64:
    return x
`
	path := filepath.Join(t.TempDir(), "ghost_fn_codegen.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr strings.Builder
	if code := runCLI([]string{"-emit", "llvm", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "define i64 @abs") || strings.Contains(stdout.String(), "declare i64 @abs") {
		t.Fatalf("ghost function was emitted in LLVM:\n%s", stdout.String())
	}
}
