package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeStaticAssertAcceptsTrueConstant(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_assert_true.elisa", `def keep() -> void:
	static assert 5 > 3
`)
}

func TestAnalyzeTopLevelStaticAssertAcceptsTrueConstant(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "top_level_static_assert_true.elisa", `static assert 5 > 3

def keep() -> void:
	pass
`)
}

func TestAnalyzeStaticAssertRejectsFalseConstant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_assert_false.elisa", `def keep() -> void:
	static assert 5 < 3, "math broke"
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert failed: math broke") {
		t.Fatalf("expected static assert failure diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeTopLevelStaticAssertRejectsFalseConstant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "top_level_static_assert_false.elisa", `static assert 5 < 3, "math broke"

def keep() -> void:
	pass
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert failed: math broke") {
		t.Fatalf("expected top-level static assert failure diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticAssertRequiresBoolCondition(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_assert_non_bool.elisa", `def keep() -> void:
	static assert 5
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static assert condition must be bool") {
		t.Fatalf("expected static assert bool diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticBlockAcceptsStaticOnlyStatements(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_block_ok.elisa", `def keep() -> void:
	static:
		assert 5 > 3
		if true:
			assert 8 > 4
		else:
			assert false, "inactive"
		static:
			assert true
`)
}

func TestAnalyzeStaticBlockRejectsRuntimeStatements(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_block_runtime_stmt.elisa", `def keep() -> void:
	pass

def run() -> void:
	static:
		keep()
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "static expression statement must evaluate at compile time") {
		t.Fatalf("expected static-only block diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeRejectsRuntimeCallToStaticFunction(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_runtime_call.elisa", `static def answer() -> i64:
	return 42

def keep() -> i64:
	return answer()
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `static function "answer" can only be called from a static context`) {
		t.Fatalf("expected static function runtime-call diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticAssertCanCallStaticFunction(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_static_assert.elisa", `static def answer(step: i64) -> i64:
	return step + 40

def keep() -> void:
	static assert answer(2) == 42
`)
}

func TestAnalyzeStaticExpressionStatementCanCallStaticFunction(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_static_stmt.elisa", `static def answer(step: i64) -> i64:
	return step + 40

def keep() -> void:
	static answer(2)
	static:
		answer(2)
`)
}

func TestAnalyzeStaticFunctionCanUseLocalsAndCompileTimeIf(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_locals.elisa", `static def answer(step: i64) -> i64:
	value: mutable i64 = step + 40
	if value == 42:
		return value
	return 0

def keep() -> void:
	static assert answer(2) == 42
	static:
		local: i64 = answer(2)
		assert local == 42
`)
}
