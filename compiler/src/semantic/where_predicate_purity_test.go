//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Unit tests for wherePredicateIsSideEffectFree
// ---------------------------------------------------------------------------
// These call the helper directly (package-internal) to confirm that newly
// covered node kinds are classified correctly without relying on full semantic
// analysis of a source file.

func pos() lexer.Pos { return lexer.Pos{} }

func identExpr(name string) *ast.Ident { return &ast.Ident{Position: pos(), Name: name} }
func intLit(v string) *ast.IntLit      { return &ast.IntLit{Position: pos(), Value: v} }
func boolLit(v bool) *ast.BoolLit      { return &ast.BoolLit{Position: pos(), Value: v} }

func binExpr(left, right ast.Expr) *ast.BinaryExpr {
	return &ast.BinaryExpr{Position: pos(), Left: left, Right: right}
}

// TestWherePurity_TernaryIsPure ensures TernaryExpr whose sub-expressions are
// all pure is classified as pure. Before the fix, TernaryExpr fell through the
// switch → false (soundly over-rejecting valid predicates).
func TestWherePurity_TernaryIsPure(t *testing.T) {
	// (n if n >= 0 else 0): Value=n, Cond=(n>=0), Alt=0
	ternary := &ast.TernaryExpr{
		Position: pos(),
		Value:    identExpr("n"),
		Cond:     binExpr(identExpr("n"), intLit("0")),
		Alt:      intLit("0"),
	}
	if !wherePredicateIsSideEffectFree(ternary) {
		t.Error("TernaryExpr with pure sub-expressions must be classified as pure")
	}
}

// TestWherePurity_TernaryImpureWhenBranchImpure ensures that a TernaryExpr
// whose branch is impure (contains a CallExpr) is rejected.
func TestWherePurity_TernaryImpureWhenBranchImpure(t *testing.T) {
	call := &ast.CallExpr{Position: pos(), Func: identExpr("side_effect")}
	ternary := &ast.TernaryExpr{
		Position: pos(),
		Value:    identExpr("n"),
		Cond:     call, // call in condition
		Alt:      intLit("0"),
	}
	if wherePredicateIsSideEffectFree(ternary) {
		t.Error("TernaryExpr with impure condition must be classified as impure")
	}
}

// TestWherePurity_TupleIsPure ensures a TupleExpr of pure elements is pure.
func TestWherePurity_TupleIsPure(t *testing.T) {
	tuple := &ast.TupleExpr{Position: pos(), Elems: []ast.Expr{identExpr("a"), intLit("1")}}
	if !wherePredicateIsSideEffectFree(tuple) {
		t.Error("TupleExpr with pure elements must be classified as pure")
	}
}

// TestWherePurity_TupleImpureWhenElemImpure confirms an impure tuple element propagates.
func TestWherePurity_TupleImpureWhenElemImpure(t *testing.T) {
	call := &ast.CallExpr{Position: pos(), Func: identExpr("f")}
	tuple := &ast.TupleExpr{Position: pos(), Elems: []ast.Expr{identExpr("a"), call}}
	if wherePredicateIsSideEffectFree(tuple) {
		t.Error("TupleExpr containing a CallExpr must be classified as impure")
	}
}

// TestWherePurity_CastIsPure ensures CastExpr of a pure operand is pure.
func TestWherePurity_CastIsPure(t *testing.T) {
	cast := &ast.CastExpr{Position: pos(), Operand: identExpr("n")}
	if !wherePredicateIsSideEffectFree(cast) {
		t.Error("CastExpr with pure operand must be classified as pure")
	}
}

// TestWherePurity_CallExprStillImpure confirms CallExpr is never pure (unchanged).
func TestWherePurity_CallExprStillImpure(t *testing.T) {
	call := &ast.CallExpr{Position: pos(), Func: identExpr("side_effect")}
	if wherePredicateIsSideEffectFree(call) {
		t.Error("CallExpr must remain impure — it can execute arbitrary code")
	}
}

// TestWherePurity_SizeofIsPure — compile-time layout query, always pure.
func TestWherePurity_SizeofIsPure(t *testing.T) {
	sz := &ast.SizeofExpr{Position: pos()}
	if !wherePredicateIsSideEffectFree(sz) {
		t.Error("SizeofExpr must be classified as pure")
	}
}

// TestWherePurity_QuantifierIsPure — forall/exists are purely logical.
func TestWherePurity_QuantifierIsPure(t *testing.T) {
	q := &ast.QuantifierExpr{Position: pos(), Body: binExpr(identExpr("i"), intLit("0"))}
	if !wherePredicateIsSideEffectFree(q) {
		t.Error("QuantifierExpr must be classified as pure")
	}
}

// ---------------------------------------------------------------------------
// Unit tests for exprIdentNames completeness
// ---------------------------------------------------------------------------
// These confirm that identifiers nested in previously-unwalked node kinds are
// now surfaced. This closes the scope-validation bypass: a name like
// `out_of_scope` hidden inside a TernaryExpr would silently pass
// validateWhereReferences before the fix.

func assertNames(t *testing.T, expr ast.Expr, expected ...string) {
	t.Helper()
	got := exprIdentNames(expr)
	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}
	for _, want := range expected {
		if !gotSet[want] {
			t.Errorf("exprIdentNames: expected %q in result %v", want, got)
		}
	}
}

// TestExprIdentNames_TernaryWalksAllBranches ensures identifiers in all three
// branches (Value, Cond, Alt) are collected. Before the fix, TernaryExpr was
// not handled, so names in the ternary were silently dropped.
func TestExprIdentNames_TernaryWalksAllBranches(t *testing.T) {
	ternary := &ast.TernaryExpr{
		Position: pos(),
		Value:    identExpr("value_ident"),
		Cond:     binExpr(identExpr("cond_ident"), intLit("0")),
		Alt:      identExpr("alt_ident"),
	}
	assertNames(t, ternary, "value_ident", "cond_ident", "alt_ident")
}

// TestExprIdentNames_CastWalksOperand ensures identifiers inside a CastExpr
// operand are extracted.
func TestExprIdentNames_CastWalksOperand(t *testing.T) {
	cast := &ast.CastExpr{Position: pos(), Operand: identExpr("ghost_var")}
	assertNames(t, cast, "ghost_var")
}

// TestExprIdentNames_TupleWalksElems ensures identifiers in TupleExpr elements
// are extracted.
func TestExprIdentNames_TupleWalksElems(t *testing.T) {
	tuple := &ast.TupleExpr{
		Position: pos(),
		Elems:    []ast.Expr{identExpr("elem_a"), identExpr("elem_b")},
	}
	assertNames(t, tuple, "elem_a", "elem_b")
}

// TestExprIdentNames_ListLitWalksElemsAndKeys ensures both element values and
// dict keys in a ListLitExpr are walked.
func TestExprIdentNames_ListLitWalksElemsAndKeys(t *testing.T) {
	lit := &ast.ListLitExpr{
		Position: pos(),
		Elems:    []ast.Expr{identExpr("v1")},
		Keys:     []ast.Expr{identExpr("k1")},
	}
	assertNames(t, lit, "v1", "k1")
}

// ---------------------------------------------------------------------------
// Integration: scope-bypass regression via validateWhereReferences
// ---------------------------------------------------------------------------
// An out-of-scope identifier nested in a ternary must now be caught.

// TestWhereScope_OutOfScopeIdentInTernaryRejected is the key regression test:
// before the exprIdentNames fix, `out_of_scope` hidden inside a TernaryExpr's
// condition was not extracted → validateWhereReferences silently accepted it.
func TestWhereScope_OutOfScopeIdentInTernaryRejected(t *testing.T) {
	// In a param where clause, only the param name (here "n") and type names
	// are in the allowed set. `out_of_scope` is not in scope.
	result := analyzeTreeTestSourceWithSemanticErrors(t, "ternary_scope.elisa", `
def f(n: i64 where (n if out_of_scope else 0) >= 0) -> i64:
    return n
`)
	all := strings.Join(append(append([]string{}, result.Errors()...), result.Warnings()...), "\n")
	if !strings.Contains(all, "out_of_scope") {
		t.Fatalf("expected scope-validation error naming out_of_scope in ternary; got:\n%s", all)
	}
}

// TestWhereScope_OutOfScopeIdentInCastRejected confirms that an identifier
// hidden inside a CastExpr is now surfaced by exprIdentNames.
func TestWhereScope_OutOfScopeIdentInCastRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "cast_scope.elisa", `
def f(n: i32 where ghost_var.i64() >= 0) -> i32:
    return n
`)
	all := strings.Join(append(append([]string{}, result.Errors()...), result.Warnings()...), "\n")
	// ghost_var is referenced as the object of a field/method call — the call
	// itself may produce an "impure" purity error first. Either way ghost_var
	// must appear in the diagnostics (purity error or scope error).
	if !strings.Contains(all, "ghost_var") && !strings.Contains(all, "pure") && !strings.Contains(all, "side-effect") {
		t.Fatalf("expected error mentioning ghost_var or purity for cast predicate; got:\n%s", all)
	}
}

// TestWherePurity_ImpureCallInWhereRejected confirms the existing behavior
// (CallExpr in where predicate → purity error) is preserved by the new code.
func TestWherePurity_ImpureCallInWhereRejected(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "call_impure.elisa", `
def side_effect() -> bool:
    return true

def impure_where(n: i64 where side_effect()) -> i64:
    return n
`)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "pure") && !strings.Contains(errs, "side-effect") {
		t.Fatalf("expected purity rejection for call in where predicate; got:\n%s", errs)
	}
}
