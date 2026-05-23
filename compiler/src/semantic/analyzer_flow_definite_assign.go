package semantic

import (
	"elisacore/src/ast"
)

// Definite-assignment tracking for locals declared with `= zeroed`.
//
// A local `x: T = zeroed` is seeded as "uninitialized". Reading it (or a field
// of it) before any assignment to a path rooted at it is a use of uninitialized
// memory. Any assignment to the variable or a field/element of it, or taking its
// address (which may initialize it through the pointer), clears the state. The
// flag lives in affineValueState keyed at {root, ""}, so clone/merge/snapshot
// across control flow come for free from the affine machinery.
//
// `Arena`-typed locals are exempt: a zeroed Arena is a valid empty arena whose
// zeroed state is its initialized state.

// isZeroedInitializer reports whether a variable initializer is the `zeroed`
// literal (through transparent paren/cast wrappers).
func isZeroedInitializer(expr ast.Expr) bool {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.ZeroedLit:
			return true
		default:
			return false
		}
	}
	return false
}

// definiteAssignRootSymbol walks an lvalue/path expression to the local it is
// rooted at and returns that symbol when it is a local of the current function.
func (a *Analyzer) definiteAssignRootSymbol(expr ast.Expr) *Symbol {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.FieldExpr:
			expr = n.Object
		case *ast.IndexExpr:
			expr = n.Object
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			sym, ok := a.currentScope.Lookup(n.Name)
			if !ok || sym == nil {
				return nil
			}
			sym = symbolAliasRoot(sym)
			if sym.Kind != SymbolLocal {
				return nil
			}
			return sym
		default:
			return nil
		}
	}
	return nil
}

// typeHasInvalidZeroValue reports whether `zeroed` produces an operationally
// invalid value for a type: a non-optional reference (null) or a handle/id
// (a zero handle is not a live object). Reading such a value before assignment
// is a null-dereference / garbage-handle hazard. For every other type (scalars,
// structs, containers, optional refs, Arena) zero is a usable default, so those
// are never seeded — keeping false positives near zero against the pervasive
// `= zeroed`-as-default idiom.
func typeHasInvalidZeroValue(t Type) bool {
	switch tt := t.(type) {
	case *RefType:
		return tt != nil && tt.State == RefStateNonNull
	case *IDType:
		return true
	case *DStrType:
		// A non-optional borrowed string is a pointer; zeroed is a null cstr.
		return true
	}
	return false
}

// markZeroedUninitialized seeds a local as uninitialized at its `= zeroed` decl,
// but only when zero is an operationally invalid value for the local's type.
func (a *Analyzer) markZeroedUninitialized(sym *Symbol) {
	if a == nil || sym == nil || !typeHasInvalidZeroValue(sym.Type) {
		return
	}
	if a.currentAffineValues == nil {
		a.currentAffineValues = map[affineValueKey]affineValueState{}
	}
	key := affineValueKey{Root: sym}
	state := a.currentAffineValues[key]
	state.Uninitialized = true
	a.currentAffineValues[key] = state
}

// clearZeroedUninitializedForExpr marks the local rooted at expr as initialized
// (after an assignment to it / a field of it, or taking its address).
func (a *Analyzer) clearZeroedUninitializedForExpr(expr ast.Expr) {
	if a == nil || len(a.currentAffineValues) == 0 {
		return
	}
	sym := a.definiteAssignRootSymbol(expr)
	if sym == nil {
		return
	}
	key := affineValueKey{Root: sym}
	if state, ok := a.currentAffineValues[key]; ok && state.Uninitialized {
		state.Uninitialized = false
		a.currentAffineValues[key] = state
	}
}

// isZeroedUninitializedSymbol reports whether a local is still in its seeded
// zeroed-uninitialized state.
func (a *Analyzer) isZeroedUninitializedSymbol(sym *Symbol) bool {
	if a == nil || sym == nil || len(a.currentAffineValues) == 0 {
		return false
	}
	state, ok := a.currentAffineValues[affineValueKey{Root: symbolAliasRoot(sym)}]
	return ok && state.Uninitialized
}
