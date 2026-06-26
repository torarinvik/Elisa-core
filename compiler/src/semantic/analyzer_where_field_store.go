package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// dischargeFieldStoreWhere re-checks a struct field's where-predicate at a field-store site
// (recv.fieldName <- value). It mirrors the construction-site check in
// dischargeStructFieldWhereRefinement but operates on a single field-store assignment rather than
// a full struct literal.
//
// The function is a no-op when:
//   - the receiver's type is not a struct (or cannot be resolved to one)
//   - the field has no where-predicate
//
// When the predicate is present:
//   - Proved    → records ProofProvenLinear (or ProofProvenSMT via the shared proof ladder)
//   - Refuted   → hard error ("where refinement on field … is violated")
//   - Unknown   → runtime lint + ProofRuntime, mirroring the construction-site behaviour
//
// Earlier-sibling values are not available at a field-store site (we are storing into a single
// field of an already-live struct, not constructing one from scratch), so cross-field predicates
// that reference earlier fields will fall through to a runtime check — never a false positive.
func (a *Analyzer) dischargeFieldStoreWhere(recv ast.Expr, fieldName string, value ast.Expr, pos lexer.Pos) {
	if recv == nil || value == nil {
		return
	}

	// Resolve the receiver's type.  analyzeExpr is called with ghostReadAllowed bumped so that
	// the re-analysis of the receiver (typically a plain identifier) does not trip uninitialized-
	// read diagnostics already emitted by the outer AssignStmt analysis.
	a.ghostReadAllowed++
	recvType := a.analyzeExpr(recv)
	a.ghostReadAllowed--

	// Strip any aggregate-state wrapper to reach the underlying struct.
	recvType = StripAggregateStateType(recvType)

	// Walk through GenericInstanceType to reach the underlying StructType.
	var structType *StructType
	switch tt := recvType.(type) {
	case *StructType:
		structType = tt
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			structType = base
		}
	}
	if structType == nil || structType.Decl == nil {
		return
	}

	// Find the FieldDecl for this field in the struct's AST declaration.
	var fieldDecl *ast.FieldDecl
	for i := range structType.Decl.Fields {
		if structType.Decl.Fields[i].Name == fieldName {
			fieldDecl = &structType.Decl.Fields[i]
			break
		}
	}
	if fieldDecl == nil || fieldDecl.Type == nil {
		return
	}

	// Unwrap MutableType wrapper if present.
	ft := fieldDecl.Type
	if mt, ok := ft.(*ast.MutableType); ok && mt != nil {
		ft = mt.Elem
	}

	wt, ok := whereRefinementTypeExpr(ft)
	if !ok || wt == nil || wt.Predicate == nil {
		return
	}

	// Build the substitution: field name → stored value.
	// We do not substitute earlier sibling values because at a field-store site the other fields
	// are not syntactically available; leaving them unsubstituted lets the prover return
	// requiresUnknown, which safely falls through to ProofRuntime.
	subst := map[string]ast.Expr{fieldName: value}

	subject := "where refinement on field \"" + fieldName + "\""
	a.dischargeWherePredicate(wt.Predicate, subst, pos, subject)
}
