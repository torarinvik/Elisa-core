package semantic

import "elisacore/src/ast"

// A statically selected, analyzed method on a concrete record has an actual
// implementation. Route it through the ordinary guarded call-equation model;
// abstract protocol receivers keep their existing uninterpreted predicate model.
func (tr *smtTranslator) concreteStaticBoolMethodCall(call *ast.CallExpr) (*ast.CallExpr, bool) {
	if tr == nil || tr.a == nil || call == nil || call.Safe || call.SafeReceiver != nil || call.HasArgForward {
		return nil, false
	}
	selector, ok := call.Func.(*ast.FieldExpr)
	if !ok || selector == nil {
		return nil, false
	}
	ref := tr.a.interfaceMethodRefs[selector]
	if ref == nil || ref.Receiver != nil || len(ref.ExplicitTypeArgs) != 0 {
		return nil, false
	}
	if ref.ImplKey == "" {
		return nil, false
	}
	impl := tr.a.staticImpls[ref.ImplKey]
	if impl == nil || impl.InterfaceName != ref.InterfaceName || len(impl.TypeParams) != 0 {
		return nil, false
	}
	record, concrete := StripAggregateStateType(impl.Receiver).(*StructType)
	if !concrete || record == nil || len(record.TypeParams) != 0 {
		return nil, false
	}
	symbol := impl.Methods[ref.MethodName]
	if symbol == nil || tr.a.exprTypes[selector] != symbol.Type {
		return nil, false
	}
	decl, declared := symbol.Node.(*ast.FuncDecl)
	fn, typed := symbol.Type.(*FuncType)
	if !declared || decl == nil || !typed || fn == nil || !fn.ReturnBorrowedOwnerRefsKnown || !IsBoolType(fn.Return) ||
		len(decl.TypeParams) != 0 || len(decl.GenericParams) != 0 || len(decl.RegionParams) != 0 || len(decl.PermissionParams) != 0 ||
		decl.Variadic || len(proofCallArgs(call)) != len(decl.Params) {
		return nil, false
	}
	// The synthetic identifier must resolve to the exact selected impl symbol,
	// never a same-spelled caller-local callback or overload.
	scope := tr.a.currentScope
	if scope == nil {
		scope = tr.a.globalScope
	}
	if scope == nil {
		return nil, false
	}
	resolved, found := scope.Lookup(symbol.Name)
	if !found || resolved != symbol {
		return nil, false
	}
	rewritten := *call
	rewritten.Func = &ast.Ident{Position: selector.Position, Name: symbol.Name}
	return &rewritten, true
}
