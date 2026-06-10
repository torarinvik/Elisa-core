package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// by_par_fold_smoke proves a `by par` fold (lowered to the runtime parallel `reduce` combinator)
// produces a result identical to the sequential fold, for + and * over large inputs.
func TestRunCLIByParFoldSmoke(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "compiler", "by_par_fold_smoke.elisa")

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("by par fold smoke failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] by_par_fold_smoke") {
		t.Fatalf("expected by_par_fold_smoke to pass, got:\n%s", stdout.String())
	}
}

// `by par` is gated to an associative-combine fold (`acc <op> x`, op ∈ {+,*}, darray source, no
// filter/bindings/range) so the parallel reduction's reordering is never silently incorrect.
// Ineligible folds are a clear parser error, not a sequential fallback.
func TestByParFoldRejectsIneligibleFolds(t *testing.T) {
	cases := map[string]string{
		"filter":                  "return (acc + x for x in a if x > 0 with acc: i64 = 0 by par)",
		"non-assoc op":            "return (acc - x for x in a with acc: i64 = 0 by par)",
		"acc not a whole operand": "return (acc*2 + x for x in a with acc: i64 = 0 by par)",
	}
	for name, foldExpr := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := "include \"" + filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa") + "\"\n" +
				"def t(a: darray[i64]&) -> i64:\n\tcan Parallel, Memory.Allocate, Memory.Release, Abort.Panic:\n\t\t" + foldExpr + "\n"
			path := filepath.Join(dir, "ineligible.elisa")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runCLI([]string{"-emit", "semantic", path}, &stdout, &stderr)
			if !strings.Contains(stderr.String(), "`by par` fold") {
				t.Fatalf("expected a `by par` eligibility error for %s, got stderr:\n%s", name, stderr.String())
			}
		})
	}
}
