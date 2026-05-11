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
