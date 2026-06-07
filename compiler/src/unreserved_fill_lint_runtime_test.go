package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unreservedFillWarning = "without a matching immediately preceding reserve"

func TestRunCLIUnreservedCountingFillLintFlagsSeparatedFill(t *testing.T) {
	out := compileAndCaptureStderr(t, "unreserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        for i in 0..<n:
            xs.push(i.i64() + gap)
        return xs.count
`)
	if !strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("expected unreserved counting-fill warning, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillLintAllowsAutoReserve(t *testing.T) {
	out := compileAndCaptureStderr(t, "auto_reserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        for i in 0..<n:
            xs.push(i.i64())
        return xs.count
`)
	if strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("auto-reserved adjacent fill must not warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillLintAllowsMatchingExplicitReserve(t *testing.T) {
	out := compileAndCaptureStderr(t, "explicit_reserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        xs.reserve(n)
        for i in 0..<n:
            xs.push(i.i64() + gap)
        return xs.count
`)
	if strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("matching explicit reserve must not warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillLintFlagsMismatchedExplicitReserve(t *testing.T) {
	out := compileAndCaptureStderr(t, "mismatched_reserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.reserve(1)
        for i in 0..<n:
            xs.push(i.i64())
        return xs.count
`)
	if !strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("mismatched explicit reserve must warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillPerfStrictErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unreserved_counting_fill_strict.elisa")
	src := `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        for i in 0..<n:
            xs.push(i.i64() + gap)
        return xs.count
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected -Wperf unreserved counting fill to fail, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), unreservedFillWarning) {
		t.Fatalf("expected -Wperf error to contain %q, got:\n%s", unreservedFillWarning, stderr.String())
	}
}
