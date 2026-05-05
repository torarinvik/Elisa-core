package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIListsAnnotatedFunctionsByKind(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated_lists.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@fixture\ndef shared_seed() -> int:\n    return 7\n\n@bench\ndef bench_hot_loop() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated list fixture: %v", err)
	}

	tests := []struct {
		name     string
		args     []string
		contains []string
		omits    []string
	}{
		{
			name:     "tests",
			args:     []string{"-emit", "tests", fixturePath},
			contains: []string{"alpha_case\tfunc() -> void"},
			omits:    []string{"shared_seed", "bench_hot_loop"},
		},
		{
			name:     "benches",
			args:     []string{"-emit", "benches", fixturePath},
			contains: []string{"bench_hot_loop\tfunc() -> void"},
			omits:    []string{"alpha_case", "shared_seed"},
		},
		{
			name:     "fixtures",
			args:     []string{"-emit", "fixtures", fixturePath},
			contains: []string{"shared_seed\tfunc() -> int"},
			omits:    []string{"alpha_case", "bench_hot_loop"},
		},
		{
			name:     "tests filtered",
			args:     []string{"-emit", "tests", "-filter", "alpha", fixturePath},
			contains: []string{"alpha_case\tfunc() -> void"},
			omits:    []string{"bench_hot_loop", "shared_seed"},
		},
		{
			name:     "benches filtered",
			args:     []string{"-emit", "benches", "-filter", "hot", fixturePath},
			contains: []string{"bench_hot_loop\tfunc() -> void"},
			omits:    []string{"alpha_case", "shared_seed"},
		},
		{
			name:     "fixtures filtered",
			args:     []string{"-emit", "fixtures", "-filter", "seed", fixturePath},
			contains: []string{"shared_seed\tfunc() -> int"},
			omits:    []string{"alpha_case", "bench_hot_loop"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(test.args, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			output := stdout.String()
			for _, want := range test.contains {
				if !strings.Contains(output, want) {
					t.Fatalf("expected output to contain %q, got:\n%s", want, output)
				}
			}
			for _, omit := range test.omits {
				if strings.Contains(output, omit) {
					t.Fatalf("expected output not to contain %q, got:\n%s", omit, output)
				}
			}
		})
	}
}
func TestRunCLIRejectsFilterOutsideAnnotationListModes(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "filter_reject.llcontext")
	if err := os.WriteFile(fixturePath, []byte("def sample_case() -> void:\n    pass\n"), 0o644); err != nil {
		t.Fatalf("failed to write filter rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", "-filter", "sample", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-filter is only supported for -emit facts, tests, benches, fixtures, test-runner, or test") {
		t.Fatalf("expected filter-mode diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIFormatsSourceCanonically(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "format_fixture.llcontext")
	src := "@test\ndef sample_case(value: i64) -> i64:\n    values=[1,2,3]\n    if likely value > 0:\n        return (value)\n    return 0\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write format fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatter to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	formatted := stdout.String()
	for _, check := range []string{
		"@test\n",
		"def sample_case(value: i64) -> i64:",
		"values = [1, 2, 3]",
		"if likely (value > 0):",
	} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}

	formattedPath := filepath.Join(fixtureDir, "formatted_fixture.llcontext")
	if err := os.WriteFile(formattedPath, stdout.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write formatted fixture: %v", err)
	}
	var astStdout bytes.Buffer
	var astStderr bytes.Buffer
	exitCode = runCLI([]string{"-emit", "ast", formattedPath}, &astStdout, &astStderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted source to reparse successfully, stderr:\n%s", astStderr.String())
	}
	if astStderr.Len() != 0 {
		t.Fatalf("expected reparsed formatted source to stay warning-free, got:\n%s", astStderr.String())
	}
}
func TestRunCLIEmitsReferenceDocs(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "reference_fixture.llcontext")
	src := "struct Pair:\n    left: i64\n    right: i64\n\n@test\ndef build_pair(value: i64) -> Pair:\n    return Pair(value, value)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write reference fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "doc", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected doc generation to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"# Reference: reference_fixture.llcontext",
		"## Struct `Pair`",
		"- declaration: `struct Pair:`",
		"- fields:",
		"`left: i64`",
		"## Function `build_pair`",
		"- declaration: `def build_pair(value: i64) -> Pair:`",
		"- annotations:",
		"`@test`",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected reference docs to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIGeneratesSkippedTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "skipped_test_runner_fixture.llcontext")
	src := "@skip(todo)\n@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write skipped runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to generate skipped test runner, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "[ SKIPPED  ] alpha_case (todo)") {
		t.Fatalf("expected skipped test runner to mention alpha_case skip, got:\n%s", output)
	}
	if strings.Contains(output, "\talpha_case()\n") {
		t.Fatalf("expected skipped runner not to invoke alpha_case, got:\n%s", output)
	}
	if !strings.Contains(output, "\tbeta_case()\n") {
		t.Fatalf("expected skipped runner to invoke beta_case, got:\n%s", output)
	}
}
func TestRunCLIGeneratesTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "test_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n\ndef helper() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"@test",
		"def ctx_test_main() -> int can[Console.Write]:",
		"alpha_case()",
		"beta_case()",
		"export func main() -> int = ctx_test_main",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected test runner output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "\thelper()\n") {
		t.Fatalf("expected helper function not to be invoked by the generated runner, got:\n%s", output)
	}
}
func TestRunCLIGeneratesFilteredTestRunnerSource(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "filtered_test_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write filtered runner fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", "-filter", "beta", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "beta_case()") {
		t.Fatalf("expected filtered runner to invoke beta_case, got:\n%s", output)
	}
	if strings.Contains(output, "\talpha_case()\n") {
		t.Fatalf("expected filtered runner not to invoke alpha_case, got:\n%s", output)
	}
	if !strings.Contains(output, "[ SUMMARY  ] 1 test(s) selected") {
		t.Fatalf("expected filtered runner summary, got:\n%s", output)
	}
}
func TestRunCLIRunsGeneratedTestRunner(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "generated_runner_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write generated runner fixture: %v", err)
	}

	var runnerStdout bytes.Buffer
	var runnerStderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test-runner", fixturePath}, &runnerStdout, &runnerStderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to generate runner, stderr:\n%s", runnerStderr.String())
	}
	if runnerStderr.Len() != 0 {
		t.Fatalf("expected no stderr while generating runner, got:\n%s", runnerStderr.String())
	}

	runnerPath := filepath.Join(fixtureDir, "generated_runner.llcontext")
	if err := os.WriteFile(runnerPath, runnerStdout.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write generated runner source: %v", err)
	}
	objectPath := filepath.Join(fixtureDir, "generated_runner.o")

	var objectStdout bytes.Buffer
	var objectStderr bytes.Buffer
	exitCode = runCLI([]string{"-emit", "obj", "-o", objectPath, runnerPath}, &objectStdout, &objectStderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to compile generated runner, stderr:\n%s", objectStderr.String())
	}
	if objectStdout.Len() != 0 {
		t.Fatalf("expected no stdout while compiling generated runner, got:\n%s", objectStdout.String())
	}
	if objectStderr.Len() != 0 {
		t.Fatalf("expected no stderr while compiling generated runner, got:\n%s", objectStderr.String())
	}

	exePath := filepath.Join(fixtureDir, "generated_runner")
	compileCmd := exec.Command(clangPath, objectPath, "-o", exePath)
	compileOutput, err := compileCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, string(compileOutput))
	}

	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated test runner failed: %v\n%s", err, string(runOutput))
	}
	output := string(runOutput)
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"[       OK ] alpha_case",
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected generated runner output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIExecutesSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_tests_fixture.llcontext")
	src := "@test\ndef alpha_case() -> void:\n    pass\n\n@test\ndef beta_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to execute tests successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] alpha_case",
		"[       OK ] alpha_case",
		"[ RUN      ] beta_case",
		"[       OK ] beta_case",
		"[ SUMMARY  ] 2 test(s) selected",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected direct test execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIExecutesEffectfulSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_effectful_tests_fixture.llcontext")
	src := "@test\ndef memory_case() -> void:\n    can Memory.Allocate, Abort.Panic:\n        values: i64[4] = zeroed\n        values[0] <- 7\n        if values[0] != 7:\n            panic(\"expected initialized value\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write effectful execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected effectful test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] memory_case",
		"[       OK ] memory_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected effectful test execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIExecutesPoolBackedSelectedTests(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "execute_pool_tests_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

struct WriteJob:
	slot_bits: uintptr
	value: i64

def write_slot(job: WriteJob) -> i64:
	slot: mutable i64& = job.slot_bits.cast[mutable i64&]
	slot[0] <- job.value
	return job.value

@test
def pool_backed_case() -> void:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
		partials: i64[2] = zeroed
		pool workers(2):
			group: mutable TaskGroup = task_group_new()
			first_bits: uintptr = (&partials[0]).cast[i64&].uintptr()
			second_bits: uintptr = (&partials[1]).cast[i64&].uintptr()
			first: Task[i64, Pending] = submit write_slot(WriteJob(first_bits, 1))
			second: Task[i64, Pending] = submit write_slot(WriteJob(second_bits, 2))
			task_group_add((&group).cast[TaskGroup&], move first)
			task_group_add((&group).cast[TaskGroup&], move second)
			wait all group
		assert_eq(partials[0], 1)
		assert_eq(partials[1], 2)
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write pool execute-tests fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected pool-backed test execution to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{
		"[ RUN      ] pool_backed_case",
		"[       OK ] pool_backed_case",
		"[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected pool-backed execution output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIAcceptsBareSViewLocalAnnotationInObjectBuild(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "sview_local_obj_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def local_view(src: u8&) -> i64:
	text: sview = sview(src, 0, 1)
	return text.len
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write sview local object fixture: %v", err)
	}

	objectPath := filepath.Join(t.TempDir(), "sview_local_obj_fixture.o")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected bare sview local annotation object build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("expected object file to be produced: %v", err)
	}
}
func TestRunCLIAcceptsSurfaceRuntimeBackedLocalAnnotationsInObjectBuild(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}

	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "surface_runtime_locals_obj_fixture.llcontext")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "llcontext_std", "contextlang_runtime.llcontext")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

extern make_text() -> cstr
extern make_bytes() -> darray[u8]
extern make_window() -> dview[u8]
extern make_table() -> dict[cstr, i64]

def local_runtime_locals() -> usize:
	text: cstr = make_text()
	bytes: darray[u8] = make_bytes()
	window: dview[u8] = make_window()
	table: dict[cstr, i64] = make_table()
	return text.len + bytes.count + window.len + table.count
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write surface-runtime locals object fixture: %v", err)
	}

	objectPath := filepath.Join(t.TempDir(), "surface_runtime_locals_obj_fixture.o")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "obj", "-O0", "-o", objectPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected surface runtime-backed local annotations object build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(objectPath); err != nil {
		t.Fatalf("expected object file to be produced: %v", err)
	}
}
