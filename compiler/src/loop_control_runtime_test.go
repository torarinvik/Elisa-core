package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIBreakAndContinueRuntime(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "loop_control_fixture.elisa")
	testPath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "test.elisa")
	testInclude, err := filepath.Rel(fixtureDir, testPath)
	if err != nil {
		t.Fatalf("failed to compute test include path: %v", err)
	}
	testInclude = filepath.ToSlash(testInclude)
	src := fmt.Sprintf(`# include %q

def while_break_continue_sum(limit: int) -> int:
	total: mutable int = 0
	i: mutable int = 0
	while i < limit:
		i <- i + 1
		if i == 2:
			continue
		if i == 5:
			break
		total <- total + i
	return total

def for_continue_break_sum() -> int:
	total: mutable int = 0
	for i in 0 ..< 8:
		if i == 1:
			continue
		if i == 4:
			break
		total <- total + i
	return total

@test
def loop_control_runtime_test() -> void:
	can Abort.Panic:
		assert_eq(while_break_continue_sum(10), 8)
		assert_eq(for_continue_break_sum(), 5)
`, testInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write loop control fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected loop control test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] loop_control_runtime_test",
		"[       OK ] loop_control_runtime_test",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected loop control output to contain %q, got:\n%s", check, output)
		}
	}
}
