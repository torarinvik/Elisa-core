package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func stripMutationTargetExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CanExpr:
			expr = n.Expr
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutationPathWritable(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return true
	}
	objType, ok := a.lookupExprType(stripped)
	if !ok || objType == nil {
		objType = a.analyzeExpr(stripped)
	}
	if ref, ok := objType.(*RefType); ok {
		if mutableViewCapabilityType(ref.Elem) {
			return true
		}
		return a.refExprAllowsMutation(stripped, ref)
	}
	// A slice-derived bounded view is writable only when its source was writable
	// (recorded at bind time). Writing through a view of an immutable source is
	// rejected; untracked views keep their existing behavior.
	if ident, ok := stripped.(*ast.Ident); ok && ident != nil {
		if mut, tracked := a.currentViewMutable[ident.Name]; tracked {
			return mut
		}
		var (
			sym   *Symbol
			found bool
		)
		if a.currentScope != nil {
			sym, found = a.currentScope.Lookup(ident.Name)
		}
		if !found {
			sym, _, found = a.lookupVisibleGlobal(ident.Name)
		}
		if found && sym.Kind == SymbolGlobal {
			if view, ok := objType.(*ViewType); ok {
				return view.Mutable
			}
			return sym.Mutable
		}
		// Preserve local and parameter place semantics here; their mutability is
		// constrained by binding/ref checks at assignment and borrow sites.
		return true
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		field, found := a.lookupFieldNoError(a.exprType(n.Object), n.Field)
		// An immutable aggregate may store a mutable view by value. Writing
		// through that view mutates its backing elements, not the field slot.
		if found {
			if view, ok := field.Type.(*ViewType); ok && view.Mutable {
				return true
			}
		}
		if ref, ok := a.exprType(n.Object).(*RefType); ok && !a.refExprAllowsMutation(n.Object, ref) {
			return false
		}
		return (found && field.Mutable) || a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		// Indexing through a mutable view field mutates the borrowed elements,
		// not the immutable field slot that stores the view itself.
		if view, ok := a.exprType(n.Object).(*ViewType); ok && view.Mutable {
			return true
		}
		return a.mutationPathWritable(n.Object)
	case *ast.SliceExpr:
		return a.mutationPathWritable(n.Object)
	default:
		return false
	}
}

func (a *Analyzer) requireWritableMutationPath(expr ast.Expr) bool {
	if a.mutationPathWritable(expr) {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
	a.errorf(stripped.Pos(), "cannot mutate through readonly ref")
	a.reportReadonlyRefMutationNote(stripped.Pos(), stripped, nil)
	return false
}

func mutableRefSuggestionString(t Type) (string, bool) {
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return "", false
	}
	// The type printer puts the storage class first (`heap mutable T&`), which does not parse;
	// a suggestion is spelled the way source writes a writable reference: `mutable heap T&`.
	cloned := cloneRefType(ref)
	cloned.Mutable = false
	return "mutable " + cloned.String(), true
}

func writableRefAssignableIgnoringMutability(expected Type, actual Type) bool {
	expectedRef, ok := expected.(*RefType)
	if !ok || expectedRef == nil || !expectedRef.Mutable {
		return false
	}
	actualRef, ok := actual.(*RefType)
	if !ok || actualRef == nil || actualRef.Mutable {
		return false
	}
	expectedReadonly := cloneRefType(expectedRef)
	expectedReadonly.Mutable = false
	actualReadonly := cloneRefType(actualRef)
	actualReadonly.Mutable = false
	return AssignableTo(expectedReadonly, actualReadonly)
}

func (a *Analyzer) writableRefSuggestionForExpr(expr ast.Expr) (string, bool) {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return "", false
	}
	t := a.exprType(stripped)
	if t == nil {
		t = a.analyzeExpr(stripped)
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		return suggestion, true
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return "", false
		}
		return a.writableRefSuggestionForExpr(n.Object)
	case *ast.SliceExpr:
		return a.writableRefSuggestionForExpr(n.Object)
	default:
		return "", false
	}
}

// rebindableReadOnlyRefRoot returns the binding at the root of a mutation path when that binding is
// re-pointable (`mutable`) but its reference type is read-only: the case where the `mutable` the
// program wrote does not grant the write it attempted. A path that passes through another reference
// before reaching the root is not blamed on the root.
func (a *Analyzer) rebindableReadOnlyRefRoot(expr ast.Expr) (*Symbol, string, bool) {
	cur := stripMutationTargetExpr(expr)
	for {
		var object ast.Expr
		switch n := cur.(type) {
		case *ast.FieldExpr:
			object = n.Object
		case *ast.IndexExpr:
			object = n.Object
		case *ast.SliceExpr:
			object = n.Object
		case *ast.Ident:
			var (
				sym   *Symbol
				found bool
			)
			if a.currentScope != nil {
				sym, found = a.currentScope.Lookup(n.Name)
			}
			if !found {
				if sym, _, found = a.lookupVisibleGlobal(n.Name); !found {
					return nil, "", false
				}
			}
			if sym == nil || !sym.Mutable {
				return nil, "", false
			}
			if ref, isRef := sym.Type.(*RefType); !isRef || ref == nil || ref.Mutable {
				return nil, "", false
			}
			return sym, n.Name, true
		default:
			return nil, "", false
		}
		cur = stripMutationTargetExpr(object)
		if _, isIdent := cur.(*ast.Ident); !isIdent {
			if _, isRef := a.exprType(cur).(*RefType); isRef {
				return nil, "", false
			}
		}
	}
}

// reportRebindableReadOnlyRefNote explains a rejected write (or writable-reference argument) whose
// root is a rebindable binding of a read-only reference, and reports whether it did.
func (a *Analyzer) reportRebindableReadOnlyRefNote(pos lexer.Pos, expr ast.Expr) bool {
	sym, name, ok := a.rebindableReadOnlyRefRoot(expr)
	if !ok {
		return false
	}
	suggestion, _ := mutableRefSuggestionString(sym.Type)
	if decl, isDecl := sym.Node.(*ast.VarDeclStmt); isDecl && sym.Kind == SymbolLocal && !decl.BindingExplicit && decl.Type != nil {
		a.errorf(pos, "note: %q was initialized from a read-only reference, so its `mutable` makes it rebindable, not writable; initialize it from a writable reference (%s) to write through it", name, suggestion)
		return true
	}
	a.errorf(pos, "note: `mutable` on %q lets it be re-pointed, not written through; declare its type %s to write through it", name, suggestion)
	return true
}

func (a *Analyzer) reportReadonlyRefMutationNote(pos lexer.Pos, expr ast.Expr, t Type) {
	if a == nil {
		return
	}
	if a.reportRebindableReadOnlyRefNote(pos, expr) {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(t); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	if suggestion, ok := a.writableRefSuggestionForExpr(expr); ok {
		a.errorf(pos, "note: plain refs T& are readonly; use %s if this reference should allow writes", suggestion)
		return
	}
	a.errorf(pos, "note: plain refs T& are readonly; use mutable T& if this reference should allow writes")
}

func (a *Analyzer) reportMutableRefArgumentNote(pos lexer.Pos, expected Type, actual Type) {
	if a == nil || !writableRefAssignableIgnoringMutability(expected, actual) {
		return
	}
	if suggestion, ok := mutableRefSuggestionString(expected); ok {
		a.errorf(pos, "note: use %s here if the callee should be allowed to write through it", suggestion)
	}
}

func (a *Analyzer) refExprAllowsMutation(expr ast.Expr, ref *RefType) bool {
	if ref == nil {
		return false
	}
	if ref.Mutable {
		return true
	}
	stripped := stripMutationTargetExpr(expr)
	switch n := stripped.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				return false
			}
		}
		// A reference binding writes through only when its TYPE is `mutable T&` (checked
		// above). A rebindable binding (`mutable x: T& = ro`, `mutable p: T&`) may be
		// re-pointed, but treating that as write permission wrote through `ro`: into a string
		// literal's static bytes (SIGBUS), or into an immutable local.
		if _, isRef := sym.Type.(*RefType); isRef {
			return false
		}
		return sym.Mutable
	case *ast.FieldExpr:
		if n.Safe {
			return false
		}
		field, ok := a.lookupFieldNoError(a.analyzeExpr(n.Object), n.Field)
		if !ok {
			return false
		}
		return field.Mutable
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return false
		}
		return a.mutationPathWritable(n.Object)
	default:
		return false
	}
}

func (a *Analyzer) asRefTargetType(expr ast.Expr, asKind string) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, _, ok = a.lookupVisibleGlobal(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q (use = to introduce a new local; <- requires an existing mutable target)", n.Name)
				return invalidType
			}
		}
		a.recordResolvedGlobalStorage(n, sym)
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
					return a.refTypeWithAsKind(sym.Type, asKind)
				}
				return a.refTypeWithAsKind(sym.Type, asKind)
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as a reference target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n)
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "cannot take a reference to a safe index fallback expression")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprType(n.Object)); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		if a.readOnlyViewIndexSource(n.Object) {
			a.errorf(n.Pos(), "cannot take a reference to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n)
		return a.refTypeWithAsKind(targetType, asKind)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) refTypeWithAsKind(t Type, asKind string) Type {
	ref, ok := t.(*RefType)
	if !ok {
		return t
	}
	switch asKind {
	case "&":
		return cloneRefTypeWithState(ref, RefStateNonNull)
	case "!":
		return cloneRefTypeWithState(ref, RefStateNull)
	default:
		return t
	}
}

func (a *Analyzer) inferAddrOfStorage(expr ast.Expr) RefStorage {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.inferAddrOfStorage(n.Inner)
	case *ast.Ident:
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				switch sym.Kind {
				case SymbolLocal, SymbolParam, SymbolRegion:
					return RefStorageStack
				case SymbolGlobal:
					return RefStorageStatic
				}
			}
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym.Kind == SymbolGlobal {
			return RefStorageStatic
		}
	case *ast.FieldExpr:
		if objType, ok := a.exprType(n.Object).(*RefType); ok {
			return objType.Storage
		}
		return a.inferAddrOfStorage(n.Object)
	case *ast.IndexExpr:
		switch objType := a.exprType(n.Object).(type) {
		case *RefType:
			if _, ok := objType.Elem.(*ArrayType); ok {
				return objType.Storage
			}
			return RefStorageAny
		case *ArrayType:
			return a.inferAddrOfStorage(n.Object)
		}
	}
	return RefStorageAny
}
