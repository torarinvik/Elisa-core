package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunCLIExecutesCallAsIndirectCall exercises the typed, Unsafe-gated
// indirect-call primitive `value.call_as[func(...)->T](args)` end to end: a raw
// pointer-like value is invoked through an asserted function signature and
// lowers to a native LLVM indirect call. It is the general replacement for the
// old string-keyed, one-C-shim-per-signature native callback path.
func TestRunCLIExecutesCallAsIndirectCall(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "callas_indirect_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("call_as indirect-call smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] call_as_indirect_smoke") {
		t.Fatalf("expected call_as indirect-call smoke test to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
