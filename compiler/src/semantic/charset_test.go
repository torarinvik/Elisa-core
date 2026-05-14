package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeCharsetAcceptsAsciiLiteralsAndRanges(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "charset_ascii.elisa", `charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected charset to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	if _, ok := result.File.Decls[0].(*ast.CharsetDecl); !ok {
		t.Fatalf("expected charset decl, got %T", result.File.Decls[0])
	}
}

func TestAnalyzeCharsetMembershipLowersToCandidateList(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "charset_membership.elisa", `charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'

def keep(ch: char) -> bool:
    return ch in IdentStart
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected charset membership to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	decl := result.File.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership expr, got %T", ret.Value)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || len(list.Elems) != 3 {
		t.Fatalf("expected charset membership to lower to three candidates, got %T %#v", inExpr.Right, inExpr.Right)
	}
	if _, ok := list.Elems[0].(*ast.MembershipRangeExpr); !ok {
		t.Fatalf("expected first charset candidate to lower to range, got %T", list.Elems[0])
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected charset membership type bool, got %s", got)
	}
}

func TestAnalyzeCharsetExpandsReferencesForMembership(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "charset_reference.elisa", `charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'
charset Digit = '0'..'9'
charset IdentContinue = IdentStart | Digit

def keep(ch: char) -> bool:
    return ch in IdentContinue
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected charset references to analyze cleanly, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
	decl := result.File.Decls[3].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || len(list.Elems) != 4 {
		t.Fatalf("expected referenced charset membership to expand to four candidates, got %T %#v", inExpr.Right, inExpr.Right)
	}
	if _, ok := list.Elems[3].(*ast.MembershipRangeExpr); !ok {
		t.Fatalf("expected referenced digit charset to expand to a range, got %T", list.Elems[3])
	}
}

func TestAnalyzeCharsetRejectsUnknownReference(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "charset_unknown_reference.elisa", `charset IdentContinue = IdentStart | '0'..'9'
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `unknown charset "IdentStart"`) {
		t.Fatalf("expected unknown charset reference diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCharsetRejectsReferenceCycles(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "charset_cycle.elisa", `charset A = B | 'a'
charset B = A | 'b'
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "reference cycle") {
		t.Fatalf("expected charset reference cycle diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCharsetRejectsDuplicateCharacters(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "charset_duplicate.elisa", `charset Bad = 'a'..'c' | 'b'
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "contains duplicate character") {
		t.Fatalf("expected duplicate character diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCharsetRejectsDescendingRanges(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "charset_descending_range.elisa", `charset Bad = 'z'..'a'
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "range start") {
		t.Fatalf("expected descending range diagnostic, got:\n%s", all)
	}
}
