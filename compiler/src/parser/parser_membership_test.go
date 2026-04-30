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

func TestParseTupleMatchHeadAndPatterns(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(length: int, source: dstr, start: usize) -> int:\n    match length, source[start], source[start + 1], source[start + 2], source[start + 3], source[start + 4]:\n        5, 'w', 'h', 'i', 'l', 'e':\n            return 1\n        _, _, _, _, _, _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match stmt, got %T", decl.Body[0])
	}
	head, ok := matchStmt.Value.(*ast.TupleExpr)
	if !ok || len(head.Elems) != 6 {
		t.Fatalf("expected six-element tuple match head, got %#v", matchStmt.Value)
	}
	firstArm, ok := matchStmt.Arms[0].Pattern.(*ast.MatchTuplePattern)
	if !ok || len(firstArm.Elems) != 6 {
		t.Fatalf("expected six-element tuple match pattern, got %#v", matchStmt.Arms[0].Pattern)
	}
	if _, ok := firstArm.Elems[0].(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected literal first tuple pattern element, got %T", firstArm.Elems[0])
	}
	if _, ok := firstArm.Elems[1].(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected char literal tuple pattern element, got %T", firstArm.Elems[1])
	}
	secondArm, ok := matchStmt.Arms[1].Pattern.(*ast.MatchTuplePattern)
	if !ok || len(secondArm.Elems) != 6 {
		t.Fatalf("expected wildcard tuple match pattern, got %#v", matchStmt.Arms[1].Pattern)
	}
	for i, elem := range secondArm.Elems {
		if _, ok := elem.(*ast.MatchWildcardPattern); !ok {
			t.Fatalf("expected wildcard tuple element %d, got %T", i, elem)
		}
	}
}

func TestParseRejectsRemovedOpenStatementSurface(t *testing.T) {
	_, errs := parseSourceFile(t, "packed enum Expr:\n    Lit(value: int)\n\ndef keep(node: Expr, store: Expr.Store[Local]) -> int:\n    open node in store as Expr.Lit(value: value):\n        return value\n    return 0\n")
	if len(errs) == 0 {
		t.Fatal("expected parser errors for removed open statement surface, got none")
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
	if !matchStmt.DeprecatedIfStorePatternBinder {
		t.Fatalf("expected deprecated if-store pattern binder marker, got %#v", matchStmt)
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

func TestParseIfParenthesizedMembershipConditionUsesNormalIfPath(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64) -> bool:\n    if (value in [1, 2, 3]):\n        return true\n    return false\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected normal if stmt, got %T", decl.Body[0])
	}
	paren, ok := ifStmt.Cond.(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected parenthesized membership condition, got %#v", ifStmt.Cond)
	}
	inExpr, ok := paren.Inner.(*ast.BinaryExpr)
	if !ok || inExpr.Op != lexer.TOKEN_IN {
		t.Fatalf("expected membership condition, got %#v", paren.Inner)
	}
}
