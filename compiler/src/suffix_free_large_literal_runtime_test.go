package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// A suffixless integer literal exceeding i64 max (the FNV-1a 64-bit basis) must compile when the
// declared type is a 64-bit unsigned type — the `: u64` annotation stands in for the `u64` suffix.
// This is the exact case that broke the Wolf3D port after numeric suffixes were stripped: a `global`
// with a >i64 value lowered through the strict const/global codegen path and was rejected with
// "invalid integer literal". Locals already worked; this brings const/global to parity.
func compileSuffixFree(t *testing.T, prog string) (bool, string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	fx := filepath.Join(dir, "lit.elisa")
	if err := os.WriteFile(fx, []byte(prog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fx, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, result, ok := analyzeProgram(fx, expanded, &stderr)
	if !ok {
		return false, strings.TrimSpace(stderr.String())
	}
	_, cleanup, err := buildNativeExecutable(result, nil, nil, "", backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return false, strings.TrimSpace(stderr.String())
	}
	return true, ""
}

func TestSuffixFreeLargeLiteralUnsignedGlobalCompiles(t *testing.T) {
	t.Parallel()
	for _, prog := range []string{
		"global FNV: u64 = 0xcbf29ce484222325\ndef main() -> int can[Console.Write]:\n    return FNV.i32()\n",
		"const X: u64 = 0xcbf29ce484222325\ndef main() -> int can[Console.Write]:\n    return X.i32()\n",
		"const Y: usize = 0xffffffffffffffff\ndef main() -> int can[Console.Write]:\n    return Y.i32()\n",
	} {
		if ok, diag := compileSuffixFree(t, prog); !ok {
			t.Fatalf("suffixless large literal in an unsigned-typed const/global must compile, got:\n%s\nprogram:\n%s", diag, prog)
		}
	}
}

// Soundness: the relaxation is gated on an UNSIGNED 64-bit target. A signed const/global whose value
// exceeds i64 max must still be rejected (overflow stays an error), not silently wrapped.
func TestSuffixFreeLargeLiteralSignedStillRejected(t *testing.T) {
	t.Parallel()
	for _, prog := range []string{
		"const X: i64 = 0xffffffffffffffff\ndef main() -> int can[Console.Write]:\n    return X.i32()\n",
		"global X: i64 = 0xffffffffffffffff\ndef main() -> int can[Console.Write]:\n    return X.i32()\n",
	} {
		if ok, _ := compileSuffixFree(t, prog); ok {
			t.Fatalf("a >i64 value in a SIGNED const/global must be rejected, but it compiled:\n%s", prog)
		}
	}
}
