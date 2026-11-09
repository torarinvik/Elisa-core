package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIPrintsAnnotatedFunctionsInAST(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated.elisa")
	src := "@test\ndef sample_case() -> void:\n    pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"@test", "def sample_case(0 params) -> void (1 stmts)"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIPrintsAnnotatedExternFunctionsInAST(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "annotated_extern.elisa")
	src := "struct Holder:\n    value: i32&\n\nstruct Window:\n    items: view[Holder]\n\n@borrows_return(window.items[*])\nextern borrow_value(window: Window) -> view[Holder]\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write annotated extern fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"@borrows_return(window.items[*])", "extern borrow_value(1 params) -> view[Holder]"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
	for _, check := range []string{"struct Holder (1 fields)", "struct Window (1 fields)"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIPrintsConstEnumInAST(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_ast.elisa")
	src := "const enum JsonNodeKind of i8:\n    Invalid = -1\n    Null\n    Bool = 1\n    String\n\ndef current_kind() -> JsonNodeKind:\n    return JsonNodeKind.String\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum AST fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"const enum JsonNodeKind of i8: (4 members)", "def current_kind(0 params) -> JsonNodeKind (1 stmts)"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLICompilesConstEnumSourceToLLVM(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_llvm.elisa")
	src := "const enum JsonNodeKind of i8:\n    Invalid = -1\n    Null\n    Bool = 1\n    String\n\nconst DEFAULT_KIND: JsonNodeKind = JsonNodeKind.String\n\ndef kind_raw(kind: JsonNodeKind) -> i8:\n    return kind.i8()\n\ndef is_string(kind: JsonNodeKind) -> bool:\n    return kind == JsonNodeKind.String\n\ndef default_kind() -> JsonNodeKind:\n    return DEFAULT_KIND\n\ndef make_kind() -> JsonNodeKind:\n    return 1.i8().JsonNodeKind()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum LLVM fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i8 @kind_raw(i8", "define i1 @is_string(i8", "define i8 @default_kind()", "define i8 @make_kind()", "ret i8 1"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesConstEnumFlagsToLLVM(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixtureDir, err := os.MkdirTemp(repoRoot, ".tmp-const-enum-flags-*")
	if err != nil {
		t.Fatalf("failed to create const enum flags fixture dir: %v", err)
	}
	defer os.RemoveAll(fixtureDir)
	fixturePath := filepath.Join(fixtureDir, "const_enum_flags.elisa")
	src := "include \"../compiler/runtime/elisacore_std/elisacore_runtime.elisa\"\n\nconst enum RoutineFlag of u8:\n    External\n    Forward\n    VarArgs\n    Export\n\n\ndef build_flags() -> Flags[RoutineFlag]:\n    out: mutable Flags[RoutineFlag] = flags.new()\n    out.add(RoutineFlag.External)\n    out.add(RoutineFlag.VarArgs)\n    return out\n\n\ndef has_varargs(value: Flags[RoutineFlag]&) -> bool:\n    return value[RoutineFlag.VarArgs]\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum flags LLVM fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"%Flags__RoutineFlag = type { i64 }", "define %Flags__RoutineFlag @build_flags()", "define i1 @has_varargs(ptr", "flags.has"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesConstEnumBitwiseOpsToLLVM(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_bitwise.elisa")
	src := "const enum CpuFlag of u8:\n    C = 1\n    Z = 2\n    I = 4\n    N = 128\n\n\ndef mask() -> u8:\n    return CpuFlag.C | CpuFlag.I\n\n\ndef has_zero(p: u8) -> bool:\n    return (p & CpuFlag.Z) != 0\n\n\ndef clear_negative(p: u8) -> u8:\n    return p & (CpuFlag.N ^ 0xFF)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum bitwise fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected runCLI to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, check := range []string{"define i8 @mask()", "ret i8 5", "define i1 @has_zero(i8", "andtmp", "define i8 @clear_negative(i8"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLIRejectsLegacyCastSyntax(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_cast_error.elisa")
	src := "const VALUE: i64 = 1.cast[i64]()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy cast fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy cast syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy cast syntax `.cast[T]()` is no longer supported") {
		t.Fatalf("expected legacy cast parser error on stderr, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsLegacyReverseIterableLoopSyntax(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_reverse_iter_error.elisa")
	src := "def walk(items: darray[int]) -> void:\n    for rev value in items:\n        pass\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy reverse iterable fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy reverse iterable syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported") {
		t.Fatalf("expected legacy reverse iterable parser error on stderr, got:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no formatter output on parser failure, got:\n%s", stdout.String())
	}
}
func TestRunCLIRejectsLegacyReprCStructSyntax(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "legacy_repr_c_struct_error.elisa")
	src := "repr(c) struct Holder:\n    value: i32&\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write legacy repr(c) fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "ast", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for legacy repr(c) syntax, stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "legacy `repr(c) struct` syntax is no longer supported") {
		t.Fatalf("expected legacy repr(c) parser error on stderr, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsInternalRuntimeCarrierTypes(t *testing.T) {
	prev, hadPrev := os.LookupEnv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	defer func() {
		if hadPrev {
			_ = os.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", prev)
		} else {
			_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
		}
	}()

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "runtime_carrier_warning.elisa")
	src := "extern take_view(view: StringView) -> void\nextern take_raw[T](values: DynArray[T]) -> void\nextern take_window(view: DynArrayView) -> void\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write runtime carrier rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interface", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail for runtime carrier types, stdout:\n%s", stdout.String())
	}
	for _, want := range []string{
		`internal runtime carrier type "StringView" is not supported in user-facing code; use "sview[...]" instead`,
		`internal runtime carrier type "DynArray" is not supported in user-facing code; use "darray[T, shape]" instead`,
		`internal runtime carrier type "DynArrayView" is not supported in user-facing code; use "view[T]" instead`,
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected runtime carrier rejection %q, got:\n%s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no AST output on runtime carrier rejection, got:\n%s", stdout.String())
	}
}
func TestRunCLIFmtNormalizesSingleStatementGrantBlocks(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_single_use.elisa")
	src := "def write_once(text: u8&) -> int:\n    can Console.Write:\n        return puts(text)\n\ndef assign_once(target: mutable i64&):\n    can Memory.Allocate:\n        target <- alloc_value()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write single-use grant fixture: %v", err)
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
		"return puts(text) can Console.Write",
		"target <- alloc_value() can Memory.Allocate",
	} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}
	for _, forbidden := range []string{"can Console.Write:", "can Memory.Allocate:"} {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("expected formatter to inline %q, got:\n%s", forbidden, formatted)
		}
	}
}
func TestRunCLIFmtKeepsPanicGrantBlocksInSurfaceSyntax(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_panic.elisa")
	src := "def boom():\n    can Abort.Panic:\n        panic(\"boom\")\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write panic grant fixture: %v", err)
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
	if !strings.Contains(formatted, "can Abort.Panic:\n        panic(\"boom\")") {
		t.Fatalf("expected formatter to preserve the panic grant block, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "can can[") || strings.Contains(formatted, "panic(\"boom\") can Abort.Panic") {
		t.Fatalf("expected formatter to keep surface grant syntax for panic blocks, got:\n%s", formatted)
	}
}
func TestRunCLIFmtKeepsSignalSurfaceSyntax(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_signal.elisa")
	src := "permission FooEffect:\n    pass\n\npermission ConsoleEffect:\n    Write\n\ndef run() -> void:\n    can FooEffect, ConsoleEffect.Write:\n        signal FooEffect\n        signal ConsoleEffect.Write\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write signal grant fixture: %v", err)
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
	if !strings.Contains(formatted, "permission FooEffect:") {
		t.Fatalf("expected formatter to preserve permission declarations, got:\n%s", formatted)
	}
	for _, check := range []string{"signal FooEffect", "signal ConsoleEffect.Write"} {
		if !strings.Contains(formatted, check) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", check, formatted)
		}
	}
	if strings.Contains(formatted, "signal can[") {
		t.Fatalf("expected signal statements to stay in surface syntax, got:\n%s", formatted)
	}
	if err := os.WriteFile(fixturePath, []byte(formatted), 0o644); err != nil {
		t.Fatalf("failed to rewrite signal fixture with formatted output: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted signal output to round-trip, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on signal round-trip, got:\n%s", stderr.String())
	}
}
func TestRunCLIFmtRoundTripsTryReturnGrantBlocks(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grant_fmt_try_return.elisa")
	src := "error FormatError:\n    WriteFailed\n\nextern checked() -> int error[FormatError] can[Console.Format]\n\ndef run() -> int:\n    can Console.Format:\n        return try checked() else 1\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write try-return grant fixture: %v", err)
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
	if !strings.Contains(formatted, "return (try checked() else 1) can Console.Format") {
		t.Fatalf("expected formatter to inline try-return grant block, got:\n%s", formatted)
	}
	if err := os.WriteFile(fixturePath, []byte(formatted), 0o644); err != nil {
		t.Fatalf("failed to rewrite try-return fixture with formatted output: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "fmt", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected formatted try-return output to round-trip, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output on try-return round-trip, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "return (try checked() else 1) can Console.Format") {
		t.Fatalf("expected round-tripped formatter output to preserve inlined try-return grant, got:\n%s", stdout.String())
	}
}
