package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A plain `dict[cstr, V] = zeroed` inside a region scope grows with no `can` grant, no error
// union, and no explicit Arena threading — parity with darray push. This compiles + runs the
// fixture natively and confirms put/get_or_insert/reserve work through the synthetic region-
// mutation path (semantic dispatch -> backend arena_dict_*_or_panic, panic-on-OOM).
func TestRunCLIStdDictFrictionlessRuntimeSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std_dict_frictionless_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("std dict frictionless runtime smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] std_dict_frictionless_smoke") {
		t.Fatalf("expected std dict frictionless smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
