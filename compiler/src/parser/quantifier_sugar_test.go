package parser

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// lawPredicate extracts the single predicate expression from a parsed law decl.
func lawPredicate(t *testing.T, file *ast.File) ast.Expr {
	t.Helper()
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.IsLaw && len(fd.Body) == 1 {
			if ret, ok := fd.Body[0].(*ast.ReturnStmt); ok {
				return ret.Value
			}
		}
	}
	t.Fatalf("no law predicate found")
	return nil
}

// `forall i in a.indices: P` desugars to `forall i: (not (0 <= i and i < a.count)) or P` — a plain
// QuantifierExpr over the canonical guarded form, no new AST node (docs/100).
func TestQuantifierIndicesSugarDesugars(t *testing.T) {
	src := "law AllNonNeg(self: array[i64, 8]) = forall i in self.indices: self[i] >= 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	if q.Exists || len(q.Vars) != 1 || q.Vars[0] != "i" {
		t.Fatalf("expected single forall binder i, got %+v", q)
	}
	// Body is `(not guard) or P`.
	or, ok := q.Body.(*ast.BinaryExpr)
	if !ok || or.Op != lexer.TOKEN_OR {
		t.Fatalf("expected top-level `or`, got %T %v", q.Body, q.Body)
	}
	not, ok := or.Left.(*ast.UnaryExpr)
	if !ok || not.Op != lexer.TOKEN_NOT {
		t.Fatalf("expected guard negation, got %T", or.Left)
	}
	guard, ok := not.Operand.(*ast.BinaryExpr)
	if !ok || guard.Op != lexer.TOKEN_AND {
		t.Fatalf("expected `lo <= i and i < hi` guard, got %T", not.Operand)
	}
	// Upper bound hi must be `self.count` (indices -> 0 ..< self.count).
	hiCmp, ok := guard.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected `i < hi`, got %T", guard.Right)
	}
	count, ok := hiCmp.Right.(*ast.FieldExpr)
	if !ok || count.Field != "count" {
		t.Fatalf("expected hi == self.count, got %T %+v", hiCmp.Right, hiCmp.Right)
	}
}

// Explicit `lo ..< hi` range form parses to the same canonical shape.
func TestQuantifierExplicitRangeSugarDesugars(t *testing.T) {
	src := "law NotDouble(self: i64, n: i64) = forall k in 0 ..< n: self != k * 2\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if _, ok := lawPredicate(t, file).(*ast.QuantifierExpr); !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
}
