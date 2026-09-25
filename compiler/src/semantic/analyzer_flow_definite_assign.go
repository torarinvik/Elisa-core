package semantic

import (
	"elisacore/src/ast"
)

// Definite-assignment tracking for locals whose storage can start with an
// invalid representation: an omitted initializer or `= zeroed` on a type that
// requires a non-null reference/handle value.
//
// A local is seeded as "uninitialized". Reading it before a whole-binding
// assignment is a use of uninitialized memory. Projected writes and address
// escape do not establish the whole value: the analysis has no field-sensitive
// initialization proof or callee write summary. The flag lives in
// affineValueState keyed at {root, ""}, so clone/merge/snapshot across control
// flow come from the affine machinery.
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
func (a *Analyzer) typeHasInvalidZeroValue(t Type) bool {
	return a.zeroedTypeHasInvalidRepresentation(t)
}

// markInvalidUninitialized seeds a local whose storage cannot be read until a
// whole-value assignment establishes its representation.
func (a *Analyzer) markInvalidUninitialized(sym *Symbol) {
	if a == nil || sym == nil || !a.typeHasInvalidZeroValue(sym.Type) {
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

// clearInvalidUninitializedForExpr marks a local initialized after a whole
// binding assignment. A field/index write or address escape cannot prove that
// every invalid leaf has been initialized.
func (a *Analyzer) clearInvalidUninitializedForExpr(expr ast.Expr) {
	if a == nil || len(a.currentAffineValues) == 0 {
		return
	}
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		expr = paren.Inner
	}
	if _, wholeBinding := expr.(*ast.Ident); !wholeBinding {
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

// isInvalidUninitializedSymbol reports whether a local is still in its seeded
// invalid/uninitialized state.
func (a *Analyzer) isInvalidUninitializedSymbol(sym *Symbol) bool {
	if a == nil || sym == nil || len(a.currentAffineValues) == 0 {
		return false
	}
	state, ok := a.currentAffineValues[affineValueKey{Root: symbolAliasRoot(sym)}]
	return ok && state.Uninitialized
}
