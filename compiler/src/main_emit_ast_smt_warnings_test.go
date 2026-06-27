package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The SMT discharge tier is on by default, so the inspection/default `-emit ast` warning pass must
// report refinement provability the SAME way a real build does. A nonlinear refinement the build proves
// (`a*b is Bounded[4,100]` for a,b in [2,10]) must NOT surface a spurious "could not be proven
// statically" warning here. (Regression: previously this path ran analysis without SMT, so it warned.)
func TestEmitASTUsesSMTForRefinementWarnings(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT-backed inspection warning test skipped")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "provable.elisa")
	src := `law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "ast", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected success, stderr:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "could not be proven statically") {
		t.Fatalf("the SMT tier proves `a*b is Bounded[4,100]`, so `-emit ast` must not warn; stderr:\n%s", stderr.String())
	}
}

// The counterpart: a genuinely FALSE refinement still warns under `-emit ast` (the SMT tier declines,
// it is not silently dropped). So the fix made the warning ACCURATE, not absent.
func TestEmitASTStillWarnsOnFalseRefinement(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT-backed inspection warning test skipped")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "false.elisa")
	src := `law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul(a: Small, b: Small) -> i64 is Bounded[4, 50]:
    return a * b
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "ast", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected success (warning, not error), stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not be proven statically") {
		t.Fatalf("`a*b` can reach 100 > 50, so `-emit ast` must still warn; stderr:\n%s", stderr.String())
	}
}
