//go:build cgo && (darwin || linux || freebsd)

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"elisacore/src/smt"
)

func TestBuildObjectCacheSolverReplacementChangesProofAcceptance(t *testing.T) {
	real, err := exec.LookPath(smt.ResolveBinary(""))
	if err != nil {
		t.Skip("runtime solver qualification requires installed solver")
	}
	real, err = filepath.Abs(real)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	solver := filepath.Join(dir, "z3")
	t.Setenv("ELISA_CACHE_SOLVER_REAL", real)
	t.Setenv("ELISA_Z3", solver)
	t.Setenv("ELISAC_BUILD_CACHE_DIR", filepath.Join(dir, "cache"))
	t.Setenv("ELISACORE_BUILD_CACHE", "1")
	// This wrapper changes at one path while source/options/environment stay fixed.
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexec \"$ELISA_CACHE_SOLVER_REAL\" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "proof.elisa")
	text := "law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi\ntype Small = i64 is Bounded[2, 10]\n\ndef mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:\n    return a * b\n"
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	run := func() (int, string) {
		var out, stderr bytes.Buffer
		rc := runCLI([]string{"-emit", "obj", "-o", filepath.Join(dir, "result.o"), source}, &out, &stderr)
		return rc, stderr.String()
	}
	if rc, stderr := run(); rc != 0 {
		t.Fatalf("real solver did not establish fixture: rc=%d %s", rc, stderr)
	}
	if rc, stderr := run(); rc != 0 {
		t.Fatalf("warm proved build failed: rc=%d %s", rc, stderr)
	}
	if err := os.WriteFile(solver, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	rc, diagnostic := run()
	if rc == 0 {
		t.Fatal("replacement solver failure reused cached proof success")
	}
	t.Setenv("ELISACORE_BUILD_CACHE", "0")
	freshRC, freshDiagnostic := run()
	if freshRC != rc || freshDiagnostic != diagnostic {
		t.Fatalf("replacement differs from uncached: retained=%d/%q fresh=%d/%q", rc, diagnostic, freshRC, freshDiagnostic)
	}
}
