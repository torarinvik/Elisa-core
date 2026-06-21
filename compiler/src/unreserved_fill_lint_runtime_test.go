package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unreservedFillWarning = "without a matching immediately preceding reserve"

func TestRunCLIUnreservedCountingFillLintAllowsSemanticAutoReserve(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "unreserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        gap: i64 = 0
        for i in 0..<n:
            xs.push(i.i64() + gap)
        return xs.count
`)
	if strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("semantic auto-reserved fill must not warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillLintAllowsAutoReserve(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRunCLIUnreservedCountingFillLintAllowsSufficientExplicitReserve(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "sufficient_reserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.reserve(n + n)
        for i in 0..<n:
            xs.push(i.i64())
        return xs.count
`)
	if strings.Contains(out, unreservedFillWarning) {
		t.Fatalf("sufficient explicit reserve must not warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillLintAllowsMultiTargetSemanticAutoReserve(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "multi_auto_reserved_counting_fill.elisa", `def builds(n: usize) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        ys: mutable darray[i64] = []
        for i in 0..<n:
            xs.push(i.i64())
            ys.push([i.i64(), i.i64()])
        return xs.count + ys.count
`)
	if strings.Contains(out, unreservedFillWarning) || strings.Contains(out, "cannot infer a safe reserve bound") {
		t.Fatalf("multi-target semantic auto-reserved fill must not warn, got:\n%s", out)
	}
}

func TestRunCLIUnreservedCountingFillPerfStrictErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "unreserved_counting_fill_strict.elisa")
	src := `def builds(src: darray[darray[i64]]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        for chunk in src:
            for x in chunk:
                xs.push(x)
        return xs.count
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-Wperf", "-emit", "llvm", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected -Wperf unreserved counting fill to fail, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot infer a safe reserve bound") {
		t.Fatalf("expected -Wperf error to contain uninferred reserve bound warning, got:\n%s", stderr.String())
	}
}
