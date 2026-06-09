package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The per-fold `by simd` marker opts one fold's reduction into the reassociated tree form (full
// fast-math FP scoped to that accumulator update), without the program-wide `-ffast-math`. The
// plain fold beside it must stay the bit-exact ordered reduction.
func TestBySimdFoldReassociates(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_simd_fold_probe.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "llvm", "-O3", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("emit llvm failed (%d):\n%s", code, stderr.String())
	}
	out := stdout.String()

	body := func(fn string) string {
		re := regexp.MustCompile(`(?s)define[^\n]*@` + fn + `\(.*?\n\}`)
		m := re.FindString(out)
		if m == "" {
			t.Fatalf("function %s not found in IR:\n%s", fn, out)
		}
		return m
	}

	if strings.Contains(body("probe_plain"), "fadd fast <") {
		t.Fatalf("plain fold must stay the ordered reduction (no reassociated vector fadd):\n%s", body("probe_plain"))
	}
	if !strings.Contains(body("probe_simd"), "fadd fast <") {
		t.Fatalf("`by simd` fold must reassociate into a vector `fadd fast`:\n%s", body("probe_simd"))
	}
}

// by_simd_fold_smoke pins that `by simd` parses across every fold shape and computes correct
// results (the data is integer-valued, so reassociation does not change the sums).
func TestRunCLIBySimdFoldSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_simd_fold_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by simd fold smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_simd_fold_smoke") {
		t.Fatalf("expected by_simd_fold_smoke to pass, got:\n%s", stdout.String())
	}
}
