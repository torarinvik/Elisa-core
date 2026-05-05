package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProvidesDefaultNativeRuntimeHelpersForSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "native_runtime_helpers_fixture.llcontext")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "test.llcontext")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

struct ProbeToken:
	kind: i64

def probe_keyword_hit(text: cstr) -> bool:
	return text == "program"

def probe_first_scalar(owner: mutable Arena&) -> i64:
	in owner:
		values: darray[i64] = [11, 22]
		return values[0u]

@test
def keyword_compare_test() -> void:
	can Abort.Panic:
		assert_eq(probe_keyword_hit("program"), true)

@test
def scalar_array_index_test() -> void:
	can Abort.Panic:
		region scratch(4096)
		assert_eq(probe_first_scalar(scratch.ref[mutable Arena&]), 11)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write native runtime helpers fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected native runtime helper test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] keyword_compare_test",
		"[       OK ] keyword_compare_test",
		"[ RUN      ] scalar_array_index_test",
		"[       OK ] scalar_array_index_test",
		"[ SUMMARY  ] 2 test(s) selected; passed=2 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected native runtime helper output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestNativeRuntimeSupportHandlesEmptyViews(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	workDir := t.TempDir()
	harnessPath := filepath.Join(workDir, "empty_view_harness.c")
	binaryPath := filepath.Join(workDir, "empty_view_harness")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "native_runtime_support.c")
	src := fmt.Sprintf(`#include <stdint.h>
#include <stdlib.h>
#include %q

int main(void) {
    StringView empty_text = { NULL, 0 };
    StringView empty_text_slice = ctx_string_view_slice(empty_text, 1, 9);
    if (empty_text_slice.data != NULL || empty_text_slice.len != 0) {
        return 1;
    }
    if (ctx_string_view_index(empty_text_slice, 0) != 0) {
        return 2;
    }
    uint8_t *copy = ctx_string_from_view(empty_text_slice);
    if (copy == NULL || copy[0] != 0) {
        return 3;
    }
    free(copy);
    StringView invalid_text = { NULL, 7 };
    StringView invalid_text_slice = ctx_string_view_slice(invalid_text, 0, 7);
    if (invalid_text_slice.data != NULL || invalid_text_slice.len != 0) {
        return 4;
    }
    copy = ctx_string_from_view(invalid_text);
    if (copy == NULL || copy[0] != 0) {
        return 5;
    }
    free(copy);
    DynArrayView empty_values = arena_da_view(NULL, -4, -8);
    DynArrayView empty_values_slice = arena_da_view_slice(empty_values, 1, 9);
    if (empty_values_slice.data != NULL || empty_values_slice.len != 0 || empty_values_slice.elem_size != 0) {
        return 6;
    }
    DynArrayView invalid_values = { NULL, 7, 8 };
    DynArrayView invalid_values_slice = arena_da_view_slice(invalid_values, 0, 7);
    if (invalid_values_slice.data != NULL || invalid_values_slice.len != 0 || invalid_values_slice.elem_size != 8) {
        return 7;
    }
    return 0;
}
`, runtimePath)
	if err := os.WriteFile(harnessPath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write native runtime harness: %v", err)
	}
	if out, err := exec.Command("clang", harnessPath, "-o", binaryPath).CombinedOutput(); err != nil {
		t.Fatalf("failed to compile native runtime harness: %v\n%s", err, out)
	}
	if out, err := exec.Command(binaryPath).CombinedOutput(); err != nil {
		t.Fatalf("native runtime harness failed: %v\n%s", err, out)
	}
}
