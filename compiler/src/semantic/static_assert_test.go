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

func TestAnalyzeStaticFunctionCanUseBoundedLoops(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_loops.elisa", `static def sum_to(limit: i64) -> i64:
	total: mutable i64 = 0
	for i in 0..<limit:
		total <- total + i
	while total < 10:
		total <- total + 1
	return total

def keep() -> void:
	static assert sum_to(4) == 10
`)
}

func TestAnalyzeStaticFunctionSupportsNamedAndDefaultArgs(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_named_defaults.elisa", `static def answer(step: i64, base: i64 = 40) -> i64:
	return base + step

def keep() -> void:
	static assert answer(step: 2) == 42
	static assert answer(base: 41, step: 1) == 42
`)
}

func TestAnalyzeStaticFunctionRequiresReturnOnAllPaths(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_requires_return.elisa", `static def maybe(flag: bool) -> i64:
	if flag:
		return 1

def keep() -> void:
	static assert maybe(true) == 1
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `static function "maybe" must return on all paths`) {
		t.Fatalf("expected static totality diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticFunctionRejectsNonDecreasingRecursion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_rejects_partial_recursion.elisa", `static def loop(n: i64) -> i64:
	if n == 0:
		return 0
	return loop(n)

def keep() -> void:
	static assert loop(1) == 0
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `recursive static call to "loop" must decrease`) {
		t.Fatalf("expected static recursion diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticFunctionAcceptsDecreasingRecursion(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_total_recursion.elisa", `static def countdown(n: i64) -> i64:
	if n <= 0:
		return 0
	return countdown(n - 1)

def keep() -> void:
	static assert countdown(4) == 0
`)
}

func TestAnalyzeStaticFunctionAcceptsInvertedDecreasingRecursion(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_inverted_total_recursion.elisa", `static def countdown(n: i64) -> i64:
	if n > 0:
		return countdown(n - 1)
	return 0

def keep() -> void:
	static assert countdown(4) == 0
`)
}

func TestAnalyzeStaticFunctionAcceptsInvertedUnsignedZeroBaseCase(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_inverted_unsigned_zero_base.elisa", `static def countdown(n: u64) -> u64:
	if n != 0:
		return countdown(n - 1)
	return 0

def keep() -> void:
	static assert countdown(4) == 0
`)
}

func TestAnalyzeStaticFunctionRejectsDecreasingRecursionWithoutTotalBaseCase(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_rejects_decreasing_without_total_base.elisa", `static def countdown(n: i64) -> i64:
	if n == 0:
		return 0
	return countdown(n - 1)

def keep() -> void:
	static assert countdown(4) == 0
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `must have a visible terminating base case`) {
		t.Fatalf("expected static base-case diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticFunctionAcceptsUnsignedZeroBaseCase(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_unsigned_zero_base.elisa", `static def countdown(n: u64) -> u64:
	if n == 0:
		return 0
	return countdown(n - 1)

def keep() -> void:
	static assert countdown(4) == 0
`)
}

func TestAnalyzeStaticFunctionReportsCallDepthLimit(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_depth_limit.elisa", `static def countdown(n: u64) -> u64:
	if n == 0:
		return 0
	return countdown(n - 1)

def keep() -> void:
	static assert countdown(70) == 0
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `static function evaluation exceeded 64 calls`) {
		t.Fatalf("expected static call depth diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticFunctionRejectsIndirectRecursion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_rejects_indirect_recursion.elisa", `static def odd(n: i64) -> bool:
	if n <= 0:
		return false
	return even(n - 1)

static def even(n: i64) -> bool:
	if n <= 0:
		return true
	return odd(n - 1)

def keep() -> void:
	static assert even(4)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `indirect recursive static call cycle`) {
		t.Fatalf("expected indirect static recursion diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticFunctionAcceptsAcyclicStaticCall(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_acyclic_call.elisa", `static def base() -> i64:
	return 40

static def answer(step: i64) -> i64:
	return base() + step

def keep() -> void:
	static assert answer(2) == 42
`)
}
