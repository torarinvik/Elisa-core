//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// crossmodule_where_matrix_test.go exercises cross-module / SpecSignature-based `where`
// precondition discharge in a matrix of scenarios: multiple predicates, various operators,
// boundary values, and dedup behavior with AST. These complement where_call_discharge_test.go
// and crossmodule_where_soundness_test.go by covering cases not yet exercised.

// specSigWithMultiplePredicates builds a SpecSignature with predicates on multiple params.
// Each param is bound to a predicate of form `p{index} OP rhs`.
func specSigWithMultiplePredicates(declName string, predicates []struct {
	paramPos int
	op       lexer.TokenKind
	rhs      string
}) *SpecSignature {
	// Determine param count from the highest predicate position.
	maxPos := 0
	for _, p := range predicates {
		if p.paramPos > maxPos {
			maxPos = p.paramPos
		}
	}
	nParams := maxPos + 1

	// Build binders for all params.
	binders := make([]SpecBinder, nParams)
	for i := 0; i < nParams; i++ {
		binders[i] = NewParamSpecBinder(i, paramName(i), nil, nil, lexer.Pos{})
	}

	sig := NewSpecSignature(lexer.Pos{}, declName, binders, nil)

	// Attach one predicate per specified position.
	for _, p := range predicates {
		ref := binders[p.paramPos].Ref()
		pred := &ast.BinaryExpr{
			Op:    p.op,
			Left:  &ast.Ident{Name: paramName(p.paramPos)},
			Right: &ast.IntLit{Value: p.rhs},
		}
		sig.ParamPredicates = append(sig.ParamPredicates, NewRefinementPredicate(
			RefinementPredicateType, ref, "", nil, nil, pred, lexer.Pos{},
		))
	}

	return sig
}

// TestCrossModule_MultiplePredicates_AllSatisfied verifies that a call with all args
// satisfying their respective predicates produces no error.
func TestCrossModule_MultiplePredicates_AllSatisfied(t *testing.T) {
	sig := specSigWithMultiplePredicates("multi_check", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},   // p0 > 0
		{1, lexer.TOKEN_LT, "100"}, // p1 < 100
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	args := []ast.Expr{
		&ast.IntLit{Value: "5"},  // satisfies p0 > 0
		&ast.IntLit{Value: "50"}, // satisfies p1 < 100
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "multi_check"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "multi_check", sig, nil, args)

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error when all predicates satisfied, got: %s", d.Message)
		}
	}
}

// TestCrossModule_MultiplePredicates_FirstViolated verifies that if the first predicate
// is violated, an error is reported even if the second is satisfied.
func TestCrossModule_MultiplePredicates_FirstViolated(t *testing.T) {
	sig := specSigWithMultiplePredicates("multi_check", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},   // p0 > 0
		{1, lexer.TOKEN_LT, "100"}, // p1 < 100
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	args := []ast.Expr{
		&ast.IntLit{Value: "-5"}, // violates p0 > 0
		&ast.IntLit{Value: "50"}, // satisfies p1 < 100
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "multi_check"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "multi_check", sig, nil, args)

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of multi_check") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for first predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_MultiplePredicates_SecondViolated verifies that if the second predicate
// is violated, an error is reported.
func TestCrossModule_MultiplePredicates_SecondViolated(t *testing.T) {
	sig := specSigWithMultiplePredicates("multi_check", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},   // p0 > 0
		{1, lexer.TOKEN_LT, "100"}, // p1 < 100
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	args := []ast.Expr{
		&ast.IntLit{Value: "5"},   // satisfies p0 > 0
		&ast.IntLit{Value: "150"}, // violates p1 < 100
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "multi_check"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "multi_check", sig, nil, args)

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of multi_check") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for second predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_LT_Operator_Satisfied verifies TOKEN_LT operator with a satisfying literal.
func TestCrossModule_LT_Operator_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("bound_above", 1, 0, lexer.TOKEN_LT, "100")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "50"} // satisfies p0 < 100
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_above"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_above", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied < predicate, got: %s", d.Message)
		}
	}
}

// TestCrossModule_LT_Operator_Violated verifies TOKEN_LT operator with a violating literal.
func TestCrossModule_LT_Operator_Violated(t *testing.T) {
	sig := specSigWithPredAt("bound_above", 1, 0, lexer.TOKEN_LT, "100")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "150"} // violates p0 < 100
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_above"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_above", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of bound_above") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for < predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_LTEQ_Operator_Satisfied verifies TOKEN_LTEQ operator with a satisfying literal (boundary).
func TestCrossModule_LTEQ_Operator_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("bound_max_inclusive", 1, 0, lexer.TOKEN_LTEQ, "100")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "100"} // satisfies p0 <= 100 (boundary)
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_max_inclusive"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_max_inclusive", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied <= predicate at boundary, got: %s", d.Message)
		}
	}
}

// TestCrossModule_LTEQ_Operator_Violated verifies TOKEN_LTEQ operator with a violating literal.
func TestCrossModule_LTEQ_Operator_Violated(t *testing.T) {
	sig := specSigWithPredAt("bound_max_inclusive", 1, 0, lexer.TOKEN_LTEQ, "100")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "101"} // violates p0 <= 100
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_max_inclusive"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_max_inclusive", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of bound_max_inclusive") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for <= predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_GTEQ_Operator_Satisfied verifies TOKEN_GTEQ operator with a satisfying literal (boundary).
func TestCrossModule_GTEQ_Operator_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("bound_min_inclusive", 1, 0, lexer.TOKEN_GTEQ, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "0"} // satisfies p0 >= 0 (boundary)
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_min_inclusive"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_min_inclusive", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied >= predicate at boundary, got: %s", d.Message)
		}
	}
}

// TestCrossModule_GTEQ_Operator_Violated verifies TOKEN_GTEQ operator with a violating literal.
func TestCrossModule_GTEQ_Operator_Violated(t *testing.T) {
	sig := specSigWithPredAt("bound_min_inclusive", 1, 0, lexer.TOKEN_GTEQ, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "-1"} // violates p0 >= 0
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "bound_min_inclusive"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "bound_min_inclusive", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of bound_min_inclusive") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for >= predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_EQEQ_Operator_Satisfied verifies TOKEN_EQEQ operator with matching literal.
func TestCrossModule_EQEQ_Operator_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("must_equal", 1, 0, lexer.TOKEN_EQEQ, "42")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "42"} // satisfies p0 == 42
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "must_equal"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "must_equal", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied == predicate, got: %s", d.Message)
		}
	}
}

// TestCrossModule_EQEQ_Operator_Violated verifies TOKEN_EQEQ operator with non-matching literal.
func TestCrossModule_EQEQ_Operator_Violated(t *testing.T) {
	sig := specSigWithPredAt("must_equal", 1, 0, lexer.TOKEN_EQEQ, "42")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "99"} // violates p0 == 42
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "must_equal"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "must_equal", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of must_equal") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for == predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_BANGEQ_Operator_Satisfied verifies TOKEN_BANGEQ operator with non-matching literal.
func TestCrossModule_BANGEQ_Operator_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("not_equal_zero", 1, 0, lexer.TOKEN_BANGEQ, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "5"} // satisfies p0 != 0
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "not_equal_zero"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "not_equal_zero", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error for satisfied != predicate, got: %s", d.Message)
		}
	}
}

// TestCrossModule_BANGEQ_Operator_Violated verifies TOKEN_BANGEQ operator with matching literal.
func TestCrossModule_BANGEQ_Operator_Violated(t *testing.T) {
	sig := specSigWithPredAt("not_equal_zero", 1, 0, lexer.TOKEN_BANGEQ, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "0"} // violates p0 != 0
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "not_equal_zero"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "not_equal_zero", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of not_equal_zero") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for != predicate, got: %v", a.diagnostics)
	}
}

// TestCrossModule_ZeroBoundary_GT_Violated verifies zero is NOT > 0.
func TestCrossModule_ZeroBoundary_GT_Violated(t *testing.T) {
	sig := specSigWithPredAt("pos_only", 1, 0, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "0"} // zero is NOT > 0
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "pos_only"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "pos_only", sig, nil, []ast.Expr{arg})

	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of pos_only") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error: zero is not > 0, got: %v", a.diagnostics)
	}
}

// TestCrossModule_ZeroBoundary_GTEQ_Satisfied verifies zero IS >= 0.
func TestCrossModule_ZeroBoundary_GTEQ_Satisfied(t *testing.T) {
	sig := specSigWithPredAt("non_neg", 1, 0, lexer.TOKEN_GTEQ, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "0"} // zero IS >= 0
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "non_neg"},
		Args: []ast.Expr{arg},
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "non_neg", sig, nil, []ast.Expr{arg})

	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("expected no error: zero is >= 0, got: %s", d.Message)
		}
	}
}

// TestCrossModule_OpaqueThenMultiple verifies that an opaque argument with multiple predicates
// degrades all to runtime obligations (not falsely proven, not refuted).
func TestCrossModule_OpaqueThenMultiple(t *testing.T) {
	sig := specSigWithMultiplePredicates("opaque_multi", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},
		{1, lexer.TOKEN_LT, "100"},
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	args := []ast.Expr{
		&ast.Ident{Name: "unknown_a"}, // opaque
		&ast.Ident{Name: "unknown_b"}, // opaque
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "opaque_multi"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "opaque_multi", sig, nil, args)

	if hasAnyError(a) {
		t.Fatalf("unprovable predicates must not be hard errors, got: %v", a.diagnostics)
	}

	// Verify both runtime obligations are recorded.
	runtimeCount := 0
	for _, p := range a.proofReport {
		if p.Outcome == ProofRuntime && strings.Contains(p.Subject, "where precondition of opaque_multi") {
			runtimeCount++
		}
	}
	if runtimeCount < 2 {
		t.Errorf("expected at least 2 runtime obligations for opaque multi-predicate call, got %d", runtimeCount)
	}
}

// TestCrossModule_Dedup_MultiplePredicates_PartialAST_Mix verifies dedup behavior when some predicates
// are covered by AST where-types and others are not. The AST path should handle its predicates, and
// the SpecSignature path should only discharge those not covered.
func TestCrossModule_Dedup_MultiplePredicates_PartialAST_Mix(t *testing.T) {
	sig := specSigWithMultiplePredicates("partly_ast_covered", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},   // p0 > 0
		{1, lexer.TOKEN_LT, "100"}, // p1 < 100
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// AST covers p0 with a where-type, but p1 is not AST-covered.
	whereTypeP0 := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: &ast.BinaryExpr{Op: lexer.TOKEN_GT, Left: &ast.Ident{Name: "p0"}, Right: &ast.IntLit{Value: "0"}},
	}
	schemeParams := []ast.ParamDecl{
		{Name: "p0", Type: whereTypeP0},
		{Name: "p1", Type: &ast.BuiltinTypeExpr{Name: "i64"}}, // no where-type for p1
	}

	args := []ast.Expr{
		&ast.IntLit{Value: "5"},   // satisfies p0 > 0 (AST path)
		&ast.IntLit{Value: "150"}, // violates p1 < 100 (SpecSignature path should catch this)
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "partly_ast_covered"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "partly_ast_covered", sig, schemeParams, args)

	// Expect error only for p1 (the one not covered by AST).
	found := false
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, "where precondition of partly_ast_covered") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation error for SpecSignature-uncovered predicate (p1 < 100), got: %v", a.diagnostics)
	}
}

// TestCrossModule_Dedup_AllAST_Covered verifies that when ALL predicates are covered by AST where-types,
// the SpecSignature path skips all of them (no double-discharge).
func TestCrossModule_Dedup_AllAST_Covered(t *testing.T) {
	sig := specSigWithMultiplePredicates("all_ast", []struct {
		paramPos int
		op       lexer.TokenKind
		rhs      string
	}{
		{0, lexer.TOKEN_GT, "0"},
		{1, lexer.TOKEN_LT, "100"},
	})

	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// Both params have AST where-types.
	whereTypeP0 := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: &ast.BinaryExpr{Op: lexer.TOKEN_GT, Left: &ast.Ident{Name: "p0"}, Right: &ast.IntLit{Value: "0"}},
	}
	whereTypeP1 := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: &ast.BinaryExpr{Op: lexer.TOKEN_LT, Left: &ast.Ident{Name: "p1"}, Right: &ast.IntLit{Value: "100"}},
	}
	schemeParams := []ast.ParamDecl{
		{Name: "p0", Type: whereTypeP0},
		{Name: "p1", Type: whereTypeP1},
	}

	args := []ast.Expr{
		&ast.IntLit{Value: "-5"},  // violates p0 > 0 (AST path handles it)
		&ast.IntLit{Value: "150"}, // violates p1 < 100 (AST path handles it)
	}
	call := &ast.CallExpr{
		Func: &ast.Ident{Name: "all_ast"},
		Args: args,
	}

	a.checkCalleeSpecSignatureWherePredicates(call, "all_ast", sig, schemeParams, args)

	// SpecSignature path should not report any errors; AST path owns them.
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			t.Errorf("SpecSignature path should defer to AST path when all predicates are covered, got error: %s", d.Message)
		}
	}
}
