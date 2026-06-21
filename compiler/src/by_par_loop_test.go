package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// by_par_loop_smoke proves the `for i in 0 ..< n by par:` data-parallel range loop (lowered to the
// runtime `for_indices_par` combinator over disjoint index bands) produces results bit-identical to
// the equivalent sequential loop, for both a 0-based and a non-zero-based range.
func TestRunCLIByParLoopSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_par_loop_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by par loop smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_par_loop_smoke") {
		t.Fatalf("expected by_par_loop_smoke to pass, got:\n%s", stdout.String())
	}
}

// Sharing a raw darray across a `by par` index loop is a data race (the captured header aliases the
// shared backing buffer), so it must be a hard error -- not a silent sequential fallback. The
// race-safe form addresses the output through a thread-shareable Slice band instead.
func TestByParLoopRejectsSharedDarray(t *testing.T) {
	dir := t.TempDir()
	src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
		"def t() -> void:\n" +
		"\tcan Parallel, Memory.Allocate, Memory.Release, Abort.Panic:\n" +
		"\t\tout: mutable darray[i64] = []\n" +
		"\t\tout.resize(100.usize())\n" +
		"\t\tfor i in 0 ..< 100.usize() by par:\n" +
		"\t\t\tout[i] <- i.i64()\n"
	path := filepath.Join(dir, "byloop.elisa")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected the shared-darray `by par` loop to be rejected, but it compiled cleanly")
	}
	if !strings.Contains(stderr.String(), "by par` loop body shares") {
		t.Fatalf("expected a `by par` data-race rejection, got stderr:\n%s", stderr.String())
	}
}

// `by simd` on a range loop is rejected (recognized loop shapes vectorize by default), and a `by`
// marker that is neither `par` nor `simd` is a clear error.
func TestByParLoopRejectsBadMarkers(t *testing.T) {
	cases := map[string]string{
		"simd":    "for i in 0 ..< 10 by simd:\n\t\tconsume(i)\n",
		"unknown": "for i in 0 ..< 10 by wat:\n\t\tconsume(i)\n",
	}
	for name, loop := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "def consume(i: i64) -> void:\n\tpass\n" +
				"def t() -> void:\n\t" + loop
			path := filepath.Join(dir, "bad.elisa")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
			marker := "by simd"
			if name == "unknown" {
				marker = "expected `par` after `by`"
			}
			if !strings.Contains(stderr.String(), marker) {
				t.Fatalf("expected a `by %s` rejection, got stderr:\n%s", name, stderr.String())
			}
		})
	}
}
