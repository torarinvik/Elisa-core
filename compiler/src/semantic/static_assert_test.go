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

func TestAnalyzeStaticVoidFunctionMayFallThrough(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_void_fallthrough.elisa", `static def note() -> void:
	value: i64 = 42

def keep() -> void:
	static note()
	static:
		note()
`)
}

func TestAnalyzeStaticFunctionRejectsPanicAsReturnProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_panic_not_return_proof.elisa", `static def answer() -> i64:
	panic("boom")

def keep() -> void:
	pass
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `static function "answer" must return on all paths`) {
		t.Fatalf("expected static return diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestAnalyzeStaticErrorCountsAsTerminatingBranch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_static_error_terminates_branch.elisa", `static def answer(flag: bool) -> i64:
	if flag:
		return 42
	else:
		error("inactive")

def keep() -> void:
	static assert answer(true) == 42
`)
}

func TestAnalyzeStaticFunctionReportsActiveStaticError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "static_def_reports_active_static_error.elisa", `static def answer() -> i64:
	error("boom")

def keep() -> void:
	static assert answer() == 42
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, `static error: boom`) {
		t.Fatalf("expected static error diagnostic, got:\n%s", errors)
	}
	if strings.Contains(errors, `must return on all paths`) {
		t.Fatalf("did not expect return-path diagnostic when static error terminates, got:\n%s", errors)
	}
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

func TestAnalyzeStaticFunctionCanUseMatch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_match.elisa", `const enum Op of i32:
	ADD = 1
	SUB = 2
	MUL = 3

static def classify(op: Op) -> i64:
	match op:
		Op.ADD | Op.SUB:
			return 1
		_:
			return 3

def keep() -> void:
	static assert classify(Op.ADD) == 1
	static assert classify(Op.SUB) == 1
	static assert classify(Op.MUL) == 3
`)
}

func TestAnalyzeStaticFunctionCanUseConstTuplesAndLists(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_const_aggregates.elisa", `static def score() -> i64:
	return (4, 9)[1] + ([3, 5, 7][2])

def keep() -> void:
	static assert score() == 16
`)
}

func TestAnalyzeStaticFunctionConstIndexFallback(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_const_index_fallback.elisa", `static def read_or_default() -> i64:
	return [3, 5, 7][9] else 11

def keep() -> void:
	static assert read_or_default() == 11
`)
}

func TestAnalyzeStaticFunctionCanIndexConstAggregateLocals(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_const_aggregate_locals.elisa", `static def score() -> i64:
	pair = 4, 9
	items = [3, 5, 7]
	return pair[1] + items[2]

def keep() -> void:
	static assert score() == 16
`)
}

func TestAnalyzeStaticFunctionCanBuildDArrayWithPush(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_darray_push.elisa", `static def score() -> i64:
	items: mutable darray[i64] = []
	items.push(4)
	items.push(9)
	return items[0] + items[1]

def keep() -> void:
	static assert score() == 13
`)
}

func TestAnalyzeStaticFunctionDArrayPushUsesValueSemantics(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_darray_push_value_semantics.elisa", `static def score() -> i64:
	left: mutable darray[i64] = []
	left.push(4)
	right: mutable darray[i64] = left
	right.push(9)
	return left[0] + right[1]

def keep() -> void:
	static assert score() == 13
`)
}

func TestAnalyzeStaticFunctionCanBuildDArrayWithExtendAndCount(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_darray_extend_count.elisa", `static def score() -> i64:
	items: mutable darray[i64] = []
	chunk: i64[3] = [4, 9, 12]
	items.extend(chunk)
	return items.count + items[2]

def keep() -> void:
	static assert score() == 15
`)
}

func TestAnalyzeStaticFunctionCanUseConstQueries(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "static_def_const_queries.elisa", `static def score() -> i64:
	items: mutable darray[i64] = []
	chunk: i64[3] = [4, 9, 12]
	items.extend(chunk)
	assert any item in items where item == 9
	assert all item in items where item > 0
	assert (count item in items where item > 8) == 2
	selected = item for each item in items where item > 8
	assert selected.count == 2
	return selected[0] + selected[1]

def keep() -> void:
	static assert score() == 21
`)
}

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
