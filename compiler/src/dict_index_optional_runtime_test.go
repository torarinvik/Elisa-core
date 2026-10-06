package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// `d[k]` is an optional reference (stage1 parity): a missing key yields absent instead of
// trapping, and `get ... else` / `is` bindings unwrap it in native code.
func TestRunCLIDictIndexOptionalRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_dict_index_optional_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("dict index optional smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_dict_index_optional_smoke") {
		t.Fatalf("expected dict index optional smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
