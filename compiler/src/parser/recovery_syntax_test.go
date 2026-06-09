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
	x: i64 = maybe else return 0
	y: i64 = try read_value() else err:
		return 1
	z: i64 = maybe else raise FileError.NotFound
	maybe else void
	return propagated + x + y + z
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
	xDecl, ok := fn.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first local declaration, got %T", fn.Body[1])
	}
	xExpr, ok := xDecl.Value.(*ast.UnwrapElseExpr)
	if !ok || xExpr.Recovery == nil || xExpr.Recovery.Kind != ast.RecoveryReturn {
		t.Fatalf("expected else return recovery, got %#v", xDecl.Value)
	}
	yDecl, ok := fn.Body[2].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected second local declaration, got %T", fn.Body[2])
	}
	yExpr, ok := yDecl.Value.(*ast.TryExpr)
	if !ok || yExpr.Recovery == nil || yExpr.Recovery.Kind != ast.RecoveryBlock || yExpr.Recovery.Binding != "err" {
		t.Fatalf("expected try else error-binding block recovery, got %#v", yDecl.Value)
	}
	zDecl, ok := fn.Body[3].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected third local declaration, got %T", fn.Body[3])
	}
	zExpr, ok := zDecl.Value.(*ast.UnwrapElseExpr)
	if !ok || zExpr.Recovery == nil || zExpr.Recovery.Kind != ast.RecoveryRaise {
		t.Fatalf("expected else raise recovery, got %#v", zDecl.Value)
	}
	exprStmt, ok := fn.Body[4].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement, got %T", fn.Body[4])
	}
	voidExpr, ok := exprStmt.Expr.(*ast.UnwrapElseExpr)
	if !ok || voidExpr.Recovery == nil || voidExpr.Recovery.Kind != ast.RecoveryVoid {
		t.Fatalf("expected else void recovery, got %#v", exprStmt.Expr)
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
