package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Comprehensions vectorize by default (docs/79 Part IV): the per-comprehension `by simd` marker was
// removed. Both a fold and a map that carry it are a clear parser error pointing at the new default,
// not a silently-ignored token.
func TestBySimdMarkerRemoved(t *testing.T) {
	cases := map[string]string{
		"fold": "s: f64 = (acc + x for x in a with acc: f64 = 0.0 by simd)",
		"map":  "out: darray[f64] = [x * 2.0 for x in a by simd]",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
				"def t(a: mutable darray[f64]&) -> void:\n\tcan Memory.Allocate, Abort.Panic:\n\t\t" + line + "\n"
			path := filepath.Join(dir, "bysimd.elisa")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "`by simd` was removed") {
				t.Fatalf("expected a `by simd` removed error for %s, got stderr:\n%s", name, stderr.String())
			}
		})
	}
}

// fold_default_smoke pins that comprehension folds parse across every shape (plain, filtered,
// head-binding, range) and compute correct results with the default tree reduction order.
func TestRunCLIDefaultFoldSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "fold_default_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("default fold smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] fold_default_smoke") {
		t.Fatalf("expected fold_default_smoke to pass, got:\n%s", stdout.String())
	}
}

// map_default_smoke pins that list-map comprehensions compute correct values by default (indexed
// store over a darray source, and a range source).
func TestRunCLIDefaultMapSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "map_default_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("default map smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] map_default_smoke") {
		t.Fatalf("expected map_default_smoke to pass, got:\n%s", stdout.String())
	}
}
