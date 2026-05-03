package main

import (
	"errors"
	"fmt"
	"io"
	"llcontext/src/backend"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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
func compileTestRunnerExecutable(clangPath string, runnerSource string, foreignFiles []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), nativeBuildTiming, time.Duration, time.Duration, error) {
	return compileTestRunnerExecutableWithShim(clangPath, runnerSource, testRunnerRuntimeShimSource(), foreignFiles, optLevel, packedProfile, stderr)
}
func compileTestRunnerExecutableWithShim(clangPath string, runnerSource string, shimSource string, foreignFiles []string, optLevel backend.OptimizationLevel, packedProfile backend.PackedLoweringProfile, stderr io.Writer) (string, func(), nativeBuildTiming, time.Duration, time.Duration, error) {
	cacheLookupStart := time.Now()
	cacheArtifact := testRunnerCacheArtifact{}
	if testRunnerCacheEnabled() {
		artifact, hit, err := locateCachedTestRunner(runnerSource, shimSource, foreignFiles, optLevel, packedProfile)
		cacheArtifact = artifact
		lookupElapsed := time.Since(cacheLookupStart)
		if err != nil {
			return "", func() {}, nativeBuildTiming{CacheLookup: lookupElapsed}, 0, 0, err
		}
		if hit {
			debugTestRunnerCache(stderr, "hit", artifact)
			return artifact.executable, func() {}, nativeBuildTiming{CacheLookup: lookupElapsed, CacheHit: true}, 0, 0, nil
		}
		debugTestRunnerCache(stderr, "miss", artifact)
	}
	tempDir, err := os.MkdirTemp("", "llcontext-test-run-*")
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return "", func() {}, nativeBuildTiming{CacheLookup: time.Since(cacheLookupStart)}, 0, 0, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	runnerPath := filepath.Join(tempDir, "generated_runner.llcontext")
	analyzeStart := time.Now()
	_, runnerResult, ok := analyzeProgram(runnerPath, []byte(runnerSource), stderr)
	analyzeElapsed := time.Since(analyzeStart)
	if !ok {
		return "", cleanup, nativeBuildTiming{CacheLookup: time.Since(cacheLookupStart)}, analyzeElapsed, 0, fmt.Errorf("failed to analyze generated test runner")
	}

	shimPath := filepath.Join(tempDir, "test_runner_runtime_shims.c")
	shimStart := time.Now()
	if err := os.WriteFile(shimPath, []byte(shimSource), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return "", cleanup, nativeBuildTiming{CacheLookup: time.Since(cacheLookupStart)}, analyzeElapsed, time.Since(shimStart), err
	}
	shimElapsed := time.Since(shimStart)
	linkForeignFiles := append([]string{shimPath}, foreignFiles...)
	exePath, nativeCleanup, timing, err := buildNativeExecutableWithClang(clangPath, runnerResult, linkForeignFiles, filepath.Join(tempDir, "generated_runner"), optLevel, packedProfile, stderr)
	timing.CacheLookup = time.Since(cacheLookupStart)
	if err != nil {
		return "", cleanup, timing, analyzeElapsed, shimElapsed, err
	}
	if testRunnerCacheEnabled() && cacheArtifact.executable != "" {
		publishStart := time.Now()
		if err := publishCachedTestRunner(cacheArtifact, exePath); err == nil {
			timing.CachePublish = time.Since(publishStart)
			debugTestRunnerCache(stderr, "publish", cacheArtifact)
		} else {
			timing.CachePublish = time.Since(publishStart)
			if testRunnerCacheDebugEnabled() && stderr != nil {
				fmt.Fprintf(stderr, "[ cache    ] publish-error key=%s err=%s\n", cacheArtifact.key, err)
			}
		}
	}
	return exePath, func() {
		nativeCleanup()
		cleanup()
	}, timing, analyzeElapsed, shimElapsed, nil
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
