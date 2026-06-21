package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStdSetRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_set_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std set runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"[       OK ] std_set_literal_and_methods_smoke",
		"[       OK ] std_set_enum_keys_smoke",
		"[       OK ] std_set_hashtable_stress",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected std set smoke output to contain %q, got stdout:\n%s\nstderr:\n%s", want, stdout.String(), stderr.String())
		}
	}
}
