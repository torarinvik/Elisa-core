package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def sum(items: array[Expr, 3]) -> i64:
    total: mutable i64 = 0
    for item in items where Expr.Int(value):
        total <- total + value
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "iter.step"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersSubjectIsIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_subject_is_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def sum(items: array[Expr, 3]) -> i64:
    total: mutable i64 = 0
    for item in items where item is Expr.Int(value):
        total <- total + value
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "iter.step"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected subject-is iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTupleSubjectIsIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_tuple_subject_is_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def sum(items: array[Expr, 3]) -> i64:
    total: mutable i64 = 0
    for index, item in items.enumerate() where item is Expr.Int(value):
        total <- total + value + index
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "item.iter.tuple.field", "index.iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tuple subject-is iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersStructSubjectIsIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_struct_subject_is_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

struct Entry:
    index: i64
    item: Expr

def sum(entries: array[Entry, 3]) -> i64:
    total: mutable i64 = 0
    for {index, item} in entries where item is Expr.Int(value):
        total <- total + value + index
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "item.iter.field", "index.iter.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected struct subject-is iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBareIterablePatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_iter_bare_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def count_ints(items: array[Expr, 3]) -> i64:
    total: mutable i64 = 0
    for item in items where Expr.Int:
        total <- total + 1
    return total
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"iter.pattern.filter.body", "match.tag", "iter.step"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected bare iterable pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersAllQueryPatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_all_query_pattern_filter.elisa", `
enum Stmt:
    BreakStmt
    ReturnStmt

def all_breaks(items: array[Stmt, 3]) -> bool:
    return all item in items where item is Stmt.BreakStmt
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"match.expr", "match.tag", "define i1 @all_breaks"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected all-query pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersAllQueryGuardedPatternFilter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_all_query_guarded_pattern_filter.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def all_positive_ints(items: array[Expr, 3]) -> bool:
    return all item in items where item is Expr.Int(value): value > 0
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"match.expr", "match.tag", "icmp sgt", "define i1 @all_positive_ints"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded all-query pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTupleSubjectQueryPatternFilters(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_tuple_subject_query_pattern_filters.elisa", `
enum Expr:
    Int(value: i64)
    Missing

def any_positive_after_index(items: darray[Expr]) -> bool:
    return any index, item in items.enumerate() where item is Expr.Int(value): value > index

def all_positive_after_index(items: darray[Expr]) -> bool:
    return all index, item in items.enumerate() where item is Expr.Int(value): value > index

def count_positive_after_index(items: darray[Expr]) -> usize:
    return count index, item in items.enumerate() where item is Expr.Int(value): value > index

def first_positive_after_index(items: darray[Expr]) -> i64?:
    return value for first index, item in items.enumerate() where item is Expr.Int(value): value > index

def positive_after_index(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each index, item in items.enumerate() where item is Expr.Int(value): value > index
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{
		"define i1 @any_positive_after_index",
		"define i1 @all_positive_after_index",
		"define i64 @count_positive_after_index",
		"define %Optional__i64 @first_positive_after_index",
		"define %DynArray__i64 @positive_after_index",
		"iter.pattern.filter.body",
		"match.tag",
		"icmp sgt",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tuple subject query pattern filter IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedOrPattern(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_nested_or_pattern.elisa", `
enum Token:
    Ident
    Keyword
    Other

enum Expr:
    Leaf(kind: Token)
    Missing

def score(expr: Expr) -> i64:
    match expr:
        Expr.Leaf(Token.Ident | Token.Keyword):
            return 1
        _:
            return 0
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"match.or.next", "match.tag", "define i64 @score("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected nested or-pattern IR to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBindingNestedOrPattern(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_binding_nested_or_pattern.elisa", `
enum Token:
    Ident(value: i64)
    Keyword(value: i64)
    Other

enum Expr:
    Leaf(kind: Token)
    Missing

def score(expr: Expr) -> i64:
    match expr:
        Expr.Leaf(Token.Ident(value) | Token.Keyword(value)):
            return value
        _:
            return 0
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, check := range []string{"match.or.next", "value.or", "define i64 @score("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected binding nested or-pattern IR to contain %q, got:\n%s", check, output)
		}
	}
}
