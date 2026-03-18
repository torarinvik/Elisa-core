package main

import (
	"errors"
	"fmt"
	"io"
	"llcontext/src/backend"
	"llcontext/src/semantic"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func selectAnnotatedFunctions(result *semantic.Result, annotationName string, filter string) []*semantic.AnnotatedFunc {
	if result == nil || annotationName == "" {
		return nil
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	selected := make([]*semantic.AnnotatedFunc, 0)
	for _, fn := range result.AnnotatedFuncs {
		if fn == nil || !hasAnnotation(fn, annotationName) {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(fn.Name), filter) {
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
	tests := selectAnnotatedFunctions(result, "test", filter)

	var out strings.Builder
	out.Write(source)
	if len(source) == 0 || source[len(source)-1] != '\n' {
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	if !strings.Contains(string(source), "extern puts(") {
		out.WriteString("extern puts(text: any u8&) -> int\n\n")
	}
	out.WriteString("def ctx_test_main() -> int:\n")

	if len(tests) == 0 {
		message := llcontextStringLiteral(fmt.Sprintf("[ NO TESTS ] no @test functions matched filter %q", strings.TrimSpace(filter)))
		out.WriteString("\tputs(")
		out.WriteString(message)
		out.WriteString(".cast[any u8&]())\n")
		out.WriteString("\treturn 1\n\n")
		out.WriteString("export func main() -> int = ctx_test_main\n")
		return out.String(), nil
	}

	for _, testFn := range tests {
		runLine := llcontextStringLiteral(fmt.Sprintf("[ RUN      ] %s", testFn.Name))
		okLine := llcontextStringLiteral(fmt.Sprintf("[       OK ] %s", testFn.Name))
		out.WriteString("\tputs(")
		out.WriteString(runLine)
		out.WriteString(".cast[any u8&]())\n")
		out.WriteString("\t")
		out.WriteString(testFn.Name)
		out.WriteString("()\n")
		out.WriteString("\tputs(")
		out.WriteString(okLine)
		out.WriteString(".cast[any u8&]())\n")
	}

	summaryLine := llcontextStringLiteral(fmt.Sprintf("[ SUMMARY  ] %d test(s) selected", len(tests)))
	out.WriteString("\tputs(")
	out.WriteString(summaryLine)
	out.WriteString(".cast[any u8&]())\n")
	out.WriteString("\treturn 0\n\n")
	out.WriteString("export func main() -> int = ctx_test_main\n")
	return out.String(), nil
}

func executeSelectedTests(inputFile string, result *semantic.Result, filter string, optLevel backend.OptimizationLevel, packedABI backend.PackedEnumABI, stdout io.Writer, stderr io.Writer) int {
	runnerSource, err := generateTestRunnerSource(inputFile, result, filter)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	clangPath, err := exec.LookPath("clang")
	if err != nil {
		fmt.Fprintf(stderr, "error: clang is required to execute tests: %s\n", err)
		return 1
	}

	tempDir, err := os.MkdirTemp("", "llcontext-test-run-*")
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	defer os.RemoveAll(tempDir)

	runnerPath := filepath.Join(tempDir, "generated_runner.llcontext")
	_, runnerResult, ok := analyzeProgram(runnerPath, []byte(runnerSource), stderr)
	if !ok {
		return 1
	}

	objectPath := filepath.Join(tempDir, "generated_runner.o")
	if err := backend.WriteLLVMObjectFileWithOptAndPackedABI(runnerResult, objectPath, optLevel, packedABI); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	exePath := filepath.Join(tempDir, "generated_runner")
	linkArgs := []string{objectPath, "-o", exePath}
	if runtime.GOOS == "darwin" {
		shimPath := filepath.Join(tempDir, "test_runner_runtime_shims.c")
		if err := os.WriteFile(shimPath, []byte(testRunnerRuntimeShimSource()), 0o644); err != nil {
			fmt.Fprintf(stderr, "error: %s\n", err)
			return 1
		}
		linkArgs = append([]string{"-Wl,-undefined,dynamic_lookup", shimPath}, linkArgs...)
	}
	linkCmd := exec.Command(clangPath, linkArgs...)
	linkCmd.Stdout = stderr
	linkCmd.Stderr = stderr
	if err := linkCmd.Run(); err != nil {
		fmt.Fprintf(stderr, "error: failed to link generated test runner: %s\n", err)
		return 1
	}

	runCmd := exec.Command(exePath)
	runCmd.Stdout = stdout
	runCmd.Stderr = stderr
	if err := runCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "error: failed to execute generated test runner: %s\n", err)
		return 1
	}

	return 0
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
