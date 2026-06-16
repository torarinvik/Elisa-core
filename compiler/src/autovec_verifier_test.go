package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The auto-vectorization verifier (docs/79 Part IV) tags each indexed-store comprehension build
// loop with `!llvm.loop` metadata and, after optimization, emits a -Wperf warning for any marked
// loop left un-vectorized. The marker rides in the IR, so it survives the inlining of the
// comprehension's function into its caller (the failure mode a held function reference could not).
func TestAutovecVerifierWarnsOnNonVectorizedComprehension(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)

	emitStderr := func(t *testing.T, fixture string, opt string) string {
		path := filepath.Join(repoRoot, "compiler", fixture)
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", opt, path}, &stdout, &stderr); code != 0 {
			t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
		}
		return stderr.String()
	}

	t.Run("non-vectorizable map warns (even though the function inlines away)", func(t *testing.T) {
		errOut := emitStderr(t, "autovec_warn_probe.elisa", "-O3")
		if !strings.Contains(errOut, "-Wperf") || !strings.Contains(errOut, "did not vectorize") {
			t.Fatalf("expected a -Wperf non-vectorization warning, got stderr:\n%s", errOut)
		}
	})

	t.Run("vectorizable map does not warn", func(t *testing.T) {
		errOut := emitStderr(t, "autovec_clean_probe.elisa", "-O3")
		if strings.Contains(errOut, "-Wperf") {
			t.Fatalf("vectorizable comprehensions must not warn, got stderr:\n%s", errOut)
		}
	})

	t.Run("O0 never warns", func(t *testing.T) {
		errOut := emitStderr(t, "autovec_warn_probe.elisa", "-O0")
		if strings.Contains(errOut, "-Wperf") {
			t.Fatalf("the verifier must be silent at -O0, got stderr:\n%s", errOut)
		}
	})

	// A reassociating range fold (the fallback when the pinned path does not apply) is also marked for
	// the verifier, tagged as a "fold reduction". This one vectorizes, so it must NOT warn — proving
	// the verifier covers folds and that a healthy fold is not a false positive.
	t.Run("range fold is tagged as a fold reduction and does not falsely warn", func(t *testing.T) {
		path := filepath.Join(repoRoot, "compiler", "range_fold_probe.elisa")
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "llvm", "-O3", path}, &stdout, &stderr); code != 0 {
			t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "elisa.autovec.expected") || !strings.Contains(stdout.String(), "fold reduction") {
			t.Fatalf("expected the range fold loop to carry the autovec marker tagged \"fold reduction\":\n%s", stdout.String())
		}
		if strings.Contains(stderr.String(), "-Wperf") {
			t.Fatalf("a fold that vectorizes must not warn, got stderr:\n%s", stderr.String())
		}
	})
}
