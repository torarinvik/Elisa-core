package semantic

import (
	"elisacore/src/ast"
	"strconv"
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
	// Most type queries have only a scalar leaf or a chain of single-child
	// wrappers. Avoid initializing graph state until a branch is reached.
	for {
		switch n := t.(type) {
		case *TypeParamType:
			return true
		case *ErrorUnionType:
			t = n.Value
		case *OptionalType:
			t = n.Value
		case *RefType:
			t = n.Elem
		case *ArrayType:
			t = n.Elem
		case *DArrayType:
			t = n.Elem
		case *ViewType:
			t = n.Elem
		case *AggregateStateType:
			t = n.Base
		case *IDType, *TupleType, *GenericInstanceType, *FuncType, *EnumType:
			return containsTypeParamSeen(t, &typeParamSeen{})
		default:
			return false
		}
	}
}

func (a *Analyzer) containsTypeParamCached(t Type) bool {
	if a == nil || !a.typeShapesFrozen || t == nil {
		return containsTypeParam(t)
	}
	return a.memoTypePredicate(&a.typeParamMemo, t, func() bool { return containsTypeParam(t) })
}

// Shared tuple graphs can contain exponentially many paths to the same node.
// Keep a small inline set and only allocate a map for larger graphs.
type typeParamSeen struct {
	generics     [16]*GenericInstanceType
	genericCount int
	genericLarge map[*GenericInstanceType]bool
	enums        [16]*EnumType
	enumCount    int
	enumLarge    map[*EnumType]bool
	small        [16]*TupleType
	count        int
	large        map[*TupleType]bool
}

func (s *typeParamSeen) addTuple(t *TupleType) bool {
	if s.large != nil {
		if s.large[t] {
			return false
		}
		s.large[t] = true
		return true
	}
	for i := 0; i < s.count; i++ {
		if s.small[i] == t {
			return false
		}
	}
	if s.count < len(s.small) {
		s.small[s.count] = t
		s.count++
		return true
	}
	s.large = make(map[*TupleType]bool, 2*len(s.small))
	for _, prior := range s.small {
		s.large[prior] = true
	}
	s.large[t] = true
	return true
}

func (s *typeParamSeen) addEnum(t *EnumType) bool {
	if s.enumLarge != nil {
		if s.enumLarge[t] {
			return false
		}
		s.enumLarge[t] = true
		return true
	}
	for i := 0; i < s.enumCount; i++ {
		if s.enums[i] == t {
			return false
		}
	}
	if s.enumCount < len(s.enums) {
		s.enums[s.enumCount] = t
		s.enumCount++
		return true
	}
	s.enumLarge = make(map[*EnumType]bool, 2*len(s.enums))
	for _, prior := range s.enums {
		s.enumLarge[prior] = true
	}
	s.enumLarge[t] = true
	return true
}

func (s *typeParamSeen) addGeneric(t *GenericInstanceType) bool {
	if s.genericLarge != nil {
		if s.genericLarge[t] {
			return false
		}
		s.genericLarge[t] = true
		return true
	}
	for i := 0; i < s.genericCount; i++ {
		if s.generics[i] == t {
			return false
		}
	}
	if s.genericCount < len(s.generics) {
		s.generics[s.genericCount] = t
		s.genericCount++
		return true
	}
	s.genericLarge = make(map[*GenericInstanceType]bool, 2*len(s.generics))
	for _, prior := range s.generics {
		s.genericLarge[prior] = true
	}
	s.genericLarge[t] = true
	return true
}

// Enum cycles and repeated tuple and generic nodes are visited once per query.
func containsTypeParamSeen(t Type, seen *typeParamSeen) bool {
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
		if !seen.addTuple(n) {
			return false
		}
		for _, field := range n.Fields {
			if containsTypeParamSeen(field.Type, seen) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		if !seen.addGeneric(n) {
			return false
		}
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
		if !seen.addEnum(n) {
			return false
		}
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
		a.recordResolvedGlobalStorage(n, sym)
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
		a.requireWritableMutationPath(n)
		return field.Type
	case *ast.IndexExpr:
		if n.Fallback != nil {
			a.errorf(n.Pos(), "safe index fallback cannot be used as an assignment target")
			return invalidType
		}
		targetType := a.analyzeIndexExpr(n)
		if kind, ok := valueOnlyIndexKind(a.exprType(n.Object)); ok {
			a.errorf(n.Pos(), "cannot assign to %s", kind)
			return invalidType
		}
		if a.readOnlyViewIndexSource(n.Object) {
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

// readOnlyViewIndexSource distinguishes a readonly optimization fact from a
// mutable view capability stored in an otherwise readonly aggregate field.
func (a *Analyzer) readOnlyViewIndexSource(expr ast.Expr) bool {
	facts, ok := a.lookupExprFacts(expr)
	if !ok || !facts.ReadOnly {
		return false
	}
	if mutableViewCapabilityType(a.exprType(expr)) {
		return false
	}
	return true
}

func mutableViewCapabilityType(typ Type) bool {
	for {
		switch t := typ.(type) {
		case *AggregateStateType:
			if t == nil {
				return false
			}
			typ = t.Base
		case *RefType:
			if t == nil {
				return false
			}
			typ = t.Elem
		case *ViewType:
			return t != nil && t.Mutable
		default:
			return false
		}
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
		if v.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(a.exprType(v.Operand), a.exprType(v)) {
			return true
		}
	case *ast.AddrOfExpr:
		// `region r: a: mutable Arena& = &r` -- the scope OWNS r's arena, and allocating from it
		// mutates it. `&r` types read-only only because the region binding cannot be reassigned.
		if a.regionBindingIdent(v.Operand) {
			return true
		}
	}
	if ref, ok := a.exprType(stripped).(*RefType); ok && ref != nil && ref.Mutable {
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
