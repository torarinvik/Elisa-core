package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"llcontext/src/ast"
	"llcontext/src/backend"
	"llcontext/src/semantic"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func selectedTestPermissionRefs(tests []*semantic.AnnotatedFunc) []ast.PermissionRef {
	refs := []ast.PermissionRef{{Name: "Console", Member: "Write"}}
	seen := map[string]bool{"Console.Write": true}
	for _, testFn := range tests {
		if testFn == nil || testFn.Signature == nil {
			continue
		}
		fnRefs := testFn.Signature.PermissionRefs
		if len(fnRefs) == 0 {
			for _, family := range testFn.Signature.Permissions {
				fnRefs = append(fnRefs, ast.PermissionRef{Name: family})
			}
		}
		for _, ref := range fnRefs {
			key := semantic.PermissionRefString(ref)
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, ref)
		}
	}
	return refs
}

type selectedTestCase struct {
	Func       *semantic.AnnotatedFunc
	SkipReason string
}

func (tc selectedTestCase) skipped() bool {
	return strings.TrimSpace(tc.SkipReason) != ""
}

func selectTestCases(result *semantic.Result, filter string) []selectedTestCase {
	selected := selectAnnotatedFunctions(result, "test", filter)
	if len(selected) == 0 {
		return nil
	}
	cases := make([]selectedTestCase, 0, len(selected))
	for _, fn := range selected {
		skipReason, _ := skipReasonForAnnotatedFunc(fn)
		cases = append(cases, selectedTestCase{Func: fn, SkipReason: skipReason})
	}
	return cases
}

func skipReasonForAnnotatedFunc(fn *semantic.AnnotatedFunc) (string, bool) {
	if fn == nil {
		return "", false
	}
	for _, annotation := range fn.Annotations {
		switch strings.ToLower(strings.TrimSpace(annotation.Name)) {
		case "skip", "ignore":
			reason := strings.TrimSpace(strings.Join(annotation.Args, ", "))
			if reason == "" {
				reason = "annotation-requested"
			}
			return reason, true
		}
	}
	return "", false
}

func matchesNameFilter(name string, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	name = strings.ToLower(strings.TrimSpace(name))
	patterns := strings.Split(strings.ToLower(filter), ",")
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.ContainsAny(pattern, "*?[") {
			matched, err := path.Match(pattern, name)
			if err == nil && matched {
				return true
			}
			continue
		}
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

func selectAnnotatedFunctions(result *semantic.Result, annotationName string, filter string) []*semantic.AnnotatedFunc {
	if result == nil || annotationName == "" {
		return nil
	}
	selected := make([]*semantic.AnnotatedFunc, 0)
	for _, fn := range result.AnnotatedFuncs {
		if fn == nil || !hasAnnotation(fn, annotationName) {
			continue
		}
		if !matchesNameFilter(fn.Name, filter) {
			continue
		}
		selected = append(selected, fn)
	}
	return selected
}

func generateTestRunnerSource(inputFile string, result *semantic.Result, filter string) (string, error) {
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		return "", err
	}
	return buildTestRunnerSource(source, selectTestCases(result, filter), filter), nil
}

func buildTestRunnerSource(source []byte, cases []selectedTestCase, filter string) string {
	runnable := runnableTests(cases)
	skipped := countSkippedTests(cases)

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	if !strings.Contains(string(source), "extern puts(") {
		out.WriteString("extern puts(text: any u8&) -> int can[Console.Write]\n\n")
	}
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(selectedTestPermissionRefs(runnable)))
	out.WriteString(":\n")

	if len(cases) == 0 {
		message := llcontextStringLiteral(fmt.Sprintf("[ NO TESTS ] no @test functions matched filter %q", strings.TrimSpace(filter)))
		out.WriteString("\tputs(")
		out.WriteString(message)
		out.WriteString(" -> any u8&)\n")
		out.WriteString("\treturn 1\n\n")
		out.WriteString("export func main() -> int = ctx_test_main\n")
		return out.String()
	}

	for _, testCase := range cases {
		if testCase.Func == nil {
			continue
		}
		if testCase.skipped() {
			skippedLine := llcontextStringLiteral(formatTestLine("SKIPPED", testCase.Func.Name, fmt.Sprintf(" (%s)", testCase.SkipReason)))
			out.WriteString("\tputs(")
			out.WriteString(skippedLine)
			out.WriteString(" -> any u8&)\n")
			continue
		}
		runLine := llcontextStringLiteral(formatTestLine("RUN", testCase.Func.Name, ""))
		okLine := llcontextStringLiteral(formatTestLine("OK", testCase.Func.Name, ""))
		out.WriteString("\tputs(")
		out.WriteString(runLine)
		out.WriteString(" -> any u8&)\n")
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()\n")
		out.WriteString("\tputs(")
		out.WriteString(okLine)
		out.WriteString(" -> any u8&)\n")
	}

	summaryLine := llcontextStringLiteral(fmt.Sprintf("[ SUMMARY  ] %d test(s) selected; runnable=%d skipped=%d failed=0", len(cases), len(runnable), skipped))
	out.WriteString("\tputs(")
	out.WriteString(summaryLine)
	out.WriteString(" -> any u8&)\n")
	out.WriteString("\treturn 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}

func buildIsolatedTestRunnerSource(source []byte, testCase selectedTestCase) string {
	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	out.WriteString("def ctx_test_main() -> int")
	out.WriteString(semantic.PermissionRefsString(selectedTestPermissionRefs([]*semantic.AnnotatedFunc{testCase.Func})))
	out.WriteString(":\n")
	if testCase.Func != nil {
		out.WriteString("\t")
		out.WriteString(testCase.Func.Name)
		out.WriteString("()\n")
	}
	out.WriteString("\treturn 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String()
}

func executeSelectedTests(inputFile string, result *semantic.Result, filter string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stdout io.Writer, stderr io.Writer) int {
	source, err := readSourceWithIncludes(inputFile, map[string]bool{})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	testCases := selectTestCases(result, filter)
	if len(testCases) == 0 {
		fmt.Fprintf(stdout, "[ NO TESTS ] no @test functions matched filter %q\n", strings.TrimSpace(filter))
		return 1
	}

	clangPath, err := exec.LookPath("clang")
	if err != nil {
		fmt.Fprintf(stderr, "error: clang is required to execute tests: %s\n", err)
		return 1
	}

	passed := 0
	skipped := 0
	failed := 0
	for _, testCase := range testCases {
		if testCase.Func == nil {
			continue
		}
		if testCase.skipped() {
			skipped++
			fmt.Fprintln(stdout, formatTestLine("SKIPPED", testCase.Func.Name, fmt.Sprintf(" (%s)", testCase.SkipReason)))
			continue
		}

		fmt.Fprintln(stdout, formatTestLine("RUN", testCase.Func.Name, ""))
		runnerSource := buildIsolatedTestRunnerSource(source, testCase)
		exePath, cleanup, err := compileTestRunnerExecutable(clangPath, runnerSource, optLevel, packedProfile, stderr)
		if err != nil {
			cleanup()
			return 1
		}

		var testStdout bytes.Buffer
		var testStderr bytes.Buffer
		runCmd := exec.Command(exePath)
		runCmd.Stdout = &testStdout
		runCmd.Stderr = &testStderr
		runErr := runCmd.Run()
		cleanup()

		if runErr == nil {
			passed++
			fmt.Fprintln(stdout, formatTestLine("OK", testCase.Func.Name, ""))
			continue
		}

		failed++
		status, detail := classifyTestExecutionError(runErr)
		fmt.Fprintln(stdout, formatTestLine(status, testCase.Func.Name, detail))
		writeCapturedTestOutput(stdout, testCase.Func.Name, testStdout.String(), testStderr.String())
	}
	fmt.Fprintf(stdout, "[ SUMMARY  ] %d test(s) selected; passed=%d skipped=%d failed=%d\n", len(testCases), passed, skipped, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

func runnableTests(cases []selectedTestCase) []*semantic.AnnotatedFunc {
	runnable := make([]*semantic.AnnotatedFunc, 0, len(cases))
	for _, testCase := range cases {
		if testCase.Func == nil || testCase.skipped() {
			continue
		}
		runnable = append(runnable, testCase.Func)
	}
	return runnable
}

func countSkippedTests(cases []selectedTestCase) int {
	count := 0
	for _, testCase := range cases {
		if testCase.skipped() {
			count++
		}
	}
	return count
}

func formatTestLine(status string, name string, detail string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	width := "%-8s"
	if status == "OK" {
		width = "%8s"
	}
	return fmt.Sprintf("[ "+width+" ] %s%s", status, name, detail)
}

func compileTestRunnerExecutable(clangPath string, runnerSource string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "llcontext-test-run-*")
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return "", func() {}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	runnerPath := filepath.Join(tempDir, "generated_runner.llcontext")
	_, runnerResult, ok := analyzeProgram(runnerPath, []byte(runnerSource), stderr)
	if !ok {
		return "", cleanup, fmt.Errorf("failed to analyze generated test runner")
	}

	objectPath := filepath.Join(tempDir, "generated_runner.o")
	if err := backend.WriteLLVMObjectFileWithOptAndPackedLoweringProfile(runnerResult, objectPath, optLevel, packedProfile); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return "", cleanup, err
	}

	exePath := filepath.Join(tempDir, "generated_runner")
	linkArgs := []string{objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		shimPath := filepath.Join(tempDir, "test_runner_runtime_shims.c")
		if err := os.WriteFile(shimPath, []byte(testRunnerRuntimeShimSource()), 0o644); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return "", cleanup, err
		}
		linkArgs = append([]string{"-Wl,-undefined,dynamic_lookup", shimPath}, linkArgs...)
	}
	linkCmd := exec.Command(clangPath, linkArgs...)
	linkCmd.Stdout = stderr
	linkCmd.Stderr = stderr
	if err := linkCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "error: failed to link generated test runner: %s\n", err)
		return "", cleanup, err
	}

	return exePath, cleanup, nil
}

func classifyTestExecutionError(err error) (string, string) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "FAILED", fmt.Sprintf(" (%s)", err)
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			detail := fmt.Sprintf(" (signal %s)", status.Signal())
			switch status.Signal() {
			case syscall.SIGABRT, syscall.SIGTRAP, syscall.SIGILL, syscall.SIGSEGV, syscall.SIGBUS:
				return "PANIC", detail
			default:
				return "FAILED", detail
			}
		}
		if status.Exited() && status.ExitStatus() != 0 {
			return "FAILED", fmt.Sprintf(" (exit %d)", status.ExitStatus())
		}
	}
	if code := exitErr.ExitCode(); code != 0 {
		return "FAILED", fmt.Sprintf(" (exit %d)", code)
	}
	return "FAILED", ""
}

func writeCapturedTestOutput(w io.Writer, testName string, stdOutput string, errOutput string) {
	writeCapturedTestStream(w, "STDOUT", testName, stdOutput)
	writeCapturedTestStream(w, "STDERR", testName, errOutput)
}

func writeCapturedTestStream(w io.Writer, label string, testName string, output string) {
	trimmed := strings.TrimRight(output, "\n")
	if trimmed == "" {
		return
	}
	fmt.Fprintln(w, formatTestLine(label, testName, ""))
	for _, line := range strings.Split(trimmed, "\n") {
		fmt.Fprintf(w, "    %s\n", line)
	}
}

func llcontextStringLiteral(value string) string {
	return strconv.Quote(value)
}

func testRunnerRuntimeShimSource() string {
	return `#include <stddef.h>

void *stderr = NULL;

void *llcontext_test_runner_stub_va_copy(void *args) __asm__("va_copy");
void *llcontext_test_runner_stub_va_copy(void *args) {
	return args;
}

void llcontext_test_runner_stub_va_end(void *args) __asm__("va_end");
void llcontext_test_runner_stub_va_end(void *args) {
	(void)args;
}
`
}
