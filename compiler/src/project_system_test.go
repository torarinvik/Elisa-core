package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIInitScaffoldsProjectAndLibrary(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"init", "demo", "--path", baseDir}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project init to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during init, got:\n%s", stderr.String())
	}
	projectRoot := filepath.Join(baseDir, "demo")
	for _, path := range []string{
		filepath.Join(projectRoot, projectFileName),
		filepath.Join(projectRoot, "src", "main.elisa"),
		filepath.Join(projectRoot, "build"),
		filepath.Join(projectRoot, "lib"),
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "test"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffolded path %s: %v", path, err)
		}
	}
	var defaultProject projectDefinition
	if err := decodeJSONFile(filepath.Join(projectRoot, projectFileName), &defaultProject); err != nil {
		t.Fatalf("expected scaffolded project json to decode: %v", err)
	}
	if target := defaultProject.Targets["app"]; target.Warnings != nil {
		t.Fatalf("default scaffold should stay low-friction without warning policy, got %#v", target.Warnings)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"init-lib", "mathcore", "--path", filepath.Join(projectRoot, "lib")}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected library init to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during init-lib, got:\n%s", stderr.String())
	}
	libRoot := filepath.Join(projectRoot, "lib", "mathcore.elisalib")
	for _, path := range []string{
		filepath.Join(libRoot, manifestFileName),
		filepath.Join(libRoot, "src", "mathcore.elisa"),
		filepath.Join(libRoot, "src", "mathcore.elisai"),
		filepath.Join(libRoot, "native"),
		filepath.Join(libRoot, "README.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffolded library path %s: %v", path, err)
		}
	}
	manifestBytes, err := os.ReadFile(filepath.Join(libRoot, manifestFileName))
	if err != nil {
		t.Fatalf("expected manifest to be readable: %v", err)
	}
	if !strings.Contains(string(manifestBytes), `"interface": "src/mathcore.elisai"`) {
		t.Fatalf("expected scaffolded manifest to record interface path, got:\n%s", string(manifestBytes))
	}
}

func TestRunCLIInitStrictScaffoldsStrictProjectPolicy(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"init", "strictdemo", "--path", baseDir, "--strict"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected strict project init to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during strict init, got:\n%s", stderr.String())
	}

	projectPath := filepath.Join(baseDir, "strictdemo", projectFileName)
	var project projectDefinition
	if err := decodeJSONFile(projectPath, &project); err != nil {
		t.Fatalf("expected strict scaffolded project json to decode: %v", err)
	}
	target, ok := project.Targets["app"]
	if !ok {
		t.Fatalf("expected strict scaffold to include app target, got %#v", project.Targets)
	}
	if target.Warnings == nil || !target.Warnings.Strict {
		t.Fatalf("expected strict scaffold to enable warnings.strict, got %#v", target.Warnings)
	}
}

func TestRunCLIProjectBuildRunTestBenchAndView(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	projectRoot := writeProjectFixture(t, projectFixtureOptions{})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project build to succeed, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected project build with target output not to print stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during build, got:\n%s", stderr.String())
	}
	buildOutputPath := filepath.Join(projectRoot, "build", "app.ll")
	buildOutput, err := os.ReadFile(buildOutputPath)
	if err != nil {
		t.Fatalf("expected build output file %s: %v", buildOutputPath, err)
	}
	for _, check := range []string{"define i64 @main()", "define i64 @core_seed()"} {
		if !strings.Contains(string(buildOutput), check) {
			t.Fatalf("expected build output to contain %q, got:\n%s", check, string(buildOutput))
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"run", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project run to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during run, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected project run to produce result 42, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"test", "tests", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project test to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during project test, got:\n%s", stderr.String())
	}
	for _, check := range []string{"[ RUN      ] alpha_case", "[ SUMMARY  ] 1 test(s) selected"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected project test output to contain %q, got:\n%s", check, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"bench", "benches", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project bench listing to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during bench listing, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "hot_loop\tfn() -> void") {
		t.Fatalf("expected bench listing to include hot_loop, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "view", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project view to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during project view, got:\n%s", stderr.String())
	}
	for _, check := range []string{"Selected target: app", "Resolved dependencies:", "mathcore"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected project view output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIProjectRejectsExecHookWithoutTrust(t *testing.T) {
	t.Parallel()
	projectRoot := writeProjectFixture(t, projectFixtureOptions{targetHook: "echo blocked-hook"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected project build without trust to fail, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--trust=full") {
		t.Fatalf("expected trust diagnostic, got:\n%s", stderr.String())
	}
}

func TestRunCLIProjectRunsExecHookWithTrustFull(t *testing.T) {
	t.Parallel()
	projectRoot := writeProjectFixture(t, projectFixtureOptions{targetHook: "echo trusted-hook"})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot, "--trust=full"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected trusted project build to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[ hook     ] target app: echo trusted-hook") {
		t.Fatalf("expected hook trace on stderr, got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "trusted-hook") {
		t.Fatalf("expected hook output on stderr, got:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "build", "app.ll")); err != nil {
		t.Fatalf("expected trusted build output file: %v", err)
	}
}

func TestRunCLIProjectDepsReportsInterfacesAndForeignSources(t *testing.T) {
	t.Parallel()
	projectRoot := writeProjectFixture(t, projectFixtureOptions{})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "deps", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project deps to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during project deps, got:\n%s", stderr.String())
	}
	for _, check := range []string{"Sources:", "mathcore.elisai", "mathcore_runtime.c", "app_runtime.c"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected project deps output to contain %q, got:\n%s", check, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "deps", "app", "--project", projectRoot, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project deps --json to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during project deps --json, got:\n%s", stderr.String())
	}
	var report projectDependencyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected project deps json to decode: %v\n%s", err, stdout.String())
	}
	if report.Target != "app" {
		t.Fatalf("expected target app, got %q", report.Target)
	}
	if len(report.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency report, got %d (%+v)", len(report.Dependencies), report.Dependencies)
	}
	if report.Dependencies[0].Interface == "" {
		t.Fatal("expected dependency report to include interface path")
	}
	if len(report.Foreign) == 0 {
		t.Fatal("expected project dependency report to include foreign sources")
	}
	if report.Emit == "" || report.RunEmit == "" {
		t.Fatalf("expected project dependency report to include emit metadata, got %+v", report)
	}
}

func TestRunCLIProjectABILintFlagsGuestEntryAsmHazards(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "native")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "foreign": ["native/guest_entry.c"],
      "target-triple": "x86_64-apple-darwin"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "def main() -> int:\n    return 0\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "guest_entry.c"), `void ElisaGuestExec_RunMainEntry(void* params, void* exit_func) {
    __asm__ volatile(
        "andq $-16, %%rsp\n"
        "subq $8, %%rsp\n"
        "pushq 8(%1)\n"
        "pushq 0(%1)\n"
        "movq %1, %%rdi\n"
        "movq %2, %%rsi\n"
        "call *%0\n"
        :
        : "r"(params), "r"(params), "r"(exit_func)
        : "rax", "rsi", "rdi");
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "abi-lint", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected abi-lint to fail for guest-entry call trampoline, stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during abi-lint, got:\n%s", stderr.String())
	}
	for _, check := range []string{"guest-entry-call-mangles-stack", "inline-asm-positional-abi-operands", "Target triple: x86_64-apple-darwin"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected abi-lint output to contain %q, got:\n%s", check, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "abi-lint", "app", "--project", projectRoot, "--json"}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected json abi-lint to fail for guest-entry call trampoline")
	}
	var report nativeABILintReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected abi-lint json to decode: %v\n%s", err, stdout.String())
	}
	if report.TargetTriple != "x86_64-apple-darwin" || len(report.Issues) == 0 {
		t.Fatalf("expected abi-lint json metadata and issues, got %+v", report)
	}
	if len(report.Scanned) == 0 || !strings.Contains(strings.Join(report.Scanned, "\n"), "guest_entry.c") {
		t.Fatalf("expected abi-lint json to list scanned native files, got %+v", report.Scanned)
	}
}

func TestRunCLIProjectABILintScansQuotedNativeIncludes(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "native")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "foreign": ["native/bridge.cpp"],
      "target-triple": "x86_64-apple-darwin"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "def main() -> int:\n    return 0\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "bridge.cpp"), "extern \"C\" {\n#include \"runtime.c\"\n}\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "runtime.c"), `void ElisaGuestExec_RunMainEntry(void* params, void* exit_func) {
    __asm__ volatile(
        "andq $-16, %%rsp\n"
        "subq $8, %%rsp\n"
        "pushq 8(%1)\n"
        "pushq 0(%1)\n"
        "movq %1, %%rdi\n"
        "movq %2, %%rsi\n"
        "call *%0\n"
        :
        : "r"(params), "r"(params), "r"(exit_func)
        : "rax", "rsi", "rdi");
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "abi-lint", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected abi-lint to fail for included native runtime, stdout:\n%s", stdout.String())
	}
	for _, check := range []string{"bridge.cpp", "runtime.c", "guest-entry-call-mangles-stack"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected abi-lint output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIProjectABILintStrictContractsRequireGuestEntryIntent(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "native")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "foreign": ["native/guest_exec_runtime.c"],
      "target-triple": "x86_64-apple-darwin"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "def main() -> int:\n    return 0\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "guest_exec_runtime.c"), "void helper(void) {}\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "abi-lint", "app", "--project", projectRoot, "--strict-contracts"}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected strict abi-lint to require guest_entry contract, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "missing-guest-entry-abi-contract") {
		t.Fatalf("expected missing contract diagnostic, got:\n%s", stdout.String())
	}

	writeFixtureFile(t, filepath.Join(projectRoot, "native", "guest_exec_runtime.c"), "/* ELISA_ABI_CONTRACT guest_entry x86_64 ps4_process_entry noreturn */\nvoid helper(void) {}\n")
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "abi-lint", "app", "--project", projectRoot, "--strict-contracts"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected strict abi-lint to accept declared guest_entry contract, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "guest_entry x86_64 ps4_process_entry noreturn") {
		t.Fatalf("expected contract to be reported, got:\n%s", stdout.String())
	}
}
