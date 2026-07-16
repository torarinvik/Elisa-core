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
    return value in {1..=3, 8..<10}
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

func TestGenerateLLVMIRLowersDirectCharMembershipRangeExpr(t *testing.T) {
	src := `def keep(ch: char) -> bool:
    return ch in '0'..'9'
`

	result := parseAndAnalyzeBackendTest(t, "backend_direct_char_membership_range.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(", "membership.range.lower", "membership.range.upper", "membership.range"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected direct char membership range lowering to include %q, got:\n%s", check, output)
		}
	}
}

func removedGenerateLLVMIRLowersCharsetMembershipExpr(t *testing.T) {
	src := `charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'

def keep(ch: char) -> bool:
    return ch in IdentStart
`

	result := parseAndAnalyzeBackendTest(t, "backend_charset_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(", "membership.range.lower", "membership.range.upper", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected charset membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func removedGenerateLLVMIRLowersReferencedCharsetMembershipExpr(t *testing.T) {
	src := `charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'
charset Digit = '0'..'9'
charset IdentContinue = IdentStart | Digit

def keep(ch: char) -> bool:
    return ch in IdentContinue
`

	result := parseAndAnalyzeBackendTest(t, "backend_referenced_charset_membership.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @keep(", "membership.range.lower", "membership.range.upper", "membership.next.2", "membership.result"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected referenced charset membership lowering to include %q, got:\n%s", check, output)
		}
	}
}

func removedGenerateLLVMIRLowersKeywordMapFunction(t *testing.T) {
	src := `const enum LuaTokenKind of i16:
    NAME = 0
    AND = 1
    BREAK = 2

keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "break" => .BREAK
    _ => .NAME
`

	result := parseAndAnalyzeBackendTest(t, "backend_keywordmap.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i16 @lua_keyword", "match.next", "svlit.len.eq", "ret i16"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected keywordmap lowering to include %q, got:\n%s", check, output)
		}
	}
}

func removedGenerateLLVMIRLowersCStrKeywordMapFunction(t *testing.T) {
	src := `const enum TokenKind of i16:
    NAME = 0
    PROGRAM = 1

keywordmap token_kind_for_text: cstr -> TokenKind:
    "program" => .PROGRAM
    _ => .NAME
`

	result := parseAndAnalyzeBackendTest(t, "backend_cstr_keywordmap.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i16 @token_kind_for_text", "match.next", "cstrlit.len.eq", "ret i16"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected cstr keywordmap lowering to include %q, got:\n%s", check, output)
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
    return kind in {.IF..=IDENT, .NUMBER..<STRING}
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

func removedGenerateLLVMIRLowersTokenSetMembershipExpr(t *testing.T) {
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
