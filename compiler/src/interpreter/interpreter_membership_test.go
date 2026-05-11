package interpreter_test

import (
	"testing"

	"elisacore/src/interpreter"
)

func TestExecuteMembershipExprShortCircuitsListCandidates(t *testing.T) {
	src := `def run() -> bool:
    return 1 in [1, 1 / 0]
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_short_circuit.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "true" {
		t.Fatalf("expected membership short-circuit result true, got %s", got)
	}
}

func TestExecuteMembershipExprHandlesEmptyList(t *testing.T) {
	src := `def run() -> bool:
    return 1 in []
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_empty.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "false" {
		t.Fatalf("expected empty membership result false, got %s", got)
	}
}

func TestExecuteNotInMembershipExpr(t *testing.T) {
	src := `def run() -> bool:
    return 1 not in {2, 3}
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_not_in_membership.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "true" {
		t.Fatalf("expected not-in membership result true, got %s", got)
	}
}

func TestExecuteMembershipRangeExpr(t *testing.T) {
	src := `def run() -> bool:
    return 9 in {1..3, 8..<10}
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_range.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "true" {
		t.Fatalf("expected membership range result true, got %s", got)
	}
}

func TestExecuteMembershipRangeExprAcceptsConstEnumBounds(t *testing.T) {
	src := `const enum TokenKind of u32:
    IF
    LET
    IDENT
    NUMBER
    STRING

def keep(kind: TokenKind) -> bool:
    return kind in {.IF..IDENT}

def run() -> bool:
    return keep(.LET)
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_range_enum.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "true" {
		t.Fatalf("expected enum membership range result true, got %s", got)
	}
}

func TestExecuteMembershipRangeExprRejectsOutOfEnumRange(t *testing.T) {
	src := `const enum TokenKind of u32:
    IF
    LET
    IDENT
    NUMBER
    STRING

def keep(kind: TokenKind) -> bool:
    return kind in {.IF..IDENT}

def run() -> bool:
    return keep(.STRING)
`

	result := parseAndAnalyzeInterpreterTest(t, "interpreter_membership_range_enum_out.elisa", src)
	execResult, err := interpreter.Execute(result, interpreter.Options{Entry: "run"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := execResult.Return.String(); got != "false" {
		t.Fatalf("expected enum membership range result false, got %s", got)
	}
}
