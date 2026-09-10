package main

import (
	"os"
	"path/filepath"
	"testing"
)

// appendProfilerFallbackSource keeps hand-written native test links on the same
// ABI contract as the compiler's native runner. Generated Elisa objects may
// call the optional profiler hooks even when a test is not exercising profiling;
// the weak fallback supplies those hooks without enabling collection.
func appendProfilerFallbackSource(tb testing.TB, repoRoot string, args []string) []string {
	tb.Helper()
	path := filepath.Join(repoRoot, "compiler", "runtime", "profile_hooks.c")
	if _, err := os.Stat(path); err != nil {
		tb.Fatalf("failed to locate profiler fallback source %s: %v", path, err)
	}
	return append(args, path)
}
