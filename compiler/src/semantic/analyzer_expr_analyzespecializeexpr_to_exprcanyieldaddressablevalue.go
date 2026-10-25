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
	sym, canonical, ok := a.lookupVisibleGlobal(ident.Name)
	if ok && a.resolvedValueNames != nil && canonical != "" && canonical != ident.Name {
		// The backend finds a module-scoped callee by this name, as for a plain call.
		a.resolvedValueNames[ident] = canonical
	}
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
	resolvedArgs := make([]Type, len(params))
	limit := len(expr.TypeArgs)
	if len(params) < limit {
		limit = len(params)
	}
	for i := 0; i < limit; i++ {
		resolved := a.resolveGenericArgForParam(expr.TypeArgs[i], params[i])
		bindings[params[i].Name] = resolved
		resolvedArgs[i] = resolved
	}
	for i := limit; i < len(expr.TypeArgs); i++ {
		a.resolveType(expr.TypeArgs[i])
	}
	regionBindings := regionBindingsForParams(params, resolvedArgs)
	specialized, _ := a.substituteType(fnType, bindings, nil, regionBindings, nil).(*FuncType)
	if specialized == nil {
		return invalidType
	}
	specialized.TypeParams = nil
	remainingRegions := make([]string, 0, len(specialized.RegionParams))
	for _, name := range specialized.RegionParams {
		if _, explicitlyBound := regionBindings[name]; !explicitlyBound {
			remainingRegions = append(remainingRegions, name)
		}
	}
	specialized.RegionParams = remainingRegions
	specialized.GenericParams = nil
	return specialized
}

// symbolPromotesWritableRef reports whether reading sym promotes its read-only reference type to a
// writable one because the BINDING is `mutable`. A binding's `mutable` makes its slot rebindable;
// writing through a reference is its type's capability (`mutable T&`). Only bindings whose type the
// program does not state keep the promotion (a lock guard, an extern variable). A local's declaration
// decides once (analyzer_flow.go), a capture carries its source's decision in its type, and a
// parameter or global never promotes: each let a read-only referent be written, and passing "abc"
// through `mutable p: u8&` to a writer faulted (SIGBUS).
func symbolPromotesWritableRef(sym *Symbol) bool {
	if sym == nil || !sym.Mutable || sym.BindingMutabilityExplicit {
		return false
	}
	switch sym.Kind {
	case SymbolParam, SymbolGlobal:
		return false
	case SymbolLocal:
		switch sym.Node.(type) {
		case *ast.VarDeclStmt, *ast.LambdaExpr:
			return false
		}
	}
	return true
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
	// Reuse the object's already-computed type when present. This predicate is called ALONGSIDE
	// analyzeFieldExpr on the same node (both in the FieldExpr case of analyzeExpr, line ~295), and each
	// re-analyzing the object independently makes a nested field chain `a.b.b.b…` cost 2^depth — a hang
	// the mutating fuzzer surfaced. analyzeExpr caches the object's type in exprTypes on the first pass, so
	// read it back instead of recomputing; only analyze afresh when it is genuinely unset (identical
	// result, minus the redundant re-walk and its duplicate diagnostics).
	objType := a.exprType(expr.Object)
	if objType == nil {
		objType = a.analyzeExpr(expr.Object)
	}
	field, ok := a.lookupFieldNoError(objType, expr.Field)
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
		if a.readOnlyViewIndexSource(n.Object) {
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
		if _, ok := a.exprType(n.Object).(*RefType); ok {
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
