package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProjectTestPromotesCVariadicArguments(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "test"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "foreign": ["native/varargs_probe.c"],
  "targets": {
    "tests": {
      "entry": "test/varargs_tests.elisa",
      "emit": "llvm",
      "output": "build/varargs_tests.ll",
      "opt": "O0"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "varargs_probe.c"), `#include <stdarg.h>
#include <stdint.h>
#include <string.h>

int64_t elisa_varargs_check_default_promotions(const char *tag, ...) {
    va_list ap;
    va_start(ap, tag);
    int b = va_arg(ap, int);
    int u8 = va_arg(ap, int);
    int i16 = va_arg(ap, int);
    double f32 = va_arg(ap, double);
    int64_t wide = va_arg(ap, int64_t);
    const char *text = va_arg(ap, const char *);
    va_end(ap);
    return tag && strcmp(tag, "case") == 0 && b == 1 && u8 == 7 && i16 == -9 &&
           f32 > 1.49 && f32 < 1.51 && wide == 1234567890123LL && text && strcmp(text, "ok") == 0;
}

int64_t elisa_varargs_check_unpromoted_wide_values(const char *tag, ...) {
    va_list ap;
    va_start(ap, tag);
    unsigned int u32 = va_arg(ap, unsigned int);
    double f64 = va_arg(ap, double);
    uint64_t u64 = va_arg(ap, uint64_t);
    va_end(ap);
    return tag && strcmp(tag, "wide") == 0 && u32 == 0xfedcba98 &&
           f64 > 2.24 && f64 < 2.26 && u64 == 0x1122334455667788ULL;
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "varargs_tests.elisa"), `extern elisa_varargs_check_default_promotions(tag: u8&, ...) -> i64
extern elisa_varargs_check_unpromoted_wide_values(tag: u8&, ...) -> i64

@test
def c_varargs_default_promotions() -> void:
    can Abort.Panic:
        b: bool = true
        small_u: u8 = 7.u8()
        small_s: i16 = -9.i16()
        single: f32 = 1.5.f32()
        wide: i64 = 1234567890123
        text: u8& = "ok"
        if elisa_varargs_check_default_promotions("case", b, small_u, small_s, single, wide, text) != 1:
            panic("C varargs default promotions were not ABI-correct")

@test
def c_varargs_preserves_wide_values() -> void:
    can Abort.Panic:
        u: u32 = 0xfedcba98.u32()
        d: f64 = 2.25
        wide: u64 = 0x1122334455667788.u64()
        if elisa_varargs_check_unpromoted_wide_values("wide", u, d, wide) != 1:
            panic("C varargs widened stable values changed unexpectedly")
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"test", "tests", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected C varargs project test to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	for _, check := range []string{
		"[       OK ] c_varargs_default_promotions",
		"[       OK ] c_varargs_preserves_wide_values",
		"[ SUMMARY  ] 2 test(s) selected; passed=2 skipped=0 failed=0",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
