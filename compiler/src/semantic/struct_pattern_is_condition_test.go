//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// Struct `is`-pattern condition coverage (formerly housed in the tree test file;
// the fixtures are pure structs and survived the docs/81 tree retirement).
func TestAnalyzeStructPatternIsConditionWithBindings(t *testing.T) {
	analyzeTreeTestSource(t, "struct_is_condition_bindings.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, span: Span(start: start), value: value):
		return start + value
	return 0
`)
}
func TestAnalyzeStructPatternIsConditionTruthyOrBindings(t *testing.T) {
	analyzeTreeTestSource(t, "struct_is_condition_truthy_or_bindings.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, value: value) or tok is Token(kind: 2, value: value):
		return value
	return 0
`)
}
func TestAnalyzeStructPatternIsConditionRejectsMissingNestedField(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_is_condition_missing_nested_field.elisa", `struct Span:
	start: i64
	finish: i64

struct Token:
	kind: i64
	span: Span
	value: i64

def score(tok: Token) -> i64:
	if tok is Token(kind: 1, span: Span(missing: start), value: value):
		return start + value
	return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `struct "Span" has no field "missing"`) {
		t.Fatalf("expected nested struct is-pattern diagnostic, got:\n%s", all)
	}
}
