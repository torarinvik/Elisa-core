package parser

import (
	"strings"
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

// Inclusive `lo ..= hi` desugars to the half-open form with the upper bound `hi + 1`, so the guard
// `k < hi+1` (i.e. `k <= hi` for integers) covers the inclusive endpoint. The rest of the guarded-range
// machinery is reused verbatim.
func TestQuantifierInclusiveRangeSugarDesugars(t *testing.T) {
	src := "law InRange(self: array[i64, 8], n: i64) = forall k in 0 ..= n: self[k] >= 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	// Body is `(not guard) or P`; guard is `lo <= k and k < hi`; for `..=` the hi must be `n + 1`.
	or, ok := q.Body.(*ast.BinaryExpr)
	if !ok || or.Op != lexer.TOKEN_OR {
		t.Fatalf("expected top-level `or`, got %T", q.Body)
	}
	not, ok := or.Left.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected guard negation, got %T", or.Left)
	}
	guard, ok := not.Operand.(*ast.BinaryExpr)
	if !ok || guard.Op != lexer.TOKEN_AND {
		t.Fatalf("expected `lo <= k and k < hi` guard, got %T", not.Operand)
	}
	hiCmp, ok := guard.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected `k < hi`, got %T", guard.Right)
	}
	hi, ok := hiCmp.Right.(*ast.BinaryExpr)
	if !ok || hi.Op != lexer.TOKEN_PLUS {
		t.Fatalf("inclusive `..= n` upper bound must desugar to `n + 1`, got %T %+v", hiCmp.Right, hiCmp.Right)
	}
}

// Bare `lo .. hi` as a quantifier range is REJECTED: its endpoint is ambiguous (inclusive in Elisa,
// exclusive in Rust/Python/Swift), so an explicit `..<` / `..=` is required. The diagnostic names both.
func TestQuantifierBareRangeIsRejected(t *testing.T) {
	src := "law InRange(self: array[i64, 8], n: i64) = forall k in 0 .. n: self[k] >= 0\n"
	_, errs := parseSourceFile(t, src)
	joined := ""
	for _, e := range errs {
		joined += e + "\n"
	}
	if !strings.Contains(joined, "ambiguous bare `..` range") || !strings.Contains(joined, "..<") || !strings.Contains(joined, "..=") {
		t.Fatalf("bare `.. n` in a quantifier must error pointing to `..<`/`..=`, got: %v", errs)
	}
}

func TestExistsExplicitRangeSugarDesugarsToGuardedConjunction(t *testing.T) {
	src := "law HasZero(self: array[i64, 8]) = exists i in 0 ..< self.count: self[i] == 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	if !q.Exists || len(q.Vars) != 1 || q.Vars[0] != "i" {
		t.Fatalf("expected single exists binder i, got %+v", q)
	}
	and, ok := q.Body.(*ast.BinaryExpr)
	if !ok || and.Op != lexer.TOKEN_AND {
		t.Fatalf("expected top-level guarded `and`, got %T %v", q.Body, q.Body)
	}
	guard, ok := and.Left.(*ast.BinaryExpr)
	if !ok || guard.Op != lexer.TOKEN_AND {
		t.Fatalf("expected `lo <= i and i < hi` guard, got %T", and.Left)
	}
	hiCmp, ok := guard.Right.(*ast.BinaryExpr)
	if !ok || hiCmp.Op != lexer.TOKEN_LT {
		t.Fatalf("expected upper-bound comparison, got %T %+v", guard.Right, guard.Right)
	}
	count, ok := hiCmp.Right.(*ast.FieldExpr)
	if !ok || count.Field != "count" {
		t.Fatalf("expected hi == self.count, got %T %+v", hiCmp.Right, hiCmp.Right)
	}
}

func TestExistsIndicesSugarDesugarsToGuardedConjunction(t *testing.T) {
	src := "law HasZero(self: array[i64, 8]) = exists i in self.indices: self[i] == 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	and, ok := q.Body.(*ast.BinaryExpr)
	if !q.Exists || !ok || and.Op != lexer.TOKEN_AND {
		t.Fatalf("expected exists with guarded conjunction, got q=%+v body=%T", q, q.Body)
	}
}

func TestQuantifierValueBindingDesugarsToIndexSelect(t *testing.T) {
	src := "law AllPositive(self: darray[i64]) = forall x in self: x > 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	if q.Exists || len(q.Vars) != 1 || q.Vars[0] != "x" {
		t.Fatalf("expected value forall binder, got %+v", q)
	}
	if _, ok := q.In.(*ast.Ident); !ok {
		t.Fatalf("expected collection source recorded, got %T %+v", q.In, q.In)
	}
	cmp, ok := q.Body.(*ast.BinaryExpr)
	if !ok || cmp.Op != lexer.TOKEN_GT {
		t.Fatalf("expected predicate comparison, got %T %+v", q.Body, q.Body)
	}
	id, ok := cmp.Left.(*ast.Ident)
	if !ok || id.Name != "x" {
		t.Fatalf("expected x preserved for typed lowering, got %T %+v", cmp.Left, cmp.Left)
	}
}

func TestQuantifierDictPairBindingParsesSpecOnly(t *testing.T) {
	src := "law DictKeysNonZero(self: dict[i64, i64]) = forall (k, v) in self: k != 0 and v != 0\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	q, ok := lawPredicate(t, file).(*ast.QuantifierExpr)
	if !ok {
		t.Fatalf("expected QuantifierExpr, got %T", lawPredicate(t, file))
	}
	if q.Exists || len(q.Vars) != 2 || q.Vars[0] != "k" || q.Vars[1] != "v" {
		t.Fatalf("expected pair binders preserved as spec-only quantifier, got %+v", q)
	}
	if _, ok := q.In.(*ast.Ident); !ok {
		t.Fatalf("expected dict collection source recorded, got %T %+v", q.In, q.In)
	}
}
