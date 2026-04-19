package parser

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func TestParseMembershipExprUsesComparisonPrecedence(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64, other: bool) -> bool:\n    return value in [1, 2, 3] and other\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	andExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || andExpr.Op != lexer.TOKEN_AND {
		t.Fatalf("expected top-level and expr, got %#v", ret.Value)
	}
	inExpr, ok := andExpr.Left.(*ast.BinaryExpr)
	if !ok || inExpr.Op != lexer.TOKEN_IN {
		t.Fatalf("expected left side membership expr, got %#v", andExpr.Left)
	}
	list, ok := inExpr.Right.(*ast.ListLitExpr)
	if !ok || len(list.Elems) != 3 {
		t.Fatalf("expected three-element list literal, got %#v", inExpr.Right)
	}
}

func TestParseMembershipExprAllowsNonLiteralRightHandSide(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64, xs: i64[2]) -> bool:\n    return value in xs\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	inExpr, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || inExpr.Op != lexer.TOKEN_IN {
		t.Fatalf("expected membership binary expr, got %#v", ret.Value)
	}
	if _, ok := inExpr.Right.(*ast.Ident); !ok {
		t.Fatalf("expected non-literal rhs ident, got %T", inExpr.Right)
	}
}

func TestParseMatchStoreRemainsTrailingIn(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Store:\n    value: int\n\ndef keep(value: int, store: Store) -> int:\n    match value in store:\n        _:\n            return 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match stmt, got %T", decl.Body[0])
	}
	if matchStmt.Store == nil {
		t.Fatal("expected match store to be preserved")
	}
	if _, ok := matchStmt.Value.(*ast.BinaryExpr); ok {
		t.Fatalf("expected match head to remain plain expr, got %#v", matchStmt.Value)
	}
	if _, ok := matchStmt.Store.(*ast.Ident); !ok {
		t.Fatalf("expected store ident, got %T", matchStmt.Store)
	}
}

func TestParseOpenStatementRemainsTrailingIn(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Lit(value: int)\n\ndef keep(node: Expr, store: Expr.Store[Local]) -> int:\n    open node in store as Expr.Lit(value: value):\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	openStmt, ok := decl.Body[0].(*ast.OpenStmt)
	if !ok {
		t.Fatalf("expected open stmt, got %T", decl.Body[0])
	}
	if openStmt.Store == nil {
		t.Fatal("expected open store to be preserved")
	}
	if _, ok := openStmt.Value.(*ast.BinaryExpr); ok {
		t.Fatalf("expected open value to remain plain expr, got %#v", openStmt.Value)
	}
}

func TestParseIfStoreBinderRemainsTrailingIn(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Lit(value: int)\n\ndef keep(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store as Expr.Lit(value: value):\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	if matchStmt.Store == nil {
		t.Fatalf("expected if store binder, got %#v", matchStmt)
	}
	if _, ok := matchStmt.Value.(*ast.BinaryExpr); ok {
		t.Fatalf("expected if value to remain plain expr, got %#v", matchStmt.Value)
	}
}

func TestParseIfMembershipConditionUsesNormalIfPath(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64) -> bool:\n    if value in [1, 2, 3]:\n        return true\n    return false\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected normal if stmt, got %T", decl.Body[0])
	}
	inExpr, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok || inExpr.Op != lexer.TOKEN_IN {
		t.Fatalf("expected membership condition, got %#v", ifStmt.Cond)
	}
}
