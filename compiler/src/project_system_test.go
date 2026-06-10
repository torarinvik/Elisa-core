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
	if !strings.Contains(stdout.String(), "hot_loop\tfunc() -> void") {
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

func TestRunCLIProjectEASMLintReportsProjectAndDependencySources(t *testing.T) {
	projectRoot := writeProjectFixture(t, projectFixtureOptions{})
	if err := os.MkdirAll(filepath.Join(projectRoot, "easm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "lib", "mathcore.elisalib", "easm"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "include-dirs": ["shared"],
  "easm": ["easm/spin.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "run-emit": "interpret"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", manifestFileName), `{
  "provides": "mathcore",
  "entry": "src/mathcore.elisa",
  "interface": "src/mathcore.elisai",
  "include-dirs": ["shared"],
  "easm": ["easm/clock.easm"]
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "spin.easm"), `module spin
target any
export def easm_spin_pause() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "easm", "clock.easm"), `module clock
target any
export def easm_debug_trap() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: noreturn
    requires: debug.trap
    body:
        trap
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "easm-lint", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected easm-lint to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"EASM files (2)", "easm_spin_pause", "easm_debug_trap"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected easm-lint output to contain %q, got:\n%s", check, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "deps", "app", "--project", projectRoot, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deps to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"easm"`) || !strings.Contains(stdout.String(), "spin.easm") || !strings.Contains(stdout.String(), "clock.easm") {
		t.Fatalf("expected deps json to include easm sources, got:\n%s", stdout.String())
	}
}

func TestRunCLIProjectBuildEmitsEASMWrapper(t *testing.T) {
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/identity.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "target-triple": "x86_64-apple-darwin"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern easm_identity(value: i64) -> i64

def main() -> int:
    return easm_identity(7).int()
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "identity.easm"), `module identity
target any
export def easm_identity(value: i64) -> i64 abi c:
    inputs: value = rdi
    outputs: ret = rax
    stack: unchanged
    control: returns
    body:
        movq %rdi, %rax
        ret
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM project build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{"define i64 @easm_identity", "asm sideeffect", "call i64 @easm_identity"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, stdout.String())
		}
	}
	for _, bad := range []string{"~{rax}", "~{rdi}"} {
		if strings.Contains(stdout.String(), bad) {
			t.Fatalf("expected fixed EASM input/output registers not to be repeated as clobbers via %q, got:\n%s", bad, stdout.String())
		}
	}
}

func TestRunCLIProjectBuildEmitsShadPS4StyleEASMWrappers(t *testing.T) {
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/shadps4_low.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern shadps4_stack_pointer() -> uintptr
extern shadps4_fenced_rdtsc() -> u64
extern shadps4_spin_pause() -> void
extern shadps4_debug_trap() -> void

def main() -> int:
    shadps4_spin_pause()
    _ = shadps4_stack_pointer()
    _ = shadps4_fenced_rdtsc()
    return 0
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "shadps4_low.easm"), `module shadps4_low
target x86_64
export def shadps4_stack_pointer() -> uintptr abi c:
    outputs: ret = rax
    clobbers: rax
    stack: unchanged
    control: returns
    body:
        movq %rsp, %rax
        ret

export def shadps4_fenced_rdtsc() -> u64 abi c:
    outputs: ret = rax
    clobbers: rax, rdx, memory
    stack: unchanged
    control: returns
    requires: x86_64.rdtsc, x86_64.sse.lfence
    body:
        lfence
        rdtsc
        lfence
        ret

export def shadps4_spin_pause() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.sse.pause
    body:
        pause
        ret

export def shadps4_debug_trap() -> void abi c:
    clobbers: memory
    stack: unchanged
    control: noreturn
    requires: debug.trap
    body:
        trap
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected shadPS4-style EASM build to succeed, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	for _, check := range []string{
		"define i64 @shadps4_stack_pointer",
		"define i64 @shadps4_fenced_rdtsc",
		"define void @shadps4_spin_pause",
		"define void @shadps4_debug_trap",
		"rdtsc",
		"pause",
		"trap",
	} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIProjectBuildRequiresEASMEffectsOnExternSurface(t *testing.T) {
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/guarded.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
	    }
	  }
	}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "guarded.easm"), `module guarded
target any
export def guarded_asm() -> void abi c can[Unsafe.Assembly]:
    stack: unchanged
    control: returns
    body:
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern guarded_asm() -> void

def main() -> int:
    guarded_asm()
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected EASM effects to require extern permissions, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "matching Elisa extern does not expose can[Unsafe.Assembly]") {
		t.Fatalf("expected missing Unsafe.Assembly diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern guarded_asm() -> void can[Unsafe.Assembly]

def main() -> int:
    can Unsafe.Assembly:
        guarded_asm()
    return 0
`)
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM extern permission bridge to build, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunCLIProjectBuildRequiresEASMSegmentMutationOnExternSurface(t *testing.T) {
	projectRoot := t.TempDir()
	for _, dir := range []string{filepath.Join(projectRoot, "src"), filepath.Join(projectRoot, "easm")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "easm": ["easm/segment.easm"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm"
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "easm", "segment.easm"), `module segment
target x86_64-apple-darwin
export def load_fs(selector: GuestFsSelector) -> void abi c:
    inputs: selector = rdi
    clobbers: memory
    stack: unchanged
    control: returns
    requires: x86_64.segment.fs, x86_64.segment.write, x86_64.segment.persistent
    body:
        movw %di, %fs
        state fs: guest
        ret
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern GuestFsSelectorRole
type GuestFsSelector = id[GuestFsSelectorRole]

extern load_fs(selector: GuestFsSelector) -> void

def main() -> int:
    load_fs(0u32.cast[GuestFsSelector])
    return 0
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected EASM segment mutation to require extern permissions, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "matching Elisa extern does not expose can[Unsafe.SegmentMutation, Segment.Guest]") {
		t.Fatalf("expected missing Unsafe.SegmentMutation diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Segment.Guest") {
		t.Fatalf("expected missing Segment.Guest diagnostic, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), `extern GuestFsSelectorRole
type GuestFsSelector = id[GuestFsSelectorRole]

@segment_transition(guest)
extern load_fs(selector: GuestFsSelector) -> void can[Unsafe.SegmentMutation, Segment.Guest]

def main() -> int:
    can Unsafe.SegmentMutation, Segment.Guest:
        load_fs(0u32.cast[GuestFsSelector])
    return 0
`)
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected EASM segment mutation extern permission to satisfy build, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunCLIProjectRunSupportsDirectLibraryLinkFlags(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	projectRoot := writeNativeLibraryLinkProjectFixture(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"run", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected native library link project run to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during native library link run, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected successful native library link run not to print stdout, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"project", "view", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project view to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Link flags:") || !strings.Contains(stdout.String(), "-lm") {
		t.Fatalf("expected project view to report link flags, got:\n%s", stdout.String())
	}
}
func TestRunCLIProjectRunSupportsLinkNameExternFFI(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	projectRoot := writeNativeLinkNameProjectFixture(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"run", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected link_name ffi project run to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during link_name ffi run, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected successful link_name ffi run not to print stdout, got:\n%s", stdout.String())
	}
}

func TestRunCLIProjectNativeBuildAndRunWithForeignSources(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	projectRoot := writeNativeForeignProjectFixture(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"build", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected native project build to succeed, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected native project build not to print stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during native project build, got:\n%s", stderr.String())
	}
	exePath := filepath.Join(projectRoot, "build", "app_native")
	if _, err := os.Stat(exePath); err != nil {
		t.Fatalf("expected native build output file %s: %v", exePath, err)
	}
	runCmd := exec.Command(exePath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected built native executable to run successfully: %v\n%s", err, string(runOutput))
	}
	if !strings.Contains(string(runOutput), "native hello") {
		t.Fatalf("expected built native executable output to contain native hello, got:\n%s", string(runOutput))
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"run", "app", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected native project run to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during native project run, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "native hello") {
		t.Fatalf("expected native project run output to contain native hello, got:\n%s", stdout.String())
	}
}

func TestRunCLIProjectTestLinksForeignSources(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	projectRoot := writeNativeForeignProjectFixture(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"test", "tests", "--project", projectRoot}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project test with foreign sources to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during foreign-linked project test, got:\n%s", stderr.String())
	}
	for _, check := range []string{"[ RUN      ] foreign_case", "[ SUMMARY  ] 1 test(s) selected; passed=1 skipped=0 failed=0"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected foreign-linked project test output to contain %q, got:\n%s", check, stdout.String())
		}
	}
}

func TestRunCLIProjectTargetCanOptOutOfProjectNativeInputs(t *testing.T) {
	projectRoot := writeNativeForeignProjectFixture(t)
	projectJSON := `{
  "version": "0.1.0",
  "foreign": ["native/runtime.c"],
  "link-flags": ["-lprojectwide"],
  "targets": {
    "isolated": {
      "entry": "test/project_tests.elisa",
      "inherit-project-native": false,
      "foreign": ["native/isolated.c"],
      "link-flags": ["-ltargetlocal"],
      "emit": "llvm"
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "isolated.c"), "int isolated_value(void) { return 7; }\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"project", "deps", "isolated", "--project", projectRoot, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected project deps to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output during project deps, got:\n%s", stderr.String())
	}
	var report projectDependencyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected json deps report: %v\n%s", err, stdout.String())
	}
	foreign := strings.Join(report.Foreign, "\n")
	if strings.Contains(foreign, "runtime.c") {
		t.Fatalf("expected project-wide foreign source to be excluded, got:\n%s", foreign)
	}
	if !strings.Contains(foreign, "isolated.c") {
		t.Fatalf("expected target-local foreign source to remain, got:\n%s", foreign)
	}
	linkFlags := strings.Join(report.LinkFlags, "\n")
	if strings.Contains(linkFlags, "-lprojectwide") {
		t.Fatalf("expected project-wide link flag to be excluded, got:\n%s", linkFlags)
	}
	if !strings.Contains(linkFlags, "-ltargetlocal") {
		t.Fatalf("expected target-local link flag to remain, got:\n%s", linkFlags)
	}
}

func TestRunCLIProjectTargetWarningPolicyPromotesPerfAndConcurrency(t *testing.T) {
	prevSuppress, hadSuppress := os.LookupEnv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	t.Cleanup(func() {
		if hadSuppress {
			_ = os.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", prevSuppress)
		} else {
			_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
		}
	})

	projectRoot := t.TempDir()
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), `{
  "version": "0.1.0",
  "targets": {
    "perf": {
      "entry": "perf.elisa",
      "emit": "llvm",
      "warnings": {
        "perf": true
      }
    },
    "concurrency": {
      "entry": "concurrency.elisa",
      "emit": "llvm",
      "warnings": {
        "concurrency": true
      }
    },
    "strict": {
      "entry": "strict.elisa",
      "emit": "llvm",
      "warnings": {
        "strict": true
      }
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "perf.elisa"), `def fetch_add(slot: i64, value: i64, order: i64) -> i64:
    return slot + value + order

def main() -> i64:
    acc: mutable i64 = 0
    for i in 0..<4:
        acc <- acc + fetch_add(acc, i.i64(), 0)
    return acc
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "concurrency.elisa"), `enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

def load[T](slot: atomic[T]&, order: MemoryOrder) -> T:
    return zeroed

def main(slot: atomic[i64]&) -> i64:
    return load(slot, MemoryOrder.SeqCst)
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "strict.elisa"), `def read_at(xs: darray[u8], i: int) -> u8:
    if i < xs.count:
        return xs[i]
    return 0
`)

	for _, tc := range []struct {
		target string
		check  string
	}{
		{target: "perf", check: "`fetch_add` performs an atomic read-modify-write/compare-exchange on every iteration"},
		{target: "concurrency", check: "strict concurrency error: `load` is legacy raw atomic surface"},
		{target: "strict", check: "unchecked index requires"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runCLI([]string{"build", tc.target, "--project", projectRoot}, &stdout, &stderr)
		if exitCode == 0 {
			t.Fatalf("expected project target %s warning policy to fail build", tc.target)
		}
		if !strings.Contains(stderr.String(), tc.check) {
			t.Fatalf("expected project target %s stderr to contain %q, got:\n%s", tc.target, tc.check, stderr.String())
		}
	}
}

type projectFixtureOptions struct {
	targetHook string
}

func writeProjectFixture(t *testing.T, options projectFixtureOptions) string {
	t.Helper()
	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "src"),
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "shared"),
		filepath.Join(projectRoot, "test"),
		filepath.Join(projectRoot, "lib", "mathcore.elisalib", "src"),
		filepath.Join(projectRoot, "lib", "mathcore.elisalib", "native"),
		filepath.Join(projectRoot, "lib", "mathcore.elisalib", "shared"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectJSON := `{
  "version": "0.1.0",
  "dependency-search-paths": ["lib"],
  "dependencies": ["mathcore"],
  "include-dirs": ["shared"],
		"foreign": ["native/app_runtime.c"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "llvm",
      "run-emit": "interpret",
      "output": "build/app.ll",
      "opt": "O0",
      "exec": [` + hookJSON(options.targetHook) + `]
    },
    "tests": {
      "entry": "test/project_tests.elisa",
      "emit": "llvm",
      "output": "build/tests.ll",
      "opt": "O0"
    },
    "benches": {
      "entry": "test/project_benches.elisa",
      "emit": "llvm",
      "output": "build/benches.ll",
      "opt": "O0"
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "include \"project_extra.elisa\"\n\ndef main() -> int:\n    return core_seed() + project_extra()\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "app_runtime.c"), "/* app foreign stub */\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "shared", "project_extra.elisa"), "def project_extra() -> int:\n    return 1\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_tests.elisa"), "@test\ndef alpha_case() -> void:\n    pass\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_benches.elisa"), "@bench\ndef hot_loop() -> void:\n    pass\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", manifestFileName), `{
  "provides": "mathcore",
  "entry": "src/mathcore.elisa",
		"interface": "src/mathcore.elisai",
		"include-dirs": ["shared"],
		"foreign": ["native/mathcore_runtime.c"]
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "src", "mathcore.elisa"), "include \"math_helper.elisa\"\n\ndef core_seed() -> int:\n    return math_helper()\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "src", "mathcore.elisai"), "extern core_seed() -> int\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "native", "mathcore_runtime.c"), "/* mathcore foreign stub */\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.elisalib", "shared", "math_helper.elisa"), "def math_helper() -> int:\n    return 41\n")
	return projectRoot
}

func writeNativeForeignProjectFixture(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "src"),
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "test"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectJSON := `{
  "version": "0.1.0",
  "foreign": ["native/runtime.c"],
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "obj",
      "run-emit": "obj",
      "output": "build/app_native",
      "opt": "O0"
    },
    "tests": {
      "entry": "test/project_tests.elisa",
      "emit": "llvm",
      "output": "build/tests.ll",
      "opt": "O0"
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "extern foreign_message() -> u8&\nextern puts(text: u8&) -> int can[Console.Write]\n\ndef main() -> int can[Console.Write]:\n    can Console.Write:\n        puts(foreign_message())\n        return 0\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_tests.elisa"), "extern foreign_value() -> int\n\n@test\ndef foreign_case() -> void can[Abort.Panic]:\n    can Abort.Panic:\n        if foreign_value() != 42:\n            panic(\"expected foreign value\")\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "runtime.c"), "#include <stdint.h>\n\nint64_t foreign_value(void) { return 42; }\nchar *foreign_message(void) { return \"native hello\"; }\n")
	return projectRoot
}

func writeNativeLibraryLinkProjectFixture(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "src"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectJSON := `{
  "version": "0.1.0",
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "obj",
      "run-emit": "obj",
      "output": "build/app_native",
      "opt": "O0",
      "link-flags": ["-lm"]
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "extern cos(x: f64) -> f64\n\ndef main() -> int:\n    value: f64 = cos(0.0)\n    return 0 if value == 1.0 else 1\n")
	return projectRoot
}
func writeNativeLinkNameProjectFixture(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	for _, dir := range []string{
		filepath.Join(projectRoot, "src"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectJSON := `{
  "version": "0.1.0",
  "targets": {
    "app": {
      "entry": "src/main.elisa",
      "emit": "obj",
      "run-emit": "obj",
      "output": "build/app_native",
      "opt": "O0",
      "link-flags": ["-lm"]
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.elisa"), "@link_name(cos)\nextern c_cos(x: f64) -> f64\n\ndef main() -> int:\n    value: f64 = c_cos(0.0)\n    return 0 if value == 1.0 else 1\n")
	return projectRoot
}

func writeFixtureFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hookJSON(hook string) string {
	if strings.TrimSpace(hook) == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(hook, `"`, `\"`) + `"`
}
