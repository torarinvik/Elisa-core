package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIPrintsPostfixCastHookSyntaxAsPostfixShorthandInAST(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_ast.elisa")
	src := "const VALUE: i64 = 1.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast hook AST fixture: %v", err)
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
	for _, check := range []string{"const VALUE = 1.i64()"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected AST output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLICompilesBuiltinPostfixCastWithoutHook(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "builtin_postfix_cast_llvm.elisa")
	src := "def via_postfix() -> i64:\n    return 21.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write builtin postfix cast fixture: %v", err)
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
	if !strings.Contains(output, "define i64 @via_postfix(") {
		t.Fatalf("expected LLVM output to contain via_postfix definition, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @cast__") {
		t.Fatalf("expected builtin postfix cast to avoid __cast__ hook lowering, got:\n%s", output)
	}
}

func TestRunCLICompilesBuiltinTypeConstructorCastWithoutHook(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "builtin_type_constructor_cast_llvm.elisa")
	src := "def via_constructor() -> i64:\n    return i64(21)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write builtin type-constructor cast fixture: %v", err)
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
	if !strings.Contains(output, "define i64 @via_constructor(") {
		t.Fatalf("expected LLVM output to contain via_constructor definition, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @cast__") {
		t.Fatalf("expected builtin type-constructor cast to avoid __cast__ hook lowering, got:\n%s", output)
	}
}

func TestRunCLICompilesPostfixCastHookToHookCall(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_llvm.elisa")
	src := "enum Op:\n    Add\n    Sub\n\ndef __cast__(op: Op) -> i64:\n    if op == Op.Add:\n        return 10\n    return 20\n\ndef via_postfix(op: Op) -> i64:\n    return op.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast hook LLVM fixture: %v", err)
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
	for _, check := range []string{
		"define i64 @cast__Op__to__i64__L5_C1(",
		"define i64 @via_postfix(",
		"call i64 @cast__Op__to__i64__L5_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesTypeConstructorCastHookToHookCall(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "type_constructor_cast_hook_llvm.elisa")
	src := "enum Op:\n    Add\n    Sub\n\ndef __cast__(op: Op) -> i64:\n    if op == Op.Add:\n        return 10\n    return 20\n\ndef via_constructor(op: Op) -> i64:\n    return i64(op)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write type-constructor cast hook LLVM fixture: %v", err)
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
	for _, check := range []string{
		"define i64 @cast__Op__to__i64__L5_C1(",
		"define i64 @via_constructor(",
		"call i64 @cast__Op__to__i64__L5_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestRunCLICompilesOptionalPostfixCastHookToHookCall(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "optional_postfix_cast_hook_llvm.elisa")
	src := "enum Op:\n    Add\n    Sub\n\ndef __cast__(op: Op) -> i64?:\n    if op == Op.Add:\n        return 10\n    return null\n\ndef via_optional_postfix(op: Op) -> i64?:\n    return op.i64?()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write optional postfix cast hook LLVM fixture: %v", err)
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
	for _, check := range []string{
		"define %Optional__i64 @cast__Op__to__i64__L5_C1(",
		"define %Optional__i64 @via_optional_postfix(",
		"call %Optional__i64 @cast__Op__to__i64__L5_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLICompilesMultiplePostfixCastHooksInOneFile(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "multiple_postfix_cast_hooks.elisa")
	src := "const enum LuaUnaryOp of i8:\n    NEGATE = 0\n    NOT = 1\n\nconst enum LuaBinaryOp of i8:\n    ADD = 0\n    SUB = 1\n\ndef __cast__(op: LuaBinaryOp) -> i64:\n    if op == LuaBinaryOp.ADD:\n        return 3\n    return i64(op) + 5\n\ndef __cast__(op: LuaUnaryOp) -> i64:\n    if op == LuaUnaryOp.NEGATE:\n        return 29\n    return 31\n\ndef binary_score(op: LuaBinaryOp) -> i64:\n    return op.i64()\n\ndef unary_score(op: LuaUnaryOp) -> i64:\n    return op.i64()\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write multi-hook fixture: %v", err)
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
	for _, check := range []string{
		"define i64 @cast__LuaBinaryOp__to__i64__L9_C1(",
		"define i64 @cast__LuaUnaryOp__to__i64__L14_C1(",
		"call i64 @cast__LuaBinaryOp__to__i64__L9_C1(",
		"call i64 @cast__LuaUnaryOp__to__i64__L14_C1(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected LLVM output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestRunCLIRejectsArrowCastWhenOnlyPostfixHookExists(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_reject_arrow.elisa")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef bad(op: Op) -> i64:\n    return op -> i64\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write postfix cast rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "the `expr -> T` cast has been removed") {
		t.Fatalf("expected legacy arrow cast diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsDotCastSyntaxWhenOnlyPostfixHookExists(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "postfix_cast_hook_reject_dot_cast.elisa")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef bad(op: Op) -> i64:\n    return op.cast[i64]\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write dot-cast rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid cast from Op to i64") {
		t.Fatalf("expected explicit .cast[T] diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsDuplicatePostfixCastHooksForSamePair(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "duplicate_postfix_cast_hooks.elisa")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op) -> i64:\n    return 10\n\ndef __cast__(op: Op) -> i64:\n    return 20\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write duplicate cast-hook fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "duplicate __cast__ hook for Op -> i64") {
		t.Fatalf("expected duplicate cast-hook diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsPostfixCastHookWithWrongArity(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "invalid_postfix_cast_hook_arity.elisa")
	src := "enum Op:\n    Add\n\ndef __cast__(op: Op, extra: i64) -> i64:\n    return extra\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write invalid cast-hook fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "must take exactly 1 parameter, got 2") {
		t.Fatalf("expected cast-hook arity diagnostic, got:\n%s", stderr.String())
	}
}
func TestRunCLIRejectsImplicitIntReturnToConstEnum(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "const_enum_reject.elisa")
	src := "const enum Kind of i8:\n    A\n\ndef bad() -> Kind:\n    return 0\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write const enum rejection fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "llvm", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected runCLI to fail, got stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "expects Kind, got int") {
		t.Fatalf("expected const enum type mismatch diagnostic, got:\n%s", stderr.String())
	}
}
