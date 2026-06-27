package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// by_par_collection_smoke proves the `for x in xs by par:` data-parallel COLLECTION loop (lowered to
// the same `for_indices_par` disjoint-band machinery as the range form, indexing the source through
// a thread-shareable Slice) produces results bit-identical to the equivalent sequential loop, for
// both a darray source and a Slice source.
func TestRunCLIByParCollectionSmoke(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_par_collection_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by par collection smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_par_collection_smoke") {
		t.Fatalf("expected by_par_collection_smoke to pass, got:\n%s", stdout.String())
	}
}

// Sharing a raw darray for OUTPUT across a `by par` collection loop is a data race (the captured
// header aliases the shared backing buffer), so it must be a hard error -- not a silent sequential
// fallback. The read source is fine because the lowering indexes it through a thread-shareable Slice.
func TestByParCollectionRejectsSharedDarray(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rt := filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	src := "include \"" + rt + "\"\n" +
		"def t() -> void:\n" +
		"\tcan Parallel, Memory.Allocate, Memory.Release, Abort.Panic:\n" +
		"\t\txs: mutable darray[i64] = [k for k in 0..<100]\n" +
		"\t\tout: mutable darray[i64] = []\n" +
		"\t\tout.resize(100.usize())\n" +
		"\t\tfor x in xs by par:\n" +
		"\t\t\tout[x.usize()] <- x * x\n"
	path := filepath.Join(dir, "bycoll.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected the shared-darray `by par` collection loop to be rejected, but it compiled cleanly")
	}
	if !strings.Contains(stderr.String(), "by par` loop body shares") {
		t.Fatalf("expected a `by par` data-race rejection, got stderr:\n%s", stderr.String())
	}
}

// `by simd` on a collection loop is rejected, and a `by` marker that is neither `par` nor `simd` is a
// clear error -- the same diagnostics as the range form.
func TestByParCollectionRejectsBadMarkers(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ loop, want string }{
		"simd":    {"for x in xs by simd:\n\t\tconsume(x)\n", "by simd"},
		"unknown": {"for x in xs by wat:\n\t\tconsume(x)\n", "expected `par` after `by`"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "def consume(i: i64) -> void:\n\tpass\n" +
				"def t() -> void:\n\txs: mutable darray[i64] = []\n\t" + tc.loop
			path := filepath.Join(dir, "bad.elisa")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("expected a `by %s` rejection (%q), got stderr:\n%s", name, tc.want, stderr.String())
			}
		})
	}
}
