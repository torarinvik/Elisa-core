//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRSkipsStaticFunctionDecls(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "static_def_skip.elisa", `static def answer() -> i64:
    return 42

def keep() -> i64:
    return 0
`)
	ir, err := GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	if strings.Contains(ir, "@answer") {
		t.Fatalf("expected static function to be compile-time-only, got IR:\n%s", ir)
	}
}

func TestGenerateLLVMIRChecksStaticAssertCallingStaticFunction(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_assert.elisa", `static def answer(step: i64) -> i64:
    return step + 40

def keep() -> void:
    static assert answer(2) == 42
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticExpressionStatement(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_stmt.elisa", `static def answer(step: i64) -> i64:
    return step + 40

def keep() -> void:
    static answer(2)
    static:
        answer(2)
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticLocalsAndIf(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_locals.elisa", `static def answer(step: i64) -> i64:
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
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticLoops(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_loops.elisa", `static def sum_to(limit: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<limit:
        total <- total + i
    while total < 10:
        total <- total + 1
    return total

def keep() -> void:
    static assert sum_to(4) == 10
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticNamedAndDefaultArgs(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_named_defaults.elisa", `static def answer(step: i64, base: i64 = 40) -> i64:
    return base + step

def keep() -> void:
    static assert answer(step: 2) == 42
    static assert answer(base: 41, step: 1) == 42
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticMatch(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_match.elisa", `const enum Op of i32:
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
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticConstAggregates(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_const_aggregates.elisa", `static def score() -> i64:
    return (4, 9)[1] + ([3, 5, 7][2])

def keep() -> void:
    static assert score() == 16
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticConstIndexFallback(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_const_index_fallback.elisa", `static def read_or_default() -> i64:
    items: i64[3] = [3, 5, 7]
    return get items[9] else 11

def keep() -> void:
    static assert read_or_default() == 11
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticConstAggregateLocals(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_const_aggregate_locals.elisa", `static def score() -> i64:
    pair = 4, 9
    items = [3, 5, 7]
    return pair[1] + items[2]

def keep() -> void:
    static assert score() == 16
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticDArrayPush(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_darray_push.elisa", `static def score() -> i64:
    items: mutable darray[i64] = []
    items.push(4)
    items.push(9)
    return items[0] + items[1]

def keep() -> void:
    static assert score() == 13
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticDArrayPushValueSemantics(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_darray_push_value_semantics.elisa", `static def score() -> i64:
    left: mutable darray[i64] = []
    left.push(4)
    right: mutable darray[i64] = left
    right.push(9)
    return left[0] + right[1]

def keep() -> void:
    static assert score() == 13
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticDArrayExtendAndCount(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_darray_extend_count.elisa", `static def score() -> i64:
    items: mutable darray[i64] = []
    chunk: i64[3] = [4, 9, 12]
    items.extend(chunk)
    return items.count + items[2]

def keep() -> void:
    static assert score() == 15
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticConstQueries(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_const_queries.elisa", `static def score() -> i64:
    items: mutable darray[i64] = []
    chunk: i64[3] = [4, 9, 12]
    items.extend(chunk)
    assert any item in items where item == 9
    assert all item in items where item > 0
    assert (count item in items where item > 8) == 2
    selected = each item in items where item > 8
    assert selected.count == 2
    first_large = first item in items where item > 8
    missing = first item in items where item > 20
    return selected[0] + selected[1] + (get first_large else 0) + (get missing else 5)

def keep() -> void:
    static assert score() == 35
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticTupleBinderConstQueries(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_tuple_query_binders.elisa", `static def score() -> i64:
    pairs = [(1, 4), (5, 3), (2, 9)]
    selected = each left, right in pairs where left < right
    assert selected.count == 2
    return selected[0][0] + selected[0][1] + selected[1][0] + selected[1][1]

def keep() -> void:
    static assert score() == 16
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticTupleBinderIterLoops(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_tuple_iter_binders.elisa", `static def score() -> i64:
    pairs = [(1, 4), (5, 3), (2, 9)]
    total: mutable i64 = 0
    for left, right in pairs where left < right:
        total <- total + left + right
    return total

def keep() -> void:
    static assert score() == 16
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticPatternFiltersInQueriesAndLoops(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_pattern_filters.elisa", `struct Pair:
    left: i64
    right: i64

static def score() -> i64:
    values = [Pair{left: 1, right: 4}, Pair{left: 5, right: 3}, Pair{left: 1, right: 2}]
    selected = each pair in values where pair is Pair{left: 1, right: value}
    total: mutable i64 = 0
    for pair in values where pair is Pair{left: 1, right: value}:
        total <- total + value
    return selected.count + total

def keep() -> void:
    static assert score() == 8
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticListPatternFiltersInQueriesAndLoops(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_list_pattern_filters.elisa", `static def score() -> i64:
    values = [[1, 4], [5, 3], [1, 2]]
    selected = each pair in values where pair is [1, value]
    total: mutable i64 = 0
    for pair in values where pair is [1, value]:
        total <- total + value
    return selected.count + total

def keep() -> void:
    static assert score() == 8
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticPatternFiltersSkipNonMatches(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_pattern_filters_skip_nonmatches.elisa", `struct Pair:
    left: i64
    right: i64

static def score() -> i64:
    records = [Pair{left: 1, right: 4}, Pair{left: 2, right: 8}]
    record_matches = each pair in records where pair is Pair{left: 9, right: value}
    lists = [[1, 4], [2, 8]]
    list_matches = each pair in lists where pair is [9, value]
    total: mutable i64 = 0
    for pair in records where pair is Pair{left: 9, right: value}:
        total <- total + value
    for pair in lists where pair is [9, value]:
        total <- total + value
    return record_matches.count + list_matches.count + total

def keep() -> void:
    static assert score() == 0
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}

func TestGenerateLLVMIREvaluatesStaticVoidFunctionFallthrough(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_static_def_void_fallthrough.elisa", `static def note() -> void:
    value: i64 = 42

def keep() -> void:
    static note()
    static:
        note()
`)
	if _, err := GenerateLLVMIR(result); err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
}
