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

func TestRunCLIDebugTraceTapeFingerprint(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "debug_trace_tape_fixture.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test helper include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q
# include %q

def record_trace_variant(value: uintptr) -> u64:
    can Debug.Trace, Global.Read, Global.Write:
        debug_trace_reset()
        debug_trace_set_enabled(true)
        debug_trace_checkpoint("entry")
        debug_trace_value("rdi", value)
        debug_trace_boundary("hle", value, 7.uintptr(), 9.uintptr())
        return debug_trace_fingerprint()

@test
def debug_trace_identical_tapes_have_identical_fingerprints() -> void:
    can Abort.Panic, Debug.Trace, Global.Read, Global.Write:
        first: u64 = record_trace_variant(0x100000.uintptr())
        second: u64 = record_trace_variant(0x100000.uintptr())
        assert_eq(first, second)
        assert_eq(debug_trace_count(), 3.usize())
        assert_eq(debug_trace_event_kind_at(0.usize()), DEBUG_TRACE_KIND_CHECKPOINT)
        assert_eq(debug_trace_event_kind_at(2.usize()), DEBUG_TRACE_KIND_BOUNDARY)
        assert_eq(debug_trace_event_a_at(2.usize()), 0x100000.uintptr())

@test
def debug_trace_value_divergence_changes_fingerprint() -> void:
    can Abort.Panic, Debug.Trace, Global.Read, Global.Write:
        first: u64 = record_trace_variant(0x100000.uintptr())
        second: u64 = record_trace_variant(0x100008.uintptr())
        assert_ne(first, second)
`, runtimeInclude, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write debug trace tape fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected debug trace tape test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), fixturePath+":") {
		t.Fatalf("expected no fixture diagnostics, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] debug_trace_identical_tapes_have_identical_fingerprints",
		"[       OK ] debug_trace_identical_tapes_have_identical_fingerprints",
		"[ RUN      ] debug_trace_value_divergence_changes_fingerprint",
		"[       OK ] debug_trace_value_divergence_changes_fingerprint",
		"[ SUMMARY  ] 2 test(s) selected; passed=2 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected debug trace tape output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLINativeRuntimeConcatShortStringRegression(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "native_runtime_concat_short_string_regression.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def make_queue_label(vqid: int) -> cstr:
	can Memory.Allocate, Abort.Panic, Console.Format, Global{Read, Write}, Atomics{Exchange, Store}:
		queue: cstr = "GFX" if vqid > 254 else "ASC"
		prefix: cstr = rt_concat2_scratch(queue, "[")
		numbered: cstr = rt_concat2_scratch(prefix, rt_int_to_string((vqid %% 260).i64()))
		return rt_concat2_scratch(numbered, "]")

def make_queue_label_with_suffix(vqid: int, suffix: cstr) -> cstr:
	can Memory.Allocate, Abort.Panic, Console.Format, Global{Read, Write}, Atomics{Exchange, Store}:
		return rt_concat2_scratch(make_queue_label(vqid), suffix)

@test
def short_concat_finish_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format, Global{Read, Write}, Atomics{Exchange, Store}:
		for i in 0..<20000:
			label: cstr = make_queue_label((i %% 260).int())
			assert ctx_strlen(label) >= 6
			assert ctx_strlen(label) <= 8

@test
def short_concat_mixed_inputs_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format, Global{Read, Write}, Atomics{Exchange, Store}:
		for i in 0..<24000:
			slot: int = (i %% 5).int()
			vqid: mutable int = 0
			expected: mutable cstr = ""
			if slot == 0:
				vqid <- 0
				expected <- "ASC[0]"
			if slot == 1:
				vqid <- 9
				expected <- "ASC[9]"
			if slot == 2:
				vqid <- 99
				expected <- "ASC[99]"
			if slot == 3:
				vqid <- 100
				expected <- "ASC[100]"
			if slot == 4:
				vqid <- 259
				expected <- "GFX[259]"

			plain: cstr = make_queue_label_with_suffix(vqid, "")
			assert plain == expected
			assert ctx_strlen(plain) >= 6
			assert ctx_strlen(plain) <= 8

@test
def short_concat_cross_path_stress_regression() -> void:
	can Abort.Panic, Memory.Allocate, Console.Format, Global{Read, Write}, Atomics{Exchange, Store}:
		total: mutable i64 = 0
		for i in 0..<16000:
			base: cstr = make_queue_label((i %% 260).int())
			with_colon: cstr = rt_concat2_scratch(base, ":")
			label: cstr = rt_concat2_scratch(with_colon, rt_int_to_string((ctx_strlen(base) + (i %% 11).i64()).i64()))
			assert ctx_strlen(label) >= 8
			assert ctx_strlen(label) <= 12
			total += ctx_strlen(label)
		assert total > 0
	`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write concat regression fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected concat regression test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] short_concat_finish_regression",
		"[       OK ] short_concat_finish_regression",
		"[ RUN      ] short_concat_mixed_inputs_regression",
		"[       OK ] short_concat_mixed_inputs_regression",
		"[ RUN      ] short_concat_cross_path_stress_regression",
		"[       OK ] short_concat_cross_path_stress_regression",
		"[ SUMMARY  ] 3 test(s) selected; passed=3 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected concat regression output to contain %q, got:\n%s", check, output)
		}
	}
}
