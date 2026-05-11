//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersMembershipExpr(t *testing.T) {
	src := `def keep(value: i64) -> bool:
    return value in [1, 2, 3]
`

	result := parseAndAnalyzeBackendTest(t, "backend_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i64 ", "membership.next.0", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBraceMembershipExpr(t *testing.T) {
	src := `def keep(value: i64) -> bool:
    return value in {1, 2, 3}
`

	result := parseAndAnalyzeBackendTest(t, "backend_brace_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i64 ", "membership.next.0", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected brace membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNotInMembershipExpr(t *testing.T) {
	src := `def keep(value: i64) -> bool:
    return value not in {1, 2, 3}
`

	result := parseAndAnalyzeBackendTest(t, "backend_not_in_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i64 ", "membership.next.0", "membership.result", "nottmp"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected not-in membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBraceMembershipRangeExpr(t *testing.T) {
	src := `def keep(value: i64) -> bool:
    return value in {1..3, 8..<10}
`

	result := parseAndAnalyzeBackendTest(t, "backend_brace_membership_range.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i64 ", "membership.range.lower", "membership.range.upper", "membership.range"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected membership range lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBraceMembershipShorthandMembers(t *testing.T) {
	src := `const enum TokenKind of u32:
    IF
    LET
    IDENT

def keep(kind: TokenKind) -> bool:
    return kind in {.IF, .LET}
`

	result := parseAndAnalyzeBackendTest(t, "backend_brace_membership_shorthand.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i32 ", "membership.next.0", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected shorthand membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBraceMembershipRangeConstEnumBounds(t *testing.T) {
	src := `const enum TokenKind of u32:
    IF
    LET
    IDENT
    NUMBER
    STRING

def keep(kind: TokenKind) -> bool:
    return kind in {.IF..IDENT, .NUMBER..<STRING}
`

	result := parseAndAnalyzeBackendTest(t, "backend_brace_membership_range_enum.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i32 ", "membership.range.lower", "membership.range.upper", "membership.range"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enum membership range lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTokenSetMembershipExpr(t *testing.T) {
	src := `const enum TokenKind of u32:
    IF
    LET
    IDENT

tokenset ExprStart = [TokenKind.IF, TokenKind.LET]

def keep(kind: TokenKind) -> bool:
    return kind in ExprStart
`

	result := parseAndAnalyzeBackendTest(t, "backend_tokenset_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(i32 ", "membership.next.0", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tokenset membership lowering to include %q, got:\n%s", check, output)
		}
	}
}
