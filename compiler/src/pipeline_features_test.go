package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsFrontendIRAndLoadsLLVMFromBundle(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "sample.elisa")
	bundlePath := filepath.Join(fixtureDir, "sample.elisair")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_sample.elisa")
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

func TestRunCLIInterpretsTypedZeroedValues(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_zeroed.elisa")
	src := `type VAddr = uintptr
const SLOT_COUNT: usize = 4

enum Mode:
	Off
	On

struct Payload:
	addr: VAddr
	mode: Mode
	items: darray[i64]
	slots: array[i64, SLOT_COUNT]

def make_payload(payload: Payload) -> Payload:
	return payload

def main() -> i64:
	payload: Payload = make_payload(zeroed)
	if payload.addr != 0.uintptr():
		return 1
	if payload.slots[3] != 0:
		return 4
	return 42
`
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write typed zeroed interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected typed zeroed interpreter output to report result 42, got:\n%s", stdout.String())
	}
}

func TestRunCLIInterpretsModuleScopedConstantsAndGlobals(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_module_consts.elisa")
	src := `module PlayGoHle:
	type OrbisPlayGoInstallSpeed = i32
	const ORBIS_PLAYGO_INSTALL_SPEED_TRICKLE: OrbisPlayGoInstallSpeed = 1
	const ORBIS_PLAYGO_INSTALL_SPEED_FULL: OrbisPlayGoInstallSpeed = 2
	global mutable install_speed: OrbisPlayGoInstallSpeed = ORBIS_PLAYGO_INSTALL_SPEED_TRICKLE

	def write_speed(outSpeed: mutable OrbisPlayGoInstallSpeed&) -> void:
		outSpeed <- install_speed

	def reset_to_trickle() -> void:
		install_speed <- ORBIS_PLAYGO_INSTALL_SPEED_TRICKLE

	def set_full() -> void:
		install_speed <- PlayGoHle::ORBIS_PLAYGO_INSTALL_SPEED_FULL

	def current() -> OrbisPlayGoInstallSpeed:
		return install_speed

def main() -> i64:
	if PlayGoHle::current() != PlayGoHle::ORBIS_PLAYGO_INSTALL_SPEED_TRICKLE:
		return 1
	PlayGoHle::set_full()
	if PlayGoHle::current() != PlayGoHle::ORBIS_PLAYGO_INSTALL_SPEED_FULL:
		return 2
	PlayGoHle::reset_to_trickle()
	if PlayGoHle::current() != 1:
		return 3
	value: mutable PlayGoHle::OrbisPlayGoInstallSpeed = 0
	PlayGoHle::write_speed(&value)
	if value != PlayGoHle::ORBIS_PLAYGO_INSTALL_SPEED_TRICKLE:
		return 4
	return 42
`
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write module constant interpreter fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interpreter to succeed, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 42") {
		t.Fatalf("expected module constant interpreter output to report result 42, got:\n%s", stdout.String())
	}
}

func TestRunCLIActivatesLoweredGrammarProductionsForInterpretAndIR(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "grammar_active.elisa")
	bundlePath := filepath.Join(fixtureDir, "grammar_active.elisair")
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

func TestRunCLIEmittedLoweredGrammarSourceIsStandalone(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "grammar_standalone.elisa")
	loweredPath := filepath.Join(fixtureDir, "grammar_standalone.lowered.elisa")
	src := "grammar Demo:\n    produce() -> i64:\n        return 9\n\ndef main() -> i64:\n    return produce()\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write grammar standalone fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "lowered", "-o", loweredPath, sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected lowered emit to succeed, stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "interpret", loweredPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected standalone lowered source to interpret, stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 9") {
		t.Fatalf("expected standalone lowered source to report 9, got:\n%s", stdout.String())
	}
}

func TestRunCLIAcceptsParenthesizedContextualTernaryDArrayLiteral(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "contextual_ternary_darray.elisa")
	src := "def main() -> i64:\n    region scratch(4096)\n    in scratch:\n        xs: darray[i64] = ([1] if true else [])\n        out: i64 = xs.count.i64()\n        destroy scratch\n        return out\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write contextual ternary fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ir", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected contextual ternary darray fixture to compile, stderr:\n%s", stderr.String())
	}
}

func TestRunCLICompilesRegionOwnedStructFixture(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(repoRootFromMainTest(t), "Code", "test_programs", "region_owned_structs.elisa")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-owned struct fixture to compile, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i64 @main()", "%Expr", "%Box"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected llvm output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIPrintsRegionOwnedStructSyntaxInAST(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "region_owned_struct_ast.elisa")
	src := "struct Expr[@owner]:\n    next: Expr&? @owner\n\nstruct Explicit[@arena]:\n    next: Explicit&? @arena\n\nstruct Rows[@owner] layout(soa):\n    value: i64\n"
	if err := os.WriteFile(sourcePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write region-owned AST fixture: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", sourcePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected region-owned AST fixture to print, stderr:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"struct Expr[@owner]", "struct Explicit[@arena]", "layout(soa) struct Rows[@owner]"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, " in owner") {
		t.Fatalf("expected AST output to avoid the removed `in owner` region sugar, got:\n%s", output)
	}
}

func TestRunCLIActivatesExplicitGrammarReturnExpressions(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "grammar_return.elisa")
	bundlePath := filepath.Join(fixtureDir, "grammar_return.elisair")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_runtime_call.elisa")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_bad_named_runtime_call.elisa")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_local_function_alias.elisa")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_global_function_alias.elisa")
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
	t.Parallel()
	fixtureDir := t.TempDir()
	sourcePath := filepath.Join(fixtureDir, "interpret_named_global_field_function_alias.elisa")
	src := "struct CallbackBox:\n    run: func(int, int) -> int\n\ndef add(x: int, y: int) -> int:\n    return x + y\n\nglobal BOX: CallbackBox = CallbackBox{run: add}\n\ndef main() -> int:\n    return BOX.run(y: 7, x: do:\n        seed = 3\n        seed\n    )\n"
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
	t.Parallel()
	src := "def add_twice(x: i64) -> i64:\n    return x + x\n\ndef main() -> i64:\n    return add_twice(21)\n"
	buildResp, status := executeCompileServerRequest(compileServerRequest{
		Mode:     "ir",
		Filename: "server_sample.elisa",
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

func TestCompileServerRequestSupportsFactTraceV2Filter(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_interface_rules.elisa")
	source, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fact interface fixture: %v", err)
	}

	resp, status := executeCompileServerRequest(compileServerRequest{
		Mode:     "facts",
		Filename: filepath.Base(fixturePath),
		Source:   string(source),
		Filter:   "function=eq:fact_interface_rules,class=eq:interface,format=eq:json",
	})
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("expected compile server facts request to succeed, status=%d resp=%+v", status, resp)
	}
	var report struct {
		Version   string `json:"version"`
		Format    string `json:"format"`
		Functions []struct {
			Name string `json:"name"`
		} `json:"functions"`
	}
	if err := json.Unmarshal([]byte(resp.Output), &report); err != nil {
		t.Fatalf("failed to parse fact trace JSON: %v\n%s", err, resp.Output)
	}
	if report.Version != "fact-trace-v2" || report.Format != "json" || len(report.Functions) != 1 || report.Functions[0].Name != "fact_interface_rules" {
		t.Fatalf("unexpected fact trace JSON response: %#v", report)
	}

	resp, status = executeCompileServerRequest(compileServerRequest{
		Mode:     "facts",
		Filename: filepath.Base(fixturePath),
		Source:   string(source),
		Filter:   "kind=widen",
	})
	if status != http.StatusBadRequest || resp.OK || resp.ErrorCode != "fact_trace_filter" {
		t.Fatalf("expected compile server filter failure to be tagged, status=%d resp=%+v", status, resp)
	}
}
