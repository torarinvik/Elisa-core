package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The named monoid reducers `sum`/`product` join the query-expression family and fold the
// optionally-filtered numeric elements. This pins their value semantics (unconditional, filtered,
// product, and an f64 reduction) against the equivalent fold.
func TestRunCLINamedReducerSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "named_reducer_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("named reducer smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] named_reducer_smoke") {
		t.Fatalf("expected named_reducer_smoke to pass, got:\n%s", stdout.String())
	}
}

// `min`/`max` have no identity element, so they return T? (null over empty/fully-filtered),
// consumed by matching the optional. Pins min/max/filtered/empty/f64 value semantics.
func TestRunCLINamedReducerMinMaxSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "named_reducer_minmax_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("named reducer min/max smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] named_reducer_minmax_smoke") {
		t.Fatalf("expected named_reducer_minmax_smoke to pass, got:\n%s", stdout.String())
	}
}

// `sum`/`product` require a numeric element; a non-numeric source is a clear error.
func TestNamedReducerRejectsNonNumeric(t *testing.T) {
	dir := t.TempDir()
	src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
		"def t(a: darray[cstr]&) -> cstr:\n\tcan Memory.Allocate, Abort.Panic:\n\t\treturn sum x in a\n"
	path := filepath.Join(dir, "nonnum.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "sum query requires a numeric element") {
		t.Fatalf("expected a numeric-element error, got stderr:\n%s", stderr.String())
	}
}
