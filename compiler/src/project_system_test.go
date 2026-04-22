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
		filepath.Join(projectRoot, "src", "main.llcontext"),
		filepath.Join(projectRoot, "build"),
		filepath.Join(projectRoot, "lib"),
		filepath.Join(projectRoot, "native"),
		filepath.Join(projectRoot, "test"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffolded path %s: %v", path, err)
		}
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
	libRoot := filepath.Join(projectRoot, "lib", "mathcore.llctxlib")
	for _, path := range []string{
		filepath.Join(libRoot, manifestFileName),
		filepath.Join(libRoot, "src", "mathcore.llcontext"),
		filepath.Join(libRoot, "src", "mathcore.llcontexti"),
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
	if !strings.Contains(string(manifestBytes), `"interface": "src/mathcore.llcontexti"`) {
		t.Fatalf("expected scaffolded manifest to record interface path, got:\n%s", string(manifestBytes))
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
	for _, check := range []string{"Sources:", "mathcore.llcontexti", "mathcore_runtime.c", "app_runtime.c"} {
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
		filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "src"),
		filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "native"),
		filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "shared"),
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
      "entry": "src/main.llcontext",
      "emit": "llvm",
      "run-emit": "interpret",
      "output": "build/app.ll",
      "opt": "O0",
      "exec": [` + hookJSON(options.targetHook) + `]
    },
    "tests": {
      "entry": "test/project_tests.llcontext",
      "emit": "llvm",
      "output": "build/tests.ll",
      "opt": "O0"
    },
    "benches": {
      "entry": "test/project_benches.llcontext",
      "emit": "llvm",
      "output": "build/benches.ll",
      "opt": "O0"
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.llcontext"), "# include \"project_extra.llcontext\"\n\ndef main() -> int:\n    return core_seed() + project_extra()\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "app_runtime.c"), "/* app foreign stub */\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "shared", "project_extra.llcontext"), "def project_extra() -> int:\n    return 1\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_tests.llcontext"), "@test\ndef alpha_case() -> void:\n    pass\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_benches.llcontext"), "@bench\ndef hot_loop() -> void:\n    pass\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.llctxlib", manifestFileName), `{
  "provides": "mathcore",
  "entry": "src/mathcore.llcontext",
		"interface": "src/mathcore.llcontexti",
		"include-dirs": ["shared"],
		"foreign": ["native/mathcore_runtime.c"]
}
`)
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "src", "mathcore.llcontext"), "# include \"math_helper.llcontext\"\n\ndef core_seed() -> int:\n    return math_helper()\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "src", "mathcore.llcontexti"), "extern core_seed() -> int\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "native", "mathcore_runtime.c"), "/* mathcore foreign stub */\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "lib", "mathcore.llctxlib", "shared", "math_helper.llcontext"), "def math_helper() -> int:\n    return 41\n")
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
      "entry": "src/main.llcontext",
      "emit": "obj",
      "run-emit": "obj",
      "output": "build/app_native",
      "opt": "O0"
    },
    "tests": {
      "entry": "test/project_tests.llcontext",
      "emit": "llvm",
      "output": "build/tests.ll",
      "opt": "O0"
    }
  }
}
`
	writeFixtureFile(t, filepath.Join(projectRoot, projectFileName), projectJSON)
	writeFixtureFile(t, filepath.Join(projectRoot, "src", "main.llcontext"), "extern foreign_message() -> u8&\nextern puts(text: u8&) -> int can[Console.Write]\n\ndef main() -> int can[Console.Write]:\n    can Console.Write:\n        puts(foreign_message())\n        return 0\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "test", "project_tests.llcontext"), "extern foreign_value() -> int\n\n@test\ndef foreign_case() -> void can[Abort.Panic]:\n    can Abort.Panic:\n        if foreign_value() != 42:\n            panic(\"expected foreign value\")\n")
	writeFixtureFile(t, filepath.Join(projectRoot, "native", "runtime.c"), "#include <stdint.h>\n\nint64_t foreign_value(void) { return 42; }\nchar *foreign_message(void) { return \"native hello\"; }\n")
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
