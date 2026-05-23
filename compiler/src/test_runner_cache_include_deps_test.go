package main

import (
	"os"
	"path/filepath"
	"testing"

	"elisacore/src/backend"
)

func TestTestRunnerCacheArtifactForHashesQuotedForeignIncludes(t *testing.T) {
	dir := t.TempDir()
	includedPath := filepath.Join(dir, "runtime.inc")
	bridgePath := filepath.Join(dir, "runtime_bridge.cpp")

	if err := os.WriteFile(includedPath, []byte("#define ELISA_RUNTIME_VALUE 1\n"), 0o644); err != nil {
		t.Fatalf("write included file: %v", err)
	}
	if err := os.WriteFile(bridgePath, []byte("#include \"runtime.inc\"\nint runtime_bridge() { return ELISA_RUNTIME_VALUE; }\n"), 0o644); err != nil {
		t.Fatalf("write foreign bridge: %v", err)
	}

	artifact1, err := testRunnerCacheArtifactFor(
		"runner",
		"shim",
		[]string{bridgePath},
		nil,
		backend.OptimizationLevel0,
		backend.DefaultPackedLoweringProfile(),
		"x86_64-apple-darwin",
	)
	if err != nil {
		t.Fatalf("first artifact key: %v", err)
	}

	if err := os.WriteFile(includedPath, []byte("#define ELISA_RUNTIME_VALUE 2\n"), 0o644); err != nil {
		t.Fatalf("rewrite included file: %v", err)
	}

	artifact2, err := testRunnerCacheArtifactFor(
		"runner",
		"shim",
		[]string{bridgePath},
		nil,
		backend.OptimizationLevel0,
		backend.DefaultPackedLoweringProfile(),
		"x86_64-apple-darwin",
	)
	if err != nil {
		t.Fatalf("second artifact key: %v", err)
	}

	if artifact1.key == artifact2.key {
		t.Fatalf("expected quoted include change to alter cache key, got %q twice", artifact1.key)
	}
}
