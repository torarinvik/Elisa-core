//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// These tests harden checkCalleeSpecSignatureWherePredicates (the cross-module / imported
// `where`-precondition discharge path) for SOUNDNESS. Each test is written so that a naive
// regression of the implementation would FAIL it:
//
//   - sequential (counter-based) substitution instead of binder-Position keying
//   - skipping discharge whenever an AST `WhereRefinementTypeExpr` is present (even nil-predicate)
//   - accepting an unprovable cross-module precondition silently (no runtime obligation)
//   - mis-handling arg-count mismatch (varargs/defaults) → wrong arg under a binder

func hasDiagErrorContaining(a *Analyzer, frag string) bool {
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError && strings.Contains(d.Message, frag) {
			return true
		}
	}
	return false
}

func hasAnyError(a *Analyzer) bool {
	for _, d := range a.diagnostics {
		if d.Severity == DiagnosticSeverityError {
			return true
		}
	}
	return false
}

// specSigWithPredAt builds a SpecSignature with `nParams` params whose names are p0..p(n-1),
// carrying a single `where p{predPos} OP rhs` predicate at position predPos.
func specSigWithPredAt(declName string, nParams, predPos int, op lexer.TokenKind, rhs string) *SpecSignature {
	binders := make([]SpecBinder, 0, nParams)
	for i := 0; i < nParams; i++ {
		binders = append(binders, NewParamSpecBinder(i, paramName(i), nil, nil, lexer.Pos{}))
	}
	sig := NewSpecSignature(lexer.Pos{}, declName, binders, nil)
	pred := &ast.BinaryExpr{Op: op, Left: &ast.Ident{Name: paramName(predPos)}, Right: &ast.IntLit{Value: rhs}}
	ref := binders[predPos].Ref()
	sig.ParamPredicates = []RefinementPredicate{
		NewRefinementPredicate(RefinementPredicateType, ref, "", nil, nil, pred, lexer.Pos{}),
	}
	return sig
}

func paramName(i int) string {
	return "p" + string(rune('0'+i))
}

// TestCrossModule_ImportedCallee_NoAST_RuntimeFallbackOnUnprovable verifies that a cross-module
// precondition that CANNOT be proven (argument is an unknown/opaque caller value, not a literal)
// is NOT silently accepted: it records a runtime proof obligation rather than nothing.
//
// A naive implementation that simply `return`ed on "unknown" would leave NO proof record; this
// test asserts the runtime obligation is recorded (the same-module AST path behaviour).
func TestCrossModule_ImportedCallee_NoAST_RuntimeFallbackOnUnprovable(t *testing.T) {
	sig := specSigWithPredAt("imported_fn", 1, 0, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// Opaque argument: a bare ident the prover cannot const-fold. Must degrade to a runtime
	// obligation, NOT silent acceptance and NOT a false "proven".
	arg := &ast.Ident{Name: "opaque_value"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "imported_fn"}, Args: []ast.Expr{arg}}

	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, []ast.Expr{arg})

	if hasAnyError(a) {
		t.Fatalf("unprovable (not refuted) precondition must not be a hard error, got: %v", a.diagnostics)
	}
	foundRuntime := false
	for _, p := range a.proofReport {
		if p.Outcome == ProofRuntime && strings.Contains(p.Subject, "where precondition of imported_fn") {
			foundRuntime = true
		}
		if p.Outcome == ProofProvenLinear || p.Outcome == ProofProvenSMT {
			t.Fatalf("opaque arg must NOT be proven, got proof outcome %v", p.Outcome)
		}
	}
	if !foundRuntime {
		t.Fatalf("expected a runtime proof obligation for unprovable cross-module precondition, proofs=%v", a.proofReport)
	}
}

// TestCrossModule_ImportedCallee_NoAST_Violated verifies a provably-violated imported precondition
// errors even with NO AST node present (schemeParams=nil).
func TestCrossModule_ImportedCallee_NoAST_Violated(t *testing.T) {
	sig := specSigWithPredAt("imported_fn", 1, 0, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	arg := &ast.IntLit{Value: "-9"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "imported_fn"}, Args: []ast.Expr{arg}}

	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, []ast.Expr{arg})

	if !hasDiagErrorContaining(a, "where precondition of imported_fn") {
		t.Fatalf("expected violated-precondition error with no AST, got: %v", a.diagnostics)
	}
}

// TestCrossModule_MixedParams_BinderPositionSubstitution verifies that the predicate on a NON-FIRST
// param substitutes the correct argument. A naive sequential substitution would bind the wrong arg.
//
// Predicate is `p2 > 0`. Args: [p0=10, p1=10, p2=-1]. Only the position-2 arg (-1) violates, so a
// correct (position-keyed) implementation REFUTES. A buggy sequential implementation that mapped
// the predicate's binder to args[0] (=10) would FALSELY prove it satisfied → no error → test fails.
func TestCrossModule_MixedParams_BinderPositionSubstitution(t *testing.T) {
	sig := specSigWithPredAt("imported_fn", 3, 2, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	a0 := &ast.IntLit{Value: "10"}
	a1 := &ast.IntLit{Value: "10"}
	a2 := &ast.IntLit{Value: "-1"}
	args := []ast.Expr{a0, a1, a2}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "imported_fn"}, Args: args}

	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, args)

	if !hasDiagErrorContaining(a, "where precondition of imported_fn") {
		t.Fatalf("position-2 predicate over violating arg (-1) must error; wrong substitution would mask it. diagnostics=%v", a.diagnostics)
	}
}

// TestCrossModule_ArgCountMismatch_NoFalseProof guards the varargs/defaults case: the predicate is on
// a param position for which NO argument was supplied. The binder must be left UNSUBSTITUTED and the
// obligation must degrade to a runtime fallback — never a false "proven" and never an out-of-range panic.
func TestCrossModule_ArgCountMismatch_NoFalseProof(t *testing.T) {
	// Predicate on position 2, but only 1 argument supplied.
	sig := specSigWithPredAt("imported_fn", 3, 2, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	only := &ast.IntLit{Value: "5"}
	args := []ast.Expr{only}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "imported_fn"}, Args: args}

	// Must not panic on the missing index.
	a.checkCalleeSpecSignatureWherePredicates(call, "imported_fn", sig, nil, args)

	if hasAnyError(a) {
		t.Fatalf("missing arg must not produce a hard error (unsubstituted binder is unknown, not refuted): %v", a.diagnostics)
	}
	for _, p := range a.proofReport {
		if p.Outcome == ProofProvenLinear || p.Outcome == ProofProvenSMT {
			t.Fatalf("missing arg under binder must NOT yield a proof; got %v", p.Outcome)
		}
	}
}

// TestCrossModule_Dedup_NilPredicateWhereType_StillDischarges is the core hardened-hole regression.
// The AST param at the predicate position carries a WhereRefinementTypeExpr whose Predicate is NIL.
// The AST discharge path (checkCalleeParamWhereRefinements) SKIPS nil-predicate where-types, so if
// the SpecSignature path also skipped on a bare `isWhere`, the obligation would be DROPPED entirely.
// With the fix, the SpecSignature path must still fire and refute the violating arg.
func TestCrossModule_Dedup_NilPredicateWhereType_StillDischarges(t *testing.T) {
	sig := specSigWithPredAt("partly_imported_fn", 1, 0, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	// AST where-type with a NIL predicate at position 0 — the AST path will not cover it.
	nilPredWhere := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: nil,
	}
	schemeParams := []ast.ParamDecl{{Name: "p0", Type: nilPredWhere}}

	arg := &ast.IntLit{Value: "-7"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "partly_imported_fn"}, Args: []ast.Expr{arg}}

	a.checkCalleeSpecSignatureWherePredicates(call, "partly_imported_fn", sig, schemeParams, []ast.Expr{arg})

	if !hasDiagErrorContaining(a, "where precondition of partly_imported_fn") {
		t.Fatalf("nil-predicate AST where-type must NOT suppress the SpecSignature obligation; expected refutation, diagnostics=%v", a.diagnostics)
	}
}

// TestCrossModule_Dedup_RealPredicateWhereType_Skips verifies the dedup still works for the intended
// case: a genuine AST where-type (non-nil predicate) at the position causes the SpecSignature path to
// defer (no double diagnostic), since checkCalleeParamWhereRefinements owns that obligation.
func TestCrossModule_Dedup_RealPredicateWhereType_Skips(t *testing.T) {
	sig := specSigWithPredAt("local_fn", 1, 0, lexer.TOKEN_GT, "0")
	scope := NewScope(nil)
	a := newAnalyzerWithScope(scope)

	realWhere := &ast.WhereRefinementTypeExpr{
		Base:      &ast.BuiltinTypeExpr{Name: "i64"},
		Predicate: &ast.BinaryExpr{Op: lexer.TOKEN_GT, Left: &ast.Ident{Name: "p0"}, Right: &ast.IntLit{Value: "0"}},
	}
	schemeParams := []ast.ParamDecl{{Name: "p0", Type: realWhere}}

	arg := &ast.IntLit{Value: "-7"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "local_fn"}, Args: []ast.Expr{arg}}

	a.checkCalleeSpecSignatureWherePredicates(call, "local_fn", sig, schemeParams, []ast.Expr{arg})

	if hasAnyError(a) {
		t.Fatalf("SpecSignature path must defer to AST path when a real where-type covers the position; got: %v", a.diagnostics)
	}
}
