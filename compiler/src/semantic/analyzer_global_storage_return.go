package semantic

import "elisacore/src/ast"

// Returning a GLOBAL's owning container by value (`return store.items`, `return g_names`,
// `return g_rows[0]`) hands the caller a header that shares the global's heap buffer: a
// darray/dstr/dict/set copy is a shallow {data, len, cap} copy. The global is program-lifetime
// and stays growable, so its next push/extend reallocates and frees the buffer the caller's
// copy still points at — a use-after-free that no region tie describes, because the value
// lives in no caller-visible region.
//
// This is the global-rooted analogue of the parameter rule (a parameter's storage cannot be
// returned by value with a region-less type). The fix sites are a reference return
// (`-> T&` with `return &g`) or an explicit clone into a caller-owned region.
//
// Locals initialised or assigned from such a place (`x: darray[T] = g; return x`) carry the
// same alias; they are recorded flow-insensitively (a later reassignment never clears the
// mark), which can only over-report.

const globalStorageReturnMessage = "value backed by global %q cannot be returned by value with a region-less type (the copy shares the global's storage, which its next growth frees); return a reference (-> T& with return &...) or clone it into a caller-owned region (clone[darray[T] @r](...))"

// typeOwnsGrowableStorage reports whether a by-value copy of t shares a growable heap buffer
// with its source: an owning container (darray/dstr/dict/set), an SoA store, or a by-value
// aggregate holding one. Borrowed views/refs are not owners and are excluded.
func typeOwnsGrowableStorage(t Type) bool {
	return typeOwnsGrowableStorageRec(t, map[Type]bool{})
}

func typeOwnsGrowableStorageRec(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *DArrayType, *DictType, *SetType:
		return true
	case *TupleType:
		for _, field := range tt.Fields {
			if typeOwnsGrowableStorageRec(field.Type, seen) {
				return true
			}
		}
	case *OptionalType:
		return typeOwnsGrowableStorageRec(tt.Value, seen)
	case *ErrorUnionType:
		return typeOwnsGrowableStorageRec(tt.Value, seen)
	case *ArrayType:
		return typeOwnsGrowableStorageRec(tt.Elem, seen)
	case *AggregateStateType:
		if tt != nil {
			return typeOwnsGrowableStorageRec(tt.Base, seen)
		}
	case *StructType:
		if tt == nil {
			return false
		}
		if tt.Store {
			return true
		}
		for _, f := range tt.Fields {
			if typeOwnsGrowableStorageRec(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

// returnTypeIsRegionTied reports whether the declared return type names an allocation region
// at its top level (`-> darray[T] @r`). Those returns are governed by the region-escape rules.
func returnTypeIsRegionTied(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *DArrayType:
		return tt.Region != ""
	case *DictType:
		return tt.Region != ""
	case *SetType:
		return tt.Region != ""
	case *GenericInstanceType:
		return tt.Region != ""
	}
	return false
}

// globalStorageAliasRoot returns the global whose storage a by-value container expression
// shares: a global-rooted place (field/index chain, parens and casts allowed), or a local
// recorded as aliasing one.
func (a *Analyzer) globalStorageAliasRoot(expr ast.Expr) (string, bool) {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
			continue
		case *ast.CastExpr:
			expr = n.Operand
			continue
		}
		break
	}
	if _, isSlice := expr.(*ast.SliceExpr); isSlice {
		return "", false
	}
	if sym, ok := a.globalStorageRoot(expr); ok && sym != nil {
		return sym.Name, true
	}
	if ident, ok := expr.(*ast.Ident); ok && a.currentScope != nil && a.globalStorageAliasLocals != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found && sym != nil {
			if root, marked := a.globalStorageAliasLocals[sym]; marked {
				return root, true
			}
		}
	}
	return "", false
}

// noteGlobalStorageAliasLocal records that local sym now shares a global's container storage.
func (a *Analyzer) noteGlobalStorageAliasLocal(sym *Symbol, value ast.Expr, bindingType Type) {
	if a == nil || sym == nil || value == nil || isGlobalStorageSymbol(sym) {
		return
	}
	if _, isRef := StripAggregateStateType(bindingType).(*RefType); isRef || !typeOwnsGrowableStorage(bindingType) {
		return
	}
	root, ok := a.globalStorageAliasRoot(value)
	if !ok {
		return
	}
	if a.globalStorageAliasLocals == nil {
		a.globalStorageAliasLocals = map[*Symbol]string{}
	}
	a.globalStorageAliasLocals[sym] = root
}

// noteGlobalStorageAliasAssignment is noteGlobalStorageAliasLocal for `x = g` / `x <- g`.
func (a *Analyzer) noteGlobalStorageAliasAssignment(target, value ast.Expr, targetType Type) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	if sym, found := a.currentScope.Lookup(ident.Name); found {
		a.noteGlobalStorageAliasLocal(sym, value, targetType)
	}
}

// checkGlobalStorageReturnEscape rejects returning a global's owning container by value.
func (a *Analyzer) checkGlobalStorageReturnEscape(value ast.Expr) {
	if a == nil || value == nil || a.currentReturn == nil {
		return
	}
	returnType := a.currentReturn
	if union, ok := returnType.(*ErrorUnionType); ok && union != nil {
		returnType = union.Value
	}
	if _, isRef := StripAggregateStateType(returnType).(*RefType); isRef {
		return
	}
	if !typeOwnsGrowableStorage(returnType) || returnTypeIsRegionTied(returnType) {
		return
	}
	valueType := a.exprTypes[value]
	if valueType != nil {
		if _, isRef := StripAggregateStateType(valueType).(*RefType); isRef {
			return
		}
		if !typeOwnsGrowableStorage(valueType) {
			return
		}
	}
	root, ok := a.globalStorageAliasRoot(value)
	if !ok {
		return
	}
	a.errorf(value.Pos(), globalStorageReturnMessage, root)
}
