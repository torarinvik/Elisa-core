package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImplicitContextFixture(t *testing.T, name string, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture %s: %v", name, err)
	}
	return path
}

func TestRunCLIInterpretsImplicitContextGenericCalls(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "implicit_context_interpret.llcontext", `context MathCtx:
    offset: i64

def add_offset(x: i64) with MathCtx -> i64:
    return x + offset

def generic_id[T](value: T) with MathCtx -> T:
    return value

def add_twice(x: i64) with MathCtx -> i64:
	return add_offset(add_offset(x))

def main() -> i64:
    offset: i64 = 10
    with MathCtx(..):
		base: i64 = add_twice(5)
        return generic_id[i64](base)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected implicit-context interpret fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 25") {
		t.Fatalf("expected interpreter output to report result 25, got:\n%s", stdout.String())
	}
}

func TestRunCLIFmtNormalizesLegacySpecializeAndWithSurface(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "implicit_context_fmt.llcontext", `context MathCtx:
    offset: i64

def generic_id[T](value: T) with MathCtx -> T:
    return value

def main() -> i64:
    offset: i64 = 35
    return generic_id.specialize[i64]()(offset) with MathCtx(..)
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected fmt to succeed, stderr:\n%s", stderr.String())
	}
	formatted := stdout.String()
	if strings.Contains(formatted, ".specialize[") {
		t.Fatalf("expected formatted output to normalize legacy specialize syntax, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "generic_id[i64](offset) with MathCtx(..)") {
		t.Fatalf("expected formatted output to contain normalized generic call with trailing bundle, got:\n%s", formatted)
	}
}

func TestRunCLIRejectsExportTargetsWithImplicitParams(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "implicit_context_export_fail.llcontext", `context MathCtx:
    offset: i64

def helper(x: i64) with MathCtx -> i64:
    return x + offset

export func helper_export(x: i64) -> i64 = helper
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected export target with implicit params to fail")
	}
	if !strings.Contains(stderr.String(), "must not have implicit parameters in v1") {
		t.Fatalf("expected export rejection to mention implicit params, got:\n%s", stderr.String())
	}
}

func TestRunCLIRejectsCastHooksWithImplicitParams(t *testing.T) {
	sourcePath := writeImplicitContextFixture(t, "implicit_context_cast_fail.llcontext", `context CastCtx:
    offset: i64

def __cast__(x: i64) with CastCtx -> i64:
    return x + offset
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected implicit-parameter cast hook to fail")
	}
	if !strings.Contains(stderr.String(), "must not declare implicit parameters in v1") {
		t.Fatalf("expected cast-hook rejection to mention implicit params, got:\n%s", stderr.String())
	}
}
