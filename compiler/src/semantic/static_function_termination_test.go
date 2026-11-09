package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeStaticFunctionRequiresReturnOnAllPaths(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_requires_return.elisa", `static def maybe(flag: bool) -> i64:
	if flag:
		return 1

def keep() -> void:
	static assert maybe(true) == 1
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, `static function "maybe" must return on all paths`) {
		t.Fatalf("expected static totality diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	if !strings.Contains(errors, `this if statement has no else branch`) {
		t.Fatalf("expected missing-else detail, got:\n%s", errors)
	}
}

func TestAnalyzeStaticFunctionReportsMissingCatchAllInMatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_match_missing_catchall.elisa", `const enum Op of i32:
	ADD = 1
	SUB = 2

static def classify(op: Op) -> i64:
	match op:
		Op.ADD:
			return 1

def keep() -> void:
	static assert classify(Op.ADD) == 1
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, `static function "classify" must return on all paths`) {
		t.Fatalf("expected static totality diagnostic, got:\n%s", errors)
	}
	if !strings.Contains(errors, `this match statement has no catch-all arm`) {
		t.Fatalf("expected missing catch-all detail, got:\n%s", errors)
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

func TestAnalyzeStaticFunctionAcceptsConstStepAndBound(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_const_step_bound.elisa", `const Step: i64 = 2
const Bound: i64 = 0

static def countdown(n: i64) -> i64:
	if n <= Bound:
		return 0
	return countdown(n - Step)

def keep() -> void:
	static assert countdown(6) == 0
`)
}

func TestAnalyzeStaticFunctionAcceptsTernaryBaseCase(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_ternary_base.elisa", `static def countdown(n: i64) -> i64:
	return 0 if n <= 0 else countdown(n - 1)

def keep() -> void:
	static assert countdown(4) == 0
`)
}

func TestAnalyzeStaticFunctionAcceptsInvertedTernaryBaseCase(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_accepts_inverted_ternary_base.elisa", `static def countdown(n: i64) -> i64:
	return countdown(n - 1) if n > 0 else 0

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
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, "uses `== 0` as the only visible base case") {
		t.Fatalf("expected weak signed zero-base diagnostic, got:\n%s", errors)
	}
	if !strings.Contains(errors, "`n <= 0`") {
		t.Fatalf("expected signed lower-bound suggestion, got:\n%s", errors)
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
