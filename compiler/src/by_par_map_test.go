package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `by par` on a map is gated to a no-filter map over a plain darray identifier, so the parallel
// decomposition is well-defined. Ineligible maps are a clear error, not a silent sequential fallback.
func TestByParMapRejectsIneligible(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"filter": "out: darray[i64] = [x for x in a if x > 0 by par]",
		"range":  "out: darray[i64] = [i*2 for i in 0..<10 by par]",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
				"def t(a: darray[i64]&) -> usize:\n\tcan Parallel, Memory.Allocate, Memory.Release, Abort.Panic:\n\t\t" + line + "\n\t\treturn out.count\n"
			path := filepath.Join(dir, "bymap.elisa")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "`by par` map") {
				t.Fatalf("expected a clear `by par` map eligibility error for %s, got stderr:\n%s", name, stderr.String())
			}
		})
	}
}

// Generic instances are valid map elements. The old lowering reconstructed a surface type
// declaration from the resolved element type before analyzing the synthesized call; that helper
// intentionally returned nil for GenericInstanceType and rejected this otherwise structural case.
func TestByParMapAcceptsGenericElement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
		"struct Box[T]:\n\tvalue: T\n\n" +
		"def t(a: darray[Box[i64]]&) -> usize:\n\tcan Parallel, Memory.Allocate, Memory.Release, Abort.Panic:\n\t\tout: darray[Box[i64]] = [x for x in a by par]\n\t\treturn out.count\n"
	path := filepath.Join(dir, "bymap_generic.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("generic by-par map should pass semantic analysis (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "`by par` map") {
		t.Fatalf("generic element should use structural par-map inference, got stderr:\n%s", stderr.String())
	}
}

// by_par_map_smoke proves a `by par` map computes the same result as the sequential map across an
// identity-ish, a type-changing, and a struct-element transform.
func TestRunCLIByParMapSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_par_map_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by par map smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_par_map_smoke") {
		t.Fatalf("expected by_par_map_smoke to pass, got:\n%s", stdout.String())
	}
}
