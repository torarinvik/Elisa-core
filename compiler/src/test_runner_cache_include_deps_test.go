package main

import (
	"os"
	"path/filepath"
	"testing"

	"elisacore/src/backend"
)

func TestTestRunnerCacheDeclinesForeignCPreprocessorInputs(t *testing.T) {
	dir := t.TempDir()
	nativeDir := filepath.Join(dir, "native")
	includeRoot := filepath.Join(dir, "include")
	if err := os.MkdirAll(nativeDir, 0o755); err != nil {
		t.Fatalf("create native dir: %v", err)
	}
	if err := os.MkdirAll(includeRoot, 0o755); err != nil {
		t.Fatalf("create include dir: %v", err)
	}
	cases := []struct {
		name      string
		filename  string
		source    string
		linkFlags []string
	}{
		{
			name:     "C angle include through include dir",
			filename: "runtime_bridge.c",
			source:   "#include <project_header.h>\nint runtime_bridge(void) { return PROJECT_VALUE; }\n",
			linkFlags: []string{"-I", includeRoot},
		},
		{
			name:     "C++ macro include with conditional",
			filename: "runtime_bridge.cpp",
			source:   "#define HEADER_NAME \"project_header.h\"\n#if USE_PROJECT_HEADER\n#include HEADER_NAME\n#endif\nint runtime_bridge(void) { return 0; }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridgePath := filepath.Join(nativeDir, tc.filename)
			if err := os.WriteFile(bridgePath, []byte(tc.source), 0o644); err != nil {
				t.Fatalf("write foreign bridge: %v", err)
			}
			_, err := testRunnerCacheArtifactFor(
				"runner",
				"shim",
				nil,
				[]string{bridgePath},
				tc.linkFlags,
				backend.OptimizationLevel0,
				backend.DefaultPackedLoweringProfile(),
				"x86_64-apple-darwin",
				false)
			if err == nil {
				t.Fatal("foreign C++ source with preprocessor dependencies was admitted to the test-runner cache")
			}
		})
	}
}
