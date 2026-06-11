package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestParseUnifiedElseRecoveryClauses(t *testing.T) {
	file, errs := parseSourceFile(t, `
error FileError:
	NotFound

extern read_value() -> i64 error[FileError]

def fallback_value(maybe: i64?) -> i64:
	propagated: i64 = try read_value()
	y: i64 = try read_value() else err:
		return 1
	return propagated + y
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[2])
	}
	propagatedDecl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected propagation local declaration, got %T", fn.Body[0])
	}
	propagatedExpr, ok := propagatedDecl.Value.(*ast.TryExpr)
	if !ok || propagatedExpr.Recovery != nil || propagatedExpr.Fallback != nil {
		t.Fatalf("expected bare try propagation expression, got %#v", propagatedDecl.Value)
	}
	yDecl, ok := fn.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected second local declaration, got %T", fn.Body[1])
	}
	yExpr, ok := yDecl.Value.(*ast.TryExpr)
	if !ok || yExpr.Recovery == nil || yExpr.Recovery.Kind != ast.RecoveryBlock || yExpr.Recovery.Binding != "err" {
		t.Fatalf("expected try else error-binding block recovery, got %#v", yDecl.Value)
	}
}

func TestParseImplicitElseUnwrapRejected(t *testing.T) {
	file, errs := parseSourceFile(t, `
error FileError:
	NotFound

def fallback_value(maybe: i64?) -> i64:
	x: i64 = maybe else return 0
	y: i64 = maybe else raise FileError.NotFound
	maybe else void
	return x + y
`)
	if len(errs) == 0 {
		t.Fatal("expected implicit `else` unwrap spellings to be rejected")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "implicit `else` unwrap has been removed") || !strings.Contains(joined, "`get <expr> else ...`") {
		t.Fatalf("expected implicit-else removal diagnostic, got: %s", joined)
	}
	fn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	xDecl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first local declaration, got %T", fn.Body[0])
	}
	xExpr, ok := xDecl.Value.(*ast.UnwrapElseExpr)
	if !ok || !xExpr.LegacyImplicitElse || xExpr.Recovery == nil || xExpr.Recovery.Kind != ast.RecoveryReturn {
		t.Fatalf("expected flagged else-return unwrap recovery, got %#v", xDecl.Value)
	}
	yDecl, ok := fn.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected second local declaration, got %T", fn.Body[1])
	}
	yExpr, ok := yDecl.Value.(*ast.UnwrapElseExpr)
	if !ok || !yExpr.LegacyImplicitElse || yExpr.Recovery == nil || yExpr.Recovery.Kind != ast.RecoveryRaise {
		t.Fatalf("expected flagged else-raise unwrap recovery, got %#v", yDecl.Value)
	}
	exprStmt, ok := fn.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement, got %T", fn.Body[2])
	}
	voidExpr, ok := exprStmt.Expr.(*ast.UnwrapElseExpr)
	if !ok || !voidExpr.LegacyImplicitElse || voidExpr.Recovery == nil || voidExpr.Recovery.Kind != ast.RecoveryVoid {
		t.Fatalf("expected flagged else-void unwrap recovery, got %#v", exprStmt.Expr)
	}
}

// The legacy `try? ... default` recovery form has been removed: it is now a hard parser error
// pointing at the modern `try ... else ...`.
func TestParseTryQuestionDefaultRejected(t *testing.T) {
	_, errs := parseSourceFile(t, `
extern read_value() -> i64

def f() -> i64:
	return try? read_value() default 2
`)
	if len(errs) == 0 {
		t.Fatalf("expected `try? ... default` to be rejected")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "try? ... default") || !strings.Contains(joined, "try ... else") {
		t.Fatalf("expected a `try? ... default` removal diagnostic, got: %s", joined)
	}
}
