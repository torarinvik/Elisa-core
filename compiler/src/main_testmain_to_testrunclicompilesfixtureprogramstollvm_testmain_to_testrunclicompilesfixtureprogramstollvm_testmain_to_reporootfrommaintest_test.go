package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	prev, hadPrev := os.LookupEnv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Setenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS", "1")
	exitCode := m.Run()
	if hadPrev {
		_ = os.Setenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS", prev)
	} else {
		_ = os.Unsetenv("LLCONTEXT_SUPPRESS_DEPRECATED_WARNINGS")
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
