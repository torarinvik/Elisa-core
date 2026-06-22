package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `ghost`-field-referencing `ensure` clause is proof-only: kept for static discharge but stripped
// from the codegen-visible contract set, so it never lowers to a debug runtime check that reads the
// erased field. Before the strip, `-emit test` failed with "struct ... has no field <ghost>". This
// drives the full backend path to confirm the program now compiles AND runs.
func TestRunCLIGhostEnsureClauseStrippedFromRuntime(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ghost_ensure_strip_fixture.elisa")
	src := `struct MappedWindow:
    base: u64
    size: u64
    ghost model_end: u64
    invariant self.model_end == self.base + self.size

def window_end(self: MappedWindow&) -> u64:
    ensure result == self.model_end
    return self.base + self.size

@test
def ghost_ensure_runs() -> void:
    can Abort.Panic:
        w: MappedWindow = MappedWindow(0x1000, 0x2000)
        if window_end(&w) != 0x3000:
            panic("ghost-modeled end-address wrong")
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected ghost-ensure program to compile+run, exit=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "has no field") {
		t.Fatalf("ghost field leaked into codegen:\n%s", out)
	}
	for _, want := range []string{"[       OK ] ghost_ensure_runs", "passed=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}
