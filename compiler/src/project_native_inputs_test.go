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

func TestRunCLIProjectRunSupportsDirectLibraryLinkFlags(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		{target: "concurrency", check: "raw concurrency surface removed: `load` is legacy raw atomic surface"},
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
