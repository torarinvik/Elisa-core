package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// TestLawIsRuntime confirms `subject is Law` lowers to the call Law(subject) and runs correctly
// end-to-end (docs/85 Stage 1b): a predicate law used in `if … is Law` and as a returned bool
// produces the expected runtime results.
func TestLawIsRuntime(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	if _, err := os.Stat(std); err != nil {
		t.Skipf("std runtime not found: %v", err)
	}
	dir := t.TempDir()
	rel, err := filepath.Rel(dir, std)
	if err != nil {
		t.Fatalf("rel include: %v", err)
	}
	src := "# include " + "\"" + filepath.ToSlash(rel) + "\"" + `

law Positive(self: i64) = self > 0

def classify(n: i64) -> i64:
    ok: bool = n is Positive          # expression-position law application
    if ok:
        return 1
    return 0

def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    a: i64 = classify(7)
    b: i64 = classify(-3)
    c: i64 = classify(0)
    print((a * 100 + b * 10 + c).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    print("\n") can Console.Write
    return 0
`
	fixture := filepath.Join(dir, "law_is.elisa")
	if err := os.WriteFile(fixture, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixture, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, result, ok := analyzeProgram(fixture, expanded, &stderr)
	if !ok {
		t.Fatalf("analyze failed:\n%s", stderr.String())
	}
	exe, cleanup, err := buildNativeExecutable(result, nil, nil, "", backend.OptimizationLevel3, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, stderr.String())
	}
	defer cleanup()
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, string(out))
	}
	// classify(7)=1, classify(-3)=0, classify(0)=0  →  1*100 + 0 + 0 = 100
	if got := strings.TrimSpace(string(out)); got != "100" {
		t.Fatalf("law `is` runtime mismatch: got %q, want \"100\"", got)
	}
}
