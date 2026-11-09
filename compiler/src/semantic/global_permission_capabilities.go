package semantic

import (
	"elisacore/src/ast"
)

// globalWholeValueReadRequiresGrant reports whether reading expr as a value
// observes mutable storage nested inside an otherwise immutable global value.
// Projection receivers are handled separately so an immutable sibling field
// does not inherit the grant requirement from another field in the aggregate.
func (a *Analyzer) globalWholeValueReadRequiresGrant(expr ast.Expr) bool {
	sym, ok := a.globalStorageRoot(expr)
	if !ok || sym.Kind != SymbolGlobal || sym.Mutable {
		return false
	}
	typ, typed := a.lookupExprType(expr)
	if !typed {
		if _, ident := expr.(*ast.Ident); ident {
			typ = sym.Type
		} else {
			return false
		}
	}
	return a.typeContainsMutableStorage(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
}

// typeContainsMutableStorage checks whether a value can expose writable state
// through one of its fields, container elements, or mutable references.
func (a *Analyzer) typeContainsMutableStorage(typ Type, seen map[Type]bool, genericSeen map[*StructType][][]Type, depth int) bool {
	if typ == nil {
		return false
	}
	typ = StripAggregateStateType(typ)
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("global mutable-storage traversal", semanticTraversalDepthLimit)
		// Treat an incomplete traversal as mutable so it cannot silently omit
		// a required Global.Read effect.
		return true
	}
	if seen[typ] {
		return false
	}
	seen[typ] = true
	contains := func(child Type) bool {
		return a.typeContainsMutableStorage(child, seen, genericSeen, depth+1)
	}
	fieldContains := func(field Field) bool {
		if field.Ghost || field.Phantom {
			return false
		}
		return field.Mutable || contains(field.Type)
	}
	switch t := typ.(type) {
	case *RefType:
		return t != nil && t.Mutable
	case *BitGroupType:
		return t != nil && len(t.Members) != 0
	case *ArrayType:
		return t != nil && contains(t.Elem)
	case *DArrayType:
		return t != nil && contains(t.Elem)
	case *ViewType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *OptionalType:
		return t != nil && contains(t.Value)
	case *ErrorUnionType:
		if t == nil {
			return false
		}
		if contains(t.Value) {
			return true
		}
		if t.Errors != nil {
			for _, payloads := range t.Errors.Payloads {
				for _, payload := range payloads {
					if contains(payload) {
						return true
					}
				}
			}
		}
		return false
	case *TupleType:
		if t != nil {
			for _, field := range t.Fields {
				if contains(field.Type) {
					return true
				}
			}
		}
	case *DictType:
		return t != nil && (contains(t.Key) || contains(t.Value))
	case *SetType:
		return t != nil && contains(t.Elem)
	case *DictEntryType:
		return t != nil && (t.Mutable || contains(t.Dict))
	case *StoreRowsViewType:
		return t != nil
	case *StoreRowViewType:
		return t != nil
	case *StructType:
		if t != nil {
			for _, field := range t.Fields {
				if fieldContains(field) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		if t == nil {
			return false
		}
		if base, ok := t.Base.(*StructType); ok && base != nil {
			for _, previousArgs := range genericSeen[base] {
				if len(previousArgs) != len(t.Args) {
					continue
				}
				sameArgs := true
				for i := range previousArgs {
					if !SameType(previousArgs[i], t.Args[i]) {
						sameArgs = false
						break
					}
				}
				if sameArgs {
					return false
				}
			}
			genericSeen[base] = append(genericSeen[base], append([]Type(nil), t.Args...))
			bindings := genericBindingsForStructInstance(base, t.Args)
			for _, field := range base.Fields {
				if field.Ghost || field.Phantom {
					continue
				}
				if field.Mutable {
					return true
				}
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if contains(fieldType) {
					return true
				}
			}
			return false
		}
		for _, arg := range t.Args {
			if contains(arg) {
				return true
			}
		}
		return contains(t.Base)
	case *EnumType:
		if t != nil {
			for _, field := range t.Common {
				if fieldContains(field) {
					return true
				}
			}
			for _, variant := range t.Variants {
				if variant == nil {
					continue
				}
				for _, payload := range variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedVariantViewType:
		if t != nil {
			if contains(t.Enum) {
				return true
			}
			if t.Variant != nil {
				for _, payload := range t.Variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *TypeParamType:
		// A generic value's mutability is not known until specialization. Keep
		// the mandatory read when this traversal cannot resolve the parameter.
		return true
	}
	return false
}

// globalPathHasMutableField reports whether a global-rooted projection selects a
// mutable field. Such a field is writable even when the global binding itself is
// immutable, and mutating collection methods may use that field as their receiver.
func (a *Analyzer) globalPathHasMutableField(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		field, ok := a.lookupFieldNoError(a.exprType(n.Object), n.Field)
		return (ok && field.Mutable) || a.globalPathHasMutableField(n.Object)
	case *ast.IndexExpr:
		return a.globalPathHasMutableField(n.Object)
	case *ast.SliceExpr:
		return a.globalPathHasMutableField(n.Object)
	case *ast.ParenExpr:
		return a.globalPathHasMutableField(n.Inner)
	case *ast.AddrOfExpr:
		return a.globalPathHasMutableField(n.Operand)
	case *ast.CastExpr:
		if operand, _, ok := a.globalStorageCastOperand(n); ok {
			return a.globalPathHasMutableField(operand)
		}
		return false
	default:
		return false
	}
}

// globalProjectionBeginsMutablePath reports whether expr is the first projection
// that reaches mutable global storage. Reads use this to require Global.Read once
// per access path, even when the root global binding itself is immutable.
func (a *Analyzer) globalProjectionBeginsMutablePath(expr ast.Expr) bool {
	var object ast.Expr
	switch n := expr.(type) {
	case *ast.FieldExpr:
		object = n.Object
	case *ast.IndexExpr:
		object = n.Object
	case *ast.SliceExpr:
		object = n.Object
	default:
		return false
	}
	if _, mutable := a.mutableGlobalStorageRoot(expr); !mutable {
		return false
	}
	_, parentMutable := a.mutableGlobalStorageRoot(object)
	return !parentMutable
}

func mutableBorrowStorageType(t Type) bool {
	for {
		switch n := t.(type) {
		case *OptionalType:
			if n == nil {
				return false
			}
			t = n.Value
		case *RefType:
			return n != nil && n.Mutable
		case *ViewType:
			return n != nil && n.Mutable
		default:
			return false
		}
	}
}

// typeContainsMutableBorrowCapability reports whether a value can carry a
// writable reference or view, including through aggregate fields and elements.
// This is deliberately separate from typeContainsMutableStorage: a mutable
// by-value field makes its global storage writable, but copying that aggregate
// does not transfer a borrow capability.
func (a *Analyzer) typeContainsMutableBorrowCapability(typ Type, seen map[Type]bool, genericSeen map[*StructType][][]Type, depth int) bool {
	state := mutableBorrowVisitSet{large: seen}
	return a.typeContainsMutableBorrowCapabilityWithVisits(typ, &state, genericSeen, depth)
}

func (a *Analyzer) typeContainsMutableBorrowCapabilityWithVisits(typ Type, seen *mutableBorrowVisitSet, genericSeen map[*StructType][][]Type, depth int) bool {
	if typ == nil {
		return false
	}
	typ = StripAggregateStateType(typ)
	if depth > semanticTraversalDepthLimit {
		a.reportSemanticDepthLimit("global mutable-borrow capability traversal", semanticTraversalDepthLimit)
		// An incomplete traversal must not let a nested mutable capability escape
		// without its mandatory Global.Write effect.
		return true
	}
	// These cases are terminal for this query. Avoid writing them to the cycle
	// map: they cannot lead back to an ancestor, and their result does not depend
	// on any child type. Keep the depth check above this fast path so oversized
	// paths still report the same conservative diagnostic.
	switch t := typ.(type) {
	case *BuiltinType, *NeverType, *NullType, *InvalidType, *ErrorSetType, *OpaqueType:
		return false
	case *TypeParamType:
		// Until specialization, an opaque type parameter may carry a mutable
		// borrow. Conservatively require the effect at transfer boundaries.
		return !seen.has(typ)
	case *StoreRowsViewType:
		// Row views are writable proxies into backing store columns.
		return t != nil && !seen.has(typ)
	case *StoreRowViewType:
		return t != nil && !seen.has(typ)
	}
	if seen.has(typ) {
		return false
	}
	seen.add(typ)
	contains := func(child Type) bool {
		return a.typeContainsMutableBorrowCapabilityWithVisits(child, seen, genericSeen, depth+1)
	}
	fieldContains := func(field Field) bool {
		if field.Ghost || field.Phantom {
			return false
		}
		return contains(field.Type)
	}
	switch t := typ.(type) {
	case *RefType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *ViewType:
		return t != nil && (t.Mutable || contains(t.Elem))
	case *ArrayType:
		return t != nil && contains(t.Elem)
	case *DArrayType:
		return t != nil && contains(t.Elem)
	case *OptionalType:
		return t != nil && contains(t.Value)
	case *ErrorUnionType:
		if t == nil {
			return false
		}
		if contains(t.Value) {
			return true
		}
		if t.Errors != nil {
			for _, payloads := range t.Errors.Payloads {
				for _, payload := range payloads {
					if contains(payload) {
						return true
					}
				}
			}
		}
		return false
	case *TupleType:
		if t != nil {
			for _, field := range t.Fields {
				if contains(field.Type) {
					return true
				}
			}
		}
	case *DictType:
		return t != nil && (contains(t.Key) || contains(t.Value))
	case *SetType:
		return t != nil && contains(t.Elem)
	case *DictEntryType:
		return t != nil && (t.Mutable || contains(t.Dict))
	case *StoreRowsViewType:
		// Iteration yields StoreRowViewType proxies whose fields write through to
		// the store's backing columns. Keep the capability when the rows view is
		// copied, passed, or returned.
		return t != nil
	case *StoreRowViewType:
		// A row view is an addressable proxy into a SoA store, not a detached row
		// value; its synthetic fields are writable even when the binding is local.
		return t != nil
	case *StructType:
		if t != nil {
			for _, field := range t.Fields {
				if fieldContains(field) {
					return true
				}
			}
		}
	case *GenericInstanceType:
		if t == nil {
			return false
		}
		if base, ok := t.Base.(*StructType); ok && base != nil {
			for _, previousArgs := range genericSeen[base] {
				if len(previousArgs) != len(t.Args) {
					continue
				}
				sameArgs := true
				for i := range previousArgs {
					if !SameType(previousArgs[i], t.Args[i]) {
						sameArgs = false
						break
					}
				}
				if sameArgs {
					return false
				}
			}
			genericSeen[base] = append(genericSeen[base], append([]Type(nil), t.Args...))
			bindings := genericBindingsForStructInstance(base, t.Args)
			for _, field := range base.Fields {
				if field.Ghost || field.Phantom {
					continue
				}
				fieldType := a.substituteType(field.Type, bindings, nil, nil, nil)
				if contains(fieldType) {
					return true
				}
			}
			return false
		}
		for _, arg := range t.Args {
			if contains(arg) {
				return true
			}
		}
		return contains(t.Base)
	case *EnumType:
		if t != nil {
			for _, field := range t.Common {
				if fieldContains(field) {
					return true
				}
			}
			for _, variant := range t.Variants {
				if variant == nil {
					continue
				}
				for _, payload := range variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedVariantViewType:
		if t != nil {
			if contains(t.Enum) {
				return true
			}
			if t.Variant != nil {
				for _, payload := range t.Variant.Payload {
					if contains(payload) {
						return true
					}
				}
			}
		}
	case *PackedEnumStoreType:
		return t != nil && (contains(t.Enum) || contains(t.State))
	}
	return false
}
