package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// A no-filter list comprehension over a darray source lowers to a presized indexed-store loop
// (instead of per-element push) so the fused build loop auto-vectorizes at -O3. This pins the
// VALUE semantics of that fast path — order, exact contents, head bindings, the f64 (vectorizable)
// case, the filter push-fallback, and an empty source.
func TestRunCLIListComprehensionIndexedStoreRuntimeSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "list_comprehension_indexed_store_smoke.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("list comprehension indexed-store smoke failed with exit code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] list_comprehension_indexed_store_smoke") {
		t.Fatalf("expected list comprehension indexed-store smoke to pass, got stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
