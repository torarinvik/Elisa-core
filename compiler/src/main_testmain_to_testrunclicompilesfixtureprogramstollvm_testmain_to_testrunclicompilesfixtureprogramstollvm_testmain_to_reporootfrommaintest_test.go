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
	exitCode := m.Run()
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
