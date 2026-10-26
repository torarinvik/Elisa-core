package main

import (
	"elisacore/src/backend"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("ELISACORE_TEST_NATIVE_WORKERS") == "1" {
		backend.RunPartitionWorkerIfRequested()
	}
	prev, hadPrev := os.LookupEnv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", "1")
	// Dispatch real CLI children before parent-only isolation. Their fresh
	// process clock and explicit or genuinely unset cache selection are real.
	if os.Getenv("TEST_BUILD_CACHE_MAIN") == "1" {
		os.Exit(m.Run())
	}
	// Parent tests issue many logical runCLI commands inside one Go process.
	// Production never resets its bounded driver's process-start clock. Isolate
	// only the implicit parent cache, avoiding exhausted budgets and user-cache
	// writes; preserve every explicit test selection, including CACHE="".
	// Independent child tests cover actual default cold builds and disk reuse.
	_, explicitCache := os.LookupEnv("ELISACORE_ANALYSIS_CACHE")
	if !explicitCache {
		_ = os.Setenv("ELISACORE_ANALYSIS_CACHE", "0")
	}
	exitCode := m.Run()
	if !explicitCache {
		_ = os.Unsetenv("ELISACORE_ANALYSIS_CACHE")
	}
	if hadPrev {
		_ = os.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", prev)
	} else {
		_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	}
	os.Exit(exitCode)
}
func repoRootFromMainTest(t testing.TB) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}
