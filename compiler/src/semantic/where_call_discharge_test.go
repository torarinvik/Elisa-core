//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// makeWherePred constructs an AST `name > rhs` BinaryExpr using TOKEN_GT.
func makeWherePred(name string, rhs string) ast.Expr {
	return &ast.BinaryExpr{
		Op:    lexer.TOKEN_GT,
		Left:  &ast.Ident{Name: name},
		Right: &ast.IntLit{Value: rhs},
	}
}

// syntheticWhereSpecSignature builds a SpecSignature with one param "n" carrying a
// `where n > 0` predicate, mimicking what an imported/cross-module FuncType would carry.
func syntheticWhereSpecSignature(declName string) (*SpecSignature, *FuncType) {
	pred := makeWherePred("n", "0")
	binder := NewParamSpecBinder(0, "n", nil, nil, lexer.Pos{})
	sig := NewSpecSignature(lexer.Pos{}, declName, []SpecBinder{binder}, nil)
	ref := binder.Ref()
	sig.ParamPredicates = []RefinementPredicate{
		NewRefinementPredicate(RefinementPredicateType, ref, "", nil, nil, pred, lexer.Pos{}),
	}
	ft := &FuncType{Name: declName, SpecSignature: sig}
	return sig, ft
}

// newAnalyzerWithScope constructs a minimal Analyzer for unit tests.
func newAnalyzerWithScope(scope *Scope) *Analyzer {
	return &Analyzer{
		currentScope: scope,
		exprTypes:    map[ast.Expr]Type{},
	}
}

// TestWhereSpecSignatureDischarge_Satisfied verifies that a call with a provably-satisfying
// argument does not produce an error when the predicate lives in SpecSignature only.
func TestWhereSpecSignatureDischarge_Satisfied(t *testing.T) {
	sig, _ := syntheticWhereSpecSignature("imported_fn")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// Argument is the literal "5", which is > 0 so the where predicate is satisfied.
	arg := &ast.IntLit{Value: "5"}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "imported_fn"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied where precondition, got: %s", d.Message)
		}
	}
}

// TestWhereSpecSignatureDischarge_Violated verifies that a call with a provably-violated
// argument produces a where-precondition error from the SpecSignature path.
func TestWhereSpecSignatureDischarge_Violated(t *testing.T) {
	sig, _ := syntheticWhereSpecSignature("imported_fn")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// Argument is "-5", which is NOT > 0 so the where predicate is violated.
	arg := &ast.IntLit{Value: "-5"}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "imported_fn"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of imported_fn") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'where precondition of imported_fn is violated' error, got diagnostics: %v", a.diagnostics)
	}
}

// TestWhereSpecSignatureDischarge_DeduplicationSkipsASTPath verifies that when scheme.Params
// already carries a WhereRefinementTypeExpr at the param's position, the SpecSignature path
// skips that predicate (AST path handles it, no double-reporting).
func TestWhereSpecSignatureDischarge_DeduplicationSkipsASTPath(t *testing.T) {
	sig, _ := syntheticWhereSpecSignature("local_fn")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// Simulate AST path: scheme param 0 has a WhereRefinementTypeExpr.
	whereType := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: makeWherePred("n", "0"),
	}
	schemeParams := []ast.ParamDecl{{Name: "n", Type: whereType}}

	// Even though arg "-5" would violate, the SpecSignature path must NOT fire since
	// the AST path covers this obligation.
	arg := &ast.IntLit{Value: "-5"}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "local_fn"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "local_fn", sig, schemeParams, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("SpecSignature path should skip when AST path covers obligation, got error: %s", d.Message)
		}
	}
}

// TestWhereSpecSignatureDischarge_EndToEnd_Satisfied verifies the full analysis pipeline:
// a same-module function with a `where` refinement param accepts a satisfying literal argument.
func TestWhereSpecSignatureDischarge_EndToEnd_Satisfied(t *testing.T) {
	analyzeTreeTestSource(t, "where_spec_sig_satisfied.elisa", `
def check(n: i64 where n > 0) -> i64:
    return n

def caller() -> i64:
    return check(3)
`)
}

// TestWhereSpecSignatureDischarge_EndToEnd_Violated verifies the full analysis pipeline:
// a same-module function with a `where` refinement param rejects a provably-violated literal.
func TestWhereSpecSignatureDischarge_EndToEnd_Violated(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "where_spec_sig_violated.elisa", `
def check(n: i64 where n > 0) -> i64:
    return n

def caller() -> i64:
    return check(-1)
`)
	errs := result.Errors()
	found := false
	for _, e := range errs {
		if strings.Contains(e, "where precondition") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected where precondition error for violated call, got errors: %v", errs)
	}
}

// TestWhereSpecSignatureDischarge_SpecSignatureOnlyImportedCallee simulates a cross-module
// call where only the FuncType+SpecSignature is available (no AST FuncDecl). The symbol is
// registered WITHOUT a Node so resolveDirectCallFuncDecl returns false, exercising the
// pure SpecSignature discharge path through dischargeCallRequires.
func TestWhereSpecSignatureDischarge_SpecSignatureOnlyImportedCallee(t *testing.T) {
	// Build a synthetic SpecSignature for an "imported" function.
	pred := makeWherePred("x", "0")
	binder := NewParamSpecBinder(0, "x", nil, nil, lexer.Pos{})
	sig := NewSpecSignature(lexer.Pos{}, "imported_clamp", []SpecBinder{binder}, nil)
	ref := binder.Ref()
	sig.ParamPredicates = []RefinementPredicate{
		NewRefinementPredicate(RefinementPredicateType, ref, "", nil, nil, pred, lexer.Pos{}),
	}
	ft := &FuncType{Name: "imported_clamp", SpecSignature: sig}

	// Register symbol with NO Node (no AST), so only SpecSignature path fires.
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "imported_clamp", Kind: SymbolFunc, Type: ft})

	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "-3"}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "imported_clamp"},
		Args: []ast.Expr{arg},
	}
	// Seed the call expression type so callFuncType can find it via exprTypes lookup.
	a.exprTypes[call.Func] = ft

	a.dischargeCallRequires(call, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of imported_clamp") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected where precondition error from SpecSignature-only path, got diagnostics: %v", a.diagnostics)
	}
}
