package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The `can Scalar` permission (docs/70): user loops in the clean element-wise indexed-store shape
// are tagged expected-to-vectorize alongside the synthesized comprehension loops. One that stays
// scalar draws a warning naming the `can Scalar` escape hatch — and a hard compile error under
// -Wperf — unless it sits inside a `can Scalar` grant, which silences it entirely.
func TestWperfScalarPermission(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)

	run := func(t *testing.T, fixture string, args ...string) (int, string) {
		t.Helper()
		path := filepath.Join(repoRoot, "compiler", fixture)
		var stdout, stderr bytes.Buffer
		code := runCLI(append(append([]string{"-emit", "llvm"}, args...), path), &stdout, &stderr)
		return code, stderr.String()
	}

	t.Run("scalar user loop warns without -Wperf", func(t *testing.T) {
		code, errOut := run(t, "scalar_loop_warn_probe.elisa", "-O3")
		if code != 0 {
			t.Fatalf("without -Wperf a scalar loop must stay a warning (exit 0), got %d:\n%s", code, errOut)
		}
		if !strings.Contains(errOut, "warning [-Wperf]") || !strings.Contains(errOut, "did not vectorize") {
			t.Fatalf("expected a -Wperf scalar-loop warning, got stderr:\n%s", errOut)
		}
		if !strings.Contains(errOut, "can Scalar") {
			t.Fatalf("the warning must name the `can Scalar` escape hatch, got stderr:\n%s", errOut)
		}
	})

	t.Run("scalar user loop is a hard error under -Wperf", func(t *testing.T) {
		code, errOut := run(t, "scalar_loop_warn_probe.elisa", "-O3", "-Wperf")
		if code == 0 {
			t.Fatalf("-Wperf must reject an unacknowledged scalar loop, but compilation succeeded:\n%s", errOut)
		}
		if !strings.Contains(errOut, "error [-Wperf]") {
			t.Fatalf("expected an error-severity -Wperf diagnostic, got stderr:\n%s", errOut)
		}
	})

	t.Run("can Scalar block silences the loop even under -Wperf", func(t *testing.T) {
		code, errOut := run(t, "scalar_loop_granted_probe.elisa", "-O3", "-Wperf")
		if code != 0 {
			t.Fatalf("a `can Scalar:`-wrapped loop must compile under -Wperf, got %d:\n%s", code, errOut)
		}
		if strings.Contains(errOut, "-Wperf") {
			t.Fatalf("a `can Scalar:`-wrapped loop must not be flagged, got stderr:\n%s", errOut)
		}
	})

	t.Run("vectorizable user loop does not warn (no false positives)", func(t *testing.T) {
		code, errOut := run(t, "scalar_loop_clean_probe.elisa", "-O3", "-Wperf")
		if code != 0 {
			t.Fatalf("a vectorizable user loop must compile under -Wperf, got %d:\n%s", code, errOut)
		}
		if strings.Contains(errOut, "-Wperf") {
			t.Fatalf("a vectorizable user loop must not be flagged, got stderr:\n%s", errOut)
		}
	})

	t.Run("O0 never flags scalar loops", func(t *testing.T) {
		code, errOut := run(t, "scalar_loop_warn_probe.elisa", "-O0", "-Wperf")
		if code != 0 {
			t.Fatalf("no vectorizer runs at -O0, so -Wperf must not reject, got %d:\n%s", code, errOut)
		}
		if strings.Contains(errOut, "-Wperf") {
			t.Fatalf("the verifier must be silent at -O0, got stderr:\n%s", errOut)
		}
	})
}
