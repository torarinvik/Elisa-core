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
