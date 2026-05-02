package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
)

func TestAnalyzeMembershipExprUsesBoolAndArrayLiteralType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_expr.llcontext", `def keep(value: i64) -> bool:
    return value in [1, 2, 3]
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected membership binary expr, got %T", ret.Value)
	}
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected membership rhs list literal, got %T", inExpr.Right)
	}
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if builtin, ok := arrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected membership rhs element type i64, got %#v", arrayType.Elem)
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 3 {
		t.Fatalf("expected fixed-size membership array, got %#v", arrayType)
	}
}

func TestAnalyzeMembershipAllowsEmptyListLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_empty.llcontext", `def keep(value: i64) -> bool:
    return value in []
`)

	decl := result.File.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	list := inExpr.Right.(*ast.ListLitExpr)
	arrayType, ok := result.ExprTypes[list].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected empty membership rhs list type, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 0 {
		t.Fatalf("expected empty fixed-size membership array, got %#v", arrayType)
	}
}

func TestAnalyzeMembershipAllowsTokenSetDecl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "membership_tokenset.llcontext", `const enum TokenKind of u32:
    IF
    LET
    IDENT

tokenset ExprStart = [TokenKind.IF, TokenKind.LET]

def keep(kind: TokenKind) -> bool:
    return kind in ExprStart
`)

	decl := result.File.Decls[2].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr := ret.Value.(*ast.BinaryExpr)
	if got := result.ExprTypes[inExpr].String(); got != "bool" {
		t.Fatalf("expected membership expr type bool, got %s", got)
	}
	tokenSet := result.File.Decls[1].(*ast.TokenSetDecl)
	arrayType, ok := result.ExprTypes[tokenSet.Value].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected tokenset list type, got %T %#v", result.ExprTypes[tokenSet.Value], result.ExprTypes[tokenSet.Value])
	}
	if !arrayType.HasConstSize || arrayType.ConstSize != 2 {
		t.Fatalf("expected fixed-size tokenset array, got %#v", arrayType)
	}
}

func TestAnalyzeMembershipRejectsNonLiteralRightHandSide(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "membership_non_literal.llcontext", `def keep(value: i64, xs: i64[2]) -> bool:
    return value in xs
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "membership operator requires a list literal or tokenset on the right-hand side") {
		t.Fatalf("expected membership rhs diagnostic, got:\n%s", all)
	}
}
