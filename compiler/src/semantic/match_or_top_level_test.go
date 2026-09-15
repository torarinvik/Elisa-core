package semantic

import (
	"strings"
	"testing"
)

func TestTopLevelStringOrPatternIsAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "top_level_string_or.elisa", `def classify(kind: sview) -> i64:
    match kind:
        "unary" or "move":
            return 1
        _:
            return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("top-level string alternatives should be accepted, got: %v", errs)
	}
}

func TestTopLevelEnumOrPatternIsAcceptedAndCoversEachVariant(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "top_level_enum_or.elisa", `enum Expr:
    Index
    IndexN
    Other

def classify(expression: Expr) -> i64:
    match expression:
        Expr.Index or Expr.IndexN:
            return 1
        Expr.Other:
            return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("top-level enum alternatives should be accepted and counted for coverage, got: %v", errs)
	}
}

func TestTopLevelEnumOrPatternRejectsUnmergedNamedBindings(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "top_level_enum_or_bindings.elisa", `enum Expr:
    Number(value: i64)
    Word(value: i64)

def value_of(expression: Expr) -> i64:
    match expression:
        Expr.Number(value: number) or Expr.Word(value: number):
            return number
`)
	if errs := result.Errors(); !strings.Contains(strings.Join(errs, "\n"), "top-level or-pattern alternatives that bind names are not supported") {
		t.Fatalf("matching types alone cannot merge branch-specific payload aliases, got: %v", errs)
	}
}
