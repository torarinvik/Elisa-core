package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) variantConstructorMoveReason(kind string, containerName string, variant *EnumVariant, index int) string {
	if variant == nil {
		return "move into " + kind + " constructor payload"
	}
	if label := variant.PayloadLabel(index); label != "" {
		return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name+"."+label)
	}
	return "move into " + kind + " payload " + strconv.Quote(containerName+"."+variant.Name) + " argument " + strconv.Itoa(index+1)
}

func (a *Analyzer) enumConstructorMoveReason(enumName string, variant *EnumVariant, index int) string {
	return a.variantConstructorMoveReason("enum", enumName, variant, index)
}

func containsTypeParam(t Type) bool {
	return containsTypeParamSeen(t, nil)
}

// containsTypeParamSeen walks t, remembering the enums it has entered: a recursive enum
// (`enum Expr: Add(Expr&, Expr&)`) reaches itself through its payloads.
func containsTypeParamSeen(t Type, seen map[*EnumType]bool) bool {
	switch n := t.(type) {
	case nil:
		return false
	case *TypeParamType:
		return true
	case *IDType:
		return containsTypeParamSeen(n.Tag, seen) || containsTypeParamSeen(n.Storage, seen)
	case *ErrorUnionType:
		return containsTypeParamSeen(n.Value, seen)
	case *OptionalType:
		return containsTypeParamSeen(n.Value, seen)
	case *RefType:
		return containsTypeParamSeen(n.Elem, seen)
	case *ArrayType:
		return containsTypeParamSeen(n.Elem, seen)
	case *DArrayType:
		return containsTypeParamSeen(n.Elem, seen)
	case *ViewType:
		return containsTypeParamSeen(n.Elem, seen)
	case *TupleType:
		for _, field := range n.Fields {
			if containsTypeParamSeen(field.Type, seen) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		for _, arg := range n.Args {
			if containsTypeParamSeen(arg, seen) {
				return true
			}
		}
		return containsTypeParamSeen(n.Base, seen)
	case *AggregateStateType:
		return containsTypeParamSeen(n.Base, seen)
	case *FuncType:
		if len(n.GenericParams) != 0 {
			return true
		}
		for _, param := range n.Params {
			if containsTypeParamSeen(param, seen) {
				return true
			}
		}
		return containsTypeParamSeen(n.Return, seen)
	case *EnumType:
		if seen[n] {
			return false
		}
		if seen == nil {
			seen = map[*EnumType]bool{}
		}
		seen[n] = true
		for _, variant := range n.Variants {
			for _, payload := range variant.Payload {
				if containsTypeParamSeen(payload, seen) {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.assignmentTargetType(n.Inner)
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
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				if !ref.Mutable {
					a.errorf(n.Pos(), "cannot assign through readonly ref %q", sym.Name)
					a.reportReadonlyRefMutationNote(n.Pos(), n, sym.Type)
					return invalidType
				}
				// `r <- v` / `r += v` store through the reference.
				a.requireWriteThroughTarget(n, ref)
				return ref.Elem
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
			return sym.Type
		}
		if a.currentScope != nil {
			if current, exists := a.currentScope.Symbols[n.Name]; exists && current == sym && a.currentScope.Parent != nil {
				if parent, ok := a.currentScope.Parent.Lookup(n.Name); ok && parent.Node == sym.Node && parent.Kind == sym.Kind && parent.Mutable {
					return parent.Type
				}
			}
		}
		return sym.Type
	case *ast.FieldExpr:
		if n.Safe {
			a.errorf(n.Pos(), "optional chaining cannot be used as an assignment target")
			return invalidType
		}
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if field.Ghost && a.ghostReadAllowed == 0 {
			// SOUNDNESS: a ghost field is erased from codegen — it has no runtime storage
			// and cannot be written by real code. Ghost-field writes are only meaningful in
			// a contract/ghost context (e.g., inside a `ghost` declaration block).
			a.errorf(n.Pos(), "ghost field %q is verification-only and cannot be written by real code: it is erased from codegen, so it may appear only in contracts (requires/ensure/invariant/assert) or `ghost` declarations", n.Field)
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return field.Type
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "safe index fallback cannot be used as an assignment target")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot assign to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
		return targetType
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

// currentRefType is what flow analysis proves about a reference binding right now: the
// refined type while a proof (`if r != null:`, an early return on null) is in force, else the
// declared type. A proof is a refinement, not a new symbol, so the symbol alone never shows it.
func (a *Analyzer) currentRefType(ident *ast.Ident, declared *RefType) *RefType {
	if refined, ok := a.lookupRefinedExprType(ident); ok {
		if refinedRef, ok := refined.(*RefType); ok && refinedRef != nil {
			return refinedRef
		}
	}
	return declared
}

// requireWriteThroughTarget rejects a store through a reference that is not proven non-null:
// an unproven `mutable T&?` has no referent to store into (a write through null: a segfault at
// -O0, a trap at -O2), and inside `if r == null:` it is proven null.
func (a *Analyzer) requireWriteThroughTarget(ident *ast.Ident, declared *RefType) {
	if current := a.currentRefType(ident, declared); current.State != RefStateNonNull {
		a.errorf(ident.Pos(), "assignment through reference requires proven non-null reference, got %s", current)
	}
}

// legacyRefInitIsWritable reports whether a legacy `x: mutable T& = init` may become a writable
// reference although init's type is read-only: init is what a `mutable T&` parameter would accept
// (a writable place such as a mutable field or element -- never a binding that merely holds a
// read-only ref), a conditional whose arms all are, `null`/`zeroed` (no referent to protect), or
// an Unsafe pointer cast (`raw.cast[heap T&]` over an allocation's `void&`), which is itself the
// trust point and could name `mutable T&` just as well. A plain coercion (`"abc".cast[u8&]`)
// keeps its operand's read-only referent.
func (a *Analyzer) legacyRefInitIsWritable(value ast.Expr) bool {
	stripped := stripOptimizationParens(value)
	switch v := stripped.(type) {
	case nil:
		return false
	case *ast.NullLit, *ast.ZeroedLit:
		return true
	case *ast.TernaryExpr:
		return a.legacyRefInitIsWritable(v.Value) && a.legacyRefInitIsWritable(v.Alt)
	case *ast.CastExpr:
		if v.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(a.exprTypes[v.Operand], a.exprTypes[v]) {
			return true
		}
	case *ast.AddrOfExpr:
		// `region r: a: mutable Arena& = &r` -- the scope OWNS r's arena, and allocating from it
		// mutates it. `&r` types read-only only because the region binding cannot be reassigned.
		if a.regionBindingIdent(v.Operand) {
			return true
		}
	}
	if ref, ok := a.exprTypes[stripped].(*RefType); ok && ref != nil && ref.Mutable {
		return true
	}
	return !a.refBindingIdent(stripped) && (a.mutationPathWritable(stripped) || a.exprCanYieldWritableRef(stripped))
}

// regionBindingIdent reports whether expr names a `region NAME:` binding in scope.
func (a *Analyzer) regionBindingIdent(expr ast.Expr) bool {
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	return found && sym != nil && sym.Kind == SymbolRegion
}

// refBindingIdent reports whether expr names a binding whose type is itself a reference. Such a
// binding's write capability is its type's `mutable`, never the binding's own rebindability.
func (a *Analyzer) refBindingIdent(expr ast.Expr) bool {
	ident, ok := stripMutationTargetExpr(expr).(*ast.Ident)
	if !ok || ident == nil {
		return false
	}
	var (
		sym   *Symbol
		found bool
	)
	if a.currentScope != nil {
		sym, found = a.currentScope.Lookup(ident.Name)
	}
	if !found {
		if sym, _, found = a.lookupVisibleGlobal(ident.Name); !found {
			return false
		}
	}
	_, isRef := sym.Type.(*RefType)
	return isRef
}

// isBytePointerValue reports a value that is a POINTER to bytes: a string literal, a `cstr`, or
// any `u8&` (the language's C-string/byte-pointer type).
func isBytePointerValue(value ast.Expr, valueType Type) bool {
	if _, ok := stripOptimizationParens(value).(*ast.StringLit); ok {
		return true
	}
	switch t := valueType.(type) {
	case *CStrType:
		return true
	case *RefType:
		return t != nil && isBytePointerArithmeticRef(t)
	}
	return false
}

// mutableScalarRefTarget recognizes a mutable scalar reference whose assignment
// meaning depends on the RHS: `place <- 1` writes through it, while
// `place <- other_ref` rebinds the reference slot. The decision belongs after
// RHS analysis because the same syntax supports both operations.
func (a *Analyzer) mutableScalarRefTarget(expr ast.Expr) (*RefType, bool) {
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || ident == nil || a == nil || a.currentScope == nil {
		return nil, false
	}
	sym, found := a.currentScope.Lookup(ident.Name)
	if !found {
		sym, _, found = a.lookupVisibleGlobal(ident.Name)
	}
	if !found || sym == nil || !sym.Mutable {
		return nil, false
	}
	ref, ok := sym.Type.(*RefType)
	if !ok || ref == nil || !ref.Mutable || (!IsNumericType(ref.Elem) && !IsBoolType(ref.Elem)) {
		return nil, false
	}
	return ref, true
}

func (a *Analyzer) optionalAssignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.optionalAssignmentTargetType(n.Inner)
	case *ast.Ident:
		valueType := a.analyzeExpr(n)
		boundType, ok := conditionOptionalBindType(valueType)
		if !ok {
			a.errorf(n.Pos(), "?= requires a nullable reference target, got %s", valueType)
			return invalidType
		}
		refType, ok := boundType.(*RefType)
		if !ok || refType == nil {
			a.errorf(n.Pos(), "?= requires a nullable reference target, got %s", valueType)
			return invalidType
		}
		if !a.requireWritableMutationPath(n) {
			return invalidType
		}
		return refType.Elem
	case *ast.FieldExpr:
		if !n.Safe {
			a.errorf(n.Pos(), "?= requires a nullable reference target; use <- for ordinary assignment")
			return invalidType
		}
		receiverType := a.analyzeExpr(n.Object)
		boundType, ok := conditionOptionalBindType(receiverType)
		if !ok {
			a.errorf(n.Pos(), "?= requires a nullable reference receiver, got %s", receiverType)
			return invalidType
		}
		refType, ok := boundType.(*RefType)
		if !ok || refType == nil {
			a.errorf(n.Pos(), "?= requires a nullable reference receiver, got %s", receiverType)
			return invalidType
		}
		field, ok := a.lookupField(refType, n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		a.requireWritableMutationPath(n.Object)
		return field.Type
	default:
		a.errorf(expr.Pos(), "invalid ?= target")
		return invalidType
	}
}

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
	objType, ok := a.exprTypes[stripped]
	if !ok || objType == nil {
		objType = a.analyzeExpr(stripped)
	}
	if ref, ok := objType.(*RefType); ok {
		return a.refExprAllowsMutation(stripped, ref)
	}
	// A slice-derived bounded view is writable only when its source was writable
	// (recorded at bind time). Writing through a view of an immutable source is
	// rejected; untracked views keep their existing behavior.
	if ident, ok := stripped.(*ast.Ident); ok && ident != nil {
		if mut, tracked := a.currentViewMutable[ident.Name]; tracked {
			return mut
		}
	}
	switch n := stripped.(type) {
	case *ast.FieldExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		return a.mutationPathWritable(n.Object)
	case *ast.SliceExpr:
		return a.mutationPathWritable(n.Object)
	default:
		return true
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
	t := a.exprTypes[stripped]
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
			if _, isRef := a.exprTypes[cur].(*RefType); isRef {
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
		a.requireWritableMutationPath(n.Object)
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "cannot take a reference to a safe index fallback expression")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprTypes[n.Object]); ok {
			a.errorf(n.Pos(), "cannot take a reference to %s", kind)
			return invalidType
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			a.errorf(n.Pos(), "cannot take a reference to readonly view index result")
			return invalidType
		}
		a.requireWritableMutationPath(n.Object)
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
		if objType, ok := a.exprTypes[n.Object].(*RefType); ok {
			return objType.Storage
		}
		return a.inferAddrOfStorage(n.Object)
	case *ast.IndexExpr:
		switch objType := a.exprTypes[n.Object].(type) {
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
