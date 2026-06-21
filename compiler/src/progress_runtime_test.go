package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLICompilesProgressBudgetRuntimeHelpersToLLVM(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "progress_budget_llvm.elisa")
	preludePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime_prelude.elisa")
	preludeInclude, err := filepath.Rel(fixtureDir, preludePath)
	if err != nil {
		t.Fatalf("failed to compute prelude include path: %v", err)
	}
	preludeInclude = filepath.ToSlash(preludeInclude)
	src := fmt.Sprintf(`# include %q

def progress_budget_probe() -> i64:
    budget: mutable ProgressBudget = progress_budget_steps(3)
    i: mutable i64 = 0
    while i < 3:
        progress_tick(&budget) can Progress.Tick, Progress.CheckCancel, Abort.Panic
        i <- i + 1
    return budget.remaining_steps
`, preludeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write progress runtime fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected progress runtime LLVM emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"%ProgressBudget = type",
		"define %ProgressBudget @progress_budget_steps(",
		"define void @progress_tick(",
		"define i64 @progress_budget_probe(",
		"progress budget exhausted",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}
