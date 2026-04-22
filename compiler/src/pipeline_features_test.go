package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsFrontendIRAndLoadsLLVMFromBundle(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "sample.llcontext")
	bundlePath := filepath.Join(fixtureDir, "sample.llctxir")
	src := "def helper(x: i64) -> i64:\n    return x + 2\n\ndef main() -> i64:\n    return helper(40)\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write frontend IR fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ir", "-o", bundlePath, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected frontend IR emit to succeed, stderr:\n%s", stderr.String())
	}
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected frontend IR bundle at %s: %v", bundlePath, err)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "llvm", bundlePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected llvm-from-bundle to succeed, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i64 @helper(i64", "define i64 @main()"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llvm output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIInterpretsSimpleProgram(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_sample.llcontext")
	src := "def add_twice(x: i64) -> i64:\n    acc: mutable i64 = x\n    acc += x\n    return acc\n\ndef main() -> i64:\n    seed: i64 = 20\n    value: i64 = seed + 1\n    if value == 21:\n        return add_twice(value)\n    return 0\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected interpreter output to report result 42, got:\n%s", stdout.String())
	}
}

func TestRunCLIActivatesLoweredGrammarProductionsForInterpretAndIR(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "grammar_active.llcontext")
	bundlePath := filepath.Join(fixtureDir, "grammar_active.llctxir")
	src := "grammar Demo:\n    produce() -> i64:\n        pass\n\ndef main() -> i64:\n    return produce()\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write grammar activation fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar-backed interpret to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 0") {
		t.Fatalf("expected grammar-backed interpret to report zeroed grammar return, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "ir", "-o", bundlePath, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar-backed IR emit to succeed, stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "interpret", bundlePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar-backed interpret from bundle to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 0") {
		t.Fatalf("expected grammar-backed bundle interpret to report zeroed grammar return, got:\n%s", stdout.String())
	}
}

func TestRunCLIActivatesExplicitGrammarReturnExpressions(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "grammar_return.llcontext")
	bundlePath := filepath.Join(fixtureDir, "grammar_return.llctxir")
	src := "grammar Demo:\n    produce() -> i64:\n        return 7\n\ndef main() -> i64:\n    return produce()\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write grammar return fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar explicit return interpret to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 7") {
		t.Fatalf("expected grammar explicit return interpret to report 7, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "ir", "-o", bundlePath, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar explicit return IR emit to succeed, stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "interpret", bundlePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected grammar explicit return bundle interpret to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 7") {
		t.Fatalf("expected grammar explicit return bundle interpret to report 7, got:\n%s", stdout.String())
	}
}

func TestRunCLIInterpretsNamedRuntimeFunctionCalls(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_runtime_call.llcontext")
	src := "extern puts(text: u8&) -> int\nextern assert(cond: bool) -> void\n\ndef main() -> int:\n    printed: int = puts(text: do:\n        prefix: static u8& = \"hi\"\n        prefix.cast[u8&]\n    )\n    assert(cond: printed == 2)\n    return printed\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write named-runtime interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter named-runtime fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "hi") {
		t.Fatalf("expected interpreter output to include runtime puts text, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 2") {
		t.Fatalf("expected interpreter output to report result 2, got:\n%s", stdout.String())
	}
}

func TestRunCLIRejectsBadNamedRuntimeFunctionCall(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_bad_named_runtime_call.llcontext")
	src := "extern assert(cond: bool) -> void\n\ndef main() -> int:\n    assert(value: true)\n    return 0\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write bad named-runtime interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected interpreter named-runtime fixture to fail")
	}
	if !strings.Contains(stderr.String(), `unknown argument "value"`) {
		t.Fatalf("expected semantic rejection for unknown named runtime arg, got:\n%s", stderr.String())
	}
}

func TestRunCLIInterpretsNamedLocalFunctionAliasCall(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_local_function_alias.llcontext")
	src := "def add(x: int, y: int) -> int:\n    return x + y\n\ndef main() -> int:\n    f = add\n    return f(y: 7, x: do:\n        seed = 3\n        seed\n    )\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write named local alias interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter local-function-alias fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 10") {
		t.Fatalf("expected interpreter output to report result 10, got:\n%s", stdout.String())
	}
}

func TestRunCLIInterpretsNamedGlobalFunctionAliasCall(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_global_function_alias.llcontext")
	src := "def add(x: int, y: int) -> int:\n    return x + y\n\nglobal runner: func(int, int) -> int = add\n\ndef main() -> int:\n    return runner(y: 7, x: do:\n        seed = 3\n        seed\n    )\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write named global alias interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter global-function-alias fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 10") {
		t.Fatalf("expected interpreter output to report result 10, got:\n%s", stdout.String())
	}
}

func TestRunCLIInterpretsNamedGlobalFieldFunctionAliasCall(t *testing.T) {
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_global_field_function_alias.llcontext")
	src := "struct CallbackBox:\n    run: func(int, int) -> int\n\ndef add(x: int, y: int) -> int:\n    return x + y\n\nglobal BOX: CallbackBox = CallbackBox(add)\n\ndef main() -> int:\n    return BOX.run(y: 7, x: do:\n        seed = 3\n        seed\n    )\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write named global-field alias interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter global-field-alias fixture to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 10") {
		t.Fatalf("expected interpreter output to report result 10, got:\n%s", stdout.String())
	}
}

func TestCompileServerRequestSupportsIRInterpretAndLLVM(t *testing.T) {
	src := "def add_twice(x: i64) -> i64:\n    return x + x\n\ndef main() -> i64:\n    return add_twice(21)\n"
	buildResp, status := executeCompileServerRequest(compileServerRequest{
		Mode:     "ir",
		Filename: "server_sample.llcontext",
		Source:   src,
	})
	if status != http.StatusOK || !buildResp.OK {
		t.Fatalf("expected compile server IR build to succeed, status=%d resp=%+v", status, buildResp)
	}
	if buildResp.IR == "" {
		t.Fatalf("expected compile server IR response to include a bundle payload")
	}

	runResp, status := executeCompileServerRequest(compileServerRequest{
		Mode: "interpret",
		IR:   buildResp.IR,
	})
	if status != http.StatusOK || !runResp.OK {
		t.Fatalf("expected compile server interpret to succeed, status=%d resp=%+v", status, runResp)
	}
	if runResp.Value != "42" {
		t.Fatalf("expected interpret value 42, got %+v", runResp)
	}

	llvmResp, status := executeCompileServerRequest(compileServerRequest{
		Mode: "llvm",
		IR:   buildResp.IR,
	})
	if status != http.StatusOK || !llvmResp.OK {
		t.Fatalf("expected compile server llvm to succeed, status=%d resp=%+v", status, llvmResp)
	}
	if !strings.Contains(llvmResp.Output, "define i64 @main()") {
		t.Fatalf("expected llvm response to contain main definition, got:\n%s", llvmResp.Output)
	}
}
