package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeSpecializeExpr(expr *ast.SpecializeExpr) Type {
	if expr == nil || expr.Operand == nil {
		return invalidType
	}
	ident, ok := expr.Operand.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "specialize expects a named generic function")
		a.analyzeExpr(expr.Operand)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	sym, _, ok := a.lookupVisibleGlobal(ident.Name)
	if !ok {
		a.errorf(expr.Pos(), "undefined generic function %q", ident.Name)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "specialize expects a function, got %s", sym.Type)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	params := genericParamsForFuncType(fnType)
	if len(params) == 0 {
		a.errorf(expr.Pos(), "function %q is not generic", ident.Name)
		for _, arg := range expr.TypeArgs {
			a.resolveType(arg)
		}
		return invalidType
	}
	if len(expr.TypeArgs) != len(params) {
		a.errorf(expr.Pos(), "function %q expects %d %s, got %d", ident.Name, len(params), genericArgLabel(params), len(expr.TypeArgs))
	}
	bindings := make(map[string]Type, len(params))
	limit := len(expr.TypeArgs)
	if len(params) < limit {
		limit = len(params)
	}
	for i := 0; i < limit; i++ {
		bindings[params[i].Name] = a.resolveGenericArgForParam(expr.TypeArgs[i], params[i])
	}
	for i := limit; i < len(expr.TypeArgs); i++ {
		a.resolveType(expr.TypeArgs[i])
	}
	specialized, _ := a.substituteType(fnType, bindings, nil, nil, nil).(*FuncType)
	if specialized == nil {
		return invalidType
	}
	specialized.TypeParams = nil
	specialized.GenericParams = nil
	return specialized
}
func promoteWritableRefType(t Type, mutable bool) Type {
	if !mutable {
		return t
	}
	ref, ok := t.(*RefType)
	if !ok || ref == nil || ref.Mutable {
		return t
	}
	cloned := cloneRefType(ref)
	cloned.Mutable = true
	return cloned
}
func (a *Analyzer) fieldExprProvidesWritableRef(expr *ast.FieldExpr) bool {
	if expr == nil {
		return false
	}
	if expr.Safe {
		return false
	}
	field, ok := a.lookupFieldNoError(a.analyzeExpr(expr.Object), expr.Field)
	if !ok {
		return false
	}
	if ref, ok := field.Type.(*RefType); ok {
		return field.Mutable || ref.Mutable
	}
	return false
}
func (a *Analyzer) exprCanYieldWritableRef(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
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
		if ref, isRef := sym.Type.(*RefType); isRef {
			return sym.Mutable || ref.Mutable
		}
		return sym.Mutable
	case *ast.FieldExpr:
		if n.Safe {
			return false
		}
		field, ok := a.lookupFieldNoError(a.analyzeExpr(n.Object), n.Field)
		if !ok || !field.Mutable {
			return false
		}
		return a.mutationPathWritable(n.Object)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return false
		}
		if facts, ok := a.exprFacts[n.Object]; ok && facts.ReadOnly {
			return false
		}
		return a.mutationPathWritable(n.Object)
	default:
		return false
	}
}

// borrowPlaceRootsInConst reports whether the place being borrowed resolves to
// a top-level const (read-only static storage), walking through index/field/
// paren accessors to the root identifier. Used to reject mutable borrows of a
// const, which would otherwise point into rodata.
func (a *Analyzer) borrowPlaceRootsInConst(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.borrowPlaceRootsInConst(n.Inner)
	case *ast.IndexExpr:
		return a.borrowPlaceRootsInConst(n.Object)
	case *ast.FieldExpr:
		return a.borrowPlaceRootsInConst(n.Object)
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			sym, _, ok = a.lookupVisibleGlobal(n.Name)
		}
		return ok && sym != nil && sym.Kind == SymbolConst
	default:
		return false
	}
}

func (a *Analyzer) exprCanYieldAddressableValue(expr ast.Expr) bool {
	stripped := stripMutationTargetExpr(expr)
	if stripped == nil {
		return false
	}
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
			sym, _, ok = a.lookupVisibleGlobal(n.Name)
		}
		if !ok || sym == nil {
			return false
		}
		switch sym.Kind {
		case SymbolLocal, SymbolParam, SymbolGlobal, SymbolConst, SymbolExternVar:
			return true
		default:
			return false
		}
	case *ast.FieldExpr:
		if _, ok := a.lookupFieldNoError(a.analyzeExpr(n.Object), n.Field); !ok {
			return false
		}
		if _, ok := a.exprTypes[n.Object].(*RefType); ok {
			return true
		}
		if _, ok := a.analyzeExpr(n.Object).(*RefType); ok {
			return true
		}
		return a.exprCanYieldAddressableValue(n.Object)
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return false
		}
		objType := a.analyzeExpr(n.Object)
		if _, ok := objType.(*RefType); ok {
			return true
		}
		switch StripAggregateStateType(objType).(type) {
		case *ArrayType, *DArrayType, *ViewType:
			return a.exprCanYieldAddressableValue(n.Object)
		default:
			return false
		}
	default:
		return false
	}
}
