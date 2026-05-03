package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsSelectedTestPhaseDebug(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	t.Setenv("LLCONTEXT_TEST_PHASE_DEBUG", "1")

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_tests_phase_debug_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write phase-debug execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected phase-debug test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"[ phase    ] emit_test selected_test_execution",
		"[ phase    ] selected_tests read_source",
		"[ phase    ] selected_tests select_cases",
		"[ phase    ] selected_tests compile_dispatch",
		"[ phase    ] selected_tests run_cases",
	} {
		if !strings.Contains(stderr.String(), check) {
			t.Fatalf("expected selected-test phase debug output to contain %q, got:\n%s", check, stderr.String())
		}
	}
	if !strings.Contains(stdout.String(), "[       OK ] alpha_case") {
		t.Fatalf("expected successful test output, got:\n%s", stdout.String())
	}
}
func TestRunCLIExecutesFilteredSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_filtered_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write filtered execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected filtered test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "[ RUN      ] alpha_case") {
		t.Fatalf("expected filtered execution not to run alpha_case, got:\n%s", output)
	}
	for _, check := range []string{
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 1 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected filtered execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIExecutesSelectedTestsWithGlobFilter(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_glob_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write glob execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "*beta*", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected glob-filtered test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "alpha_case") {
		t.Fatalf("expected glob-filtered execution not to mention alpha_case, got:\n%s", output)
	}
	for _, check := range []string{
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected glob-filtered execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIContinuesAfterFailingAndSkippedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_fail_skip_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    can Abort.Panic:\n        panic(\"boom\")\n\n@skip(todo)\n@test\ndef beta_case() -> void:\n    pass\n\n@test\ndef gamma_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fail/skip execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected fail/skip test execution to return non-zero, stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected harness stderr to stay empty, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"PANIC",
		"[ ACTIVE   ] alpha_case",
		"alpha_case",
		"panic at ",
		"backtrace:",
		"[ SKIPPED  ] beta_case (todo)",
		"[ RUN      ] gamma_case",
		"[       OK ] gamma_case",
		"[ SUMMARY  ] 3 test(s) selected; passed=1 skipped=1 failed=1",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected richer test harness output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIExecutesTupleMatchStatement(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "tuple_match_execute_fixture.llcontext")
	src := "@test\ndef tuple_match_selects_literal_arm() -> void:\n    match 5, 'w', 'h', 'i', 'l', 'e':\n        5, 'w', 'h', 'i', 'l', 'e':\n            return\n        _:\n            can Abort.Panic:\n                panic(\"tuple match fallback\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write tuple match execute fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected tuple match test execution to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] tuple_match_selects_literal_arm",
		"[       OK ] tuple_match_selects_literal_arm",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tuple match execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLICompilesPanicToBacktraceAwareLLVM(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "panic_backtrace_fixture.llcontext")
	src := "def main() -> int:\n    can Abort.Panic:\n        panic(\"boom\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write panic backtrace fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected panic backtrace LLVM emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"declare i64 @printf(ptr, ...)",
		"declare i64 @backtrace(ptr, i64)",
		"declare void @backtrace_symbols_fd(ptr, i64, i64)",
		"declare void @abort()",
		"call i64 (ptr, ...) @printf(",
		"call i64 @backtrace(ptr",
		"call void @backtrace_symbols_fd(ptr",
		"call void @abort()",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected panic LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIReturnsNonZeroWhenNoTestsMatchExecutionFilter(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_no_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write no-match execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected no-match test execution to fail, stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ NO TESTS ] no @test functions matched filter \"beta\"") {
		t.Fatalf("expected no-tests execution output, got:\n%s", stdout.String())
	}
}
func looksLikeObjectFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magics := [][]byte{
		{0x7f, 'E', 'L', 'F'},
		{0xcf, 0xfa, 0xed, 0xfe},
		{0xce, 0xfa, 0xed, 0xfe},
		{0xfe, 0xed, 0xfa, 0xcf},
		{0xfe, 0xed, 0xfa, 0xce},
	}
	for _, magic := range magics {
		if bytes.Equal(data[:4], magic) {
			return true
		}
	}
	return false
}
func looksLikeBitcodeFile(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return bytes.HasPrefix(data, []byte{'B', 'C'}) || bytes.Equal(data[:4], []byte{0xde, 0xc0, 0x17, 0x0b})
}
func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
