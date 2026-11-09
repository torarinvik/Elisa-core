package semantic

import (
	"elisacore/src/ast"
)

// globalPathLoadsMutableBorrow reports whether accessing this global-rooted path loads a
// mutable reference or view from storage. A direct field/index projection into a global
// aggregate is address formation, but projecting through a stored borrow reaches mutable state
// even when the global binding itself is immutable.
func (a *Analyzer) globalPathLoadsMutableBorrow(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.Ident:
		sym, ok := a.globalStorageRoot(n)
		return ok && sym.Kind == SymbolGlobal && !sym.Mutable && mutableBorrowStorageType(sym.Type)
	case *ast.FieldExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.IndexExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.SliceExpr:
		return a.globalPathObjectLoadsMutableBorrow(n.Object)
	case *ast.ParenExpr:
		return a.globalPathLoadsMutableBorrow(n.Inner)
	case *ast.AddrOfExpr:
		return a.globalPathLoadsMutableBorrow(n.Operand)
	case *ast.CastExpr:
		if operand, addressFormed, ok := a.globalStorageCastOperand(n); ok {
			if addressFormed {
				return a.globalPathLoadsMutableBorrow(operand)
			}
			return mutableBorrowStorageType(a.exprType(n.Operand)) || a.globalPathLoadsMutableBorrow(operand)
		}
		return false
	default:
		return false
	}
}

// globalPathObjectLoadsMutableBorrow distinguishes a reference loaded from
// storage from one formed by taking the address of a place. Projection through
// the former reads the stored capability; projection through the latter only
// forms an address to the existing place.
func (a *Analyzer) globalPathObjectLoadsMutableBorrow(expr ast.Expr) bool {
	place := expr
	for {
		switch n := place.(type) {
		case *ast.ParenExpr:
			place = n.Inner
		case *ast.AddrOfExpr:
			return a.globalPathLoadsMutableBorrow(n.Operand)
		case *ast.CastExpr:
			if operand, addressFormed, ok := a.globalStorageCastOperand(n); ok && addressFormed {
				return a.globalPathLoadsMutableBorrow(operand)
			}
			return mutableBorrowStorageType(a.exprType(expr)) || a.globalPathLoadsMutableBorrow(expr)
		default:
			return mutableBorrowStorageType(a.exprType(expr)) || a.globalPathLoadsMutableBorrow(expr)
		}
	}
}

// Calls through mutable references and mutating collection receivers can write
// global storage even when the syntax contains no assignment statement.
func (a *Analyzer) mutableGlobalCallWriteRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	add := func(expr ast.Expr) {
		if _, ok := a.mutableGlobalBorrowStorageRoot(expr); ok {
			refs = append(refs, globalWriteRefs(expr.Pos())...)
			return
		}
		for {
			switch n := expr.(type) {
			case *ast.AddrOfExpr:
				expr = n.Operand
			case *ast.CastExpr:
				expr = n.Operand
			case *ast.ParenExpr:
				expr = n.Inner
			default:
				if _, ok := a.mutableGlobalBorrowStorageRoot(expr); ok {
					refs = append(refs, globalWriteRefs(expr.Pos())...)
				}
				return
			}
		}
	}
	if receiver, ok := a.mutatingBuiltinMethodReceiver(call); ok {
		add(receiver)
	}
	if ft, ok := a.exprType(call.Func).(*FuncType); ok {
		args := call.Args
		if aligned, ok := a.callAlignedAliasArgs[call]; ok {
			args = aligned
		}
		for i, arg := range args {
			if i >= len(ft.Params) {
				break
			}
			if a.hasMutableBorrowCapability(ft.Params[i]) {
				refs = append(refs, a.mutableGlobalBorrowTransferRefs(arg, ft.Params[i])...)
			}
		}
	}
	return canonicalizePermissionRefs(refs)
}

func globalReferenceStorageExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.ParenExpr:
			expr = n.Inner
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutableGlobalReturnedRefRefs(value ast.Expr, returnType Type) []ast.PermissionRef {
	return a.mutableGlobalBorrowTransferRefs(value, returnType)
}

func (a *Analyzer) mandatoryGlobalPermissionRefs(fn *FuncType) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	a.observePermissionFunction(fn)
	refs := append([]ast.PermissionRef(nil), fn.MutableGlobalPermissionRefs...)
	for _, name := range fn.FunctionReturnSourceNames {
		if factory := a.functionTypes[name]; factory != nil {
			a.observePermissionFunction(factory)
			refs = append(refs, factory.ReturnedFunctionGlobalPermissionRefs...)
		}
	}
	for _, name := range fn.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil {
			a.observePermissionFunction(source)
			refs = append(refs, source.MutableGlobalPermissionRefs...)
		}
	}
	// Generic/function-value copies may predate the effect fixpoint. Resolve the
	// named declaration's final mutable row instead of trusting that stale copy.
	if sym, _, ok := a.lookupVisibleGlobal(fn.Name); ok {
		if declared, ok := sym.Type.(*FuncType); ok {
			a.observePermissionFunction(declared)
			refs = append(refs, declared.MutableGlobalPermissionRefs...)
		}
	}
	for _, ref := range fn.DeclaredPermissionRefs {
		if isGlobalPermissionRef(ref) {
			refs = append(refs, ref)
		}
	}
	return canonicalizePermissionRefs(refs)
}

// Handing a mutable-global callback to a higher-order callee transfers the
// ability to perform its accesses, just as handing out a writable reference does.
func (a *Analyzer) mutableGlobalCallbackRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	for _, arg := range call.LoweredArgs() {
		if callback, ok := a.exprType(arg).(*FuncType); ok {
			refs = append(refs, a.mandatoryGlobalPermissionRefs(callback)...)
		}
	}
	return canonicalizePermissionRefs(refs)
}

// Returned callback capabilities are separate from execution effects: producing
// a callback is pure, but invoking it must retain its concrete member row.
func (a *Analyzer) effectiveFunctionPermissionRefs(fn *FuncType) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	a.observePermissionFunction(fn)
	refs := append([]ast.PermissionRef(nil), functionPermissionRefs(fn)...)
	for _, name := range fn.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil {
			a.observePermissionFunction(source)
			refs = append(refs, functionPermissionRefs(source)...)
		}
	}
	if source := a.functionTypes[fn.Name]; source != nil {
		a.observePermissionFunction(source)
		refs = append(refs, functionPermissionRefs(source)...)
	}
	for _, name := range fn.FunctionReturnSourceNames {
		if factory := a.functionTypes[name]; factory != nil {
			a.observePermissionFunction(factory)
			refs = append(refs, factory.ReturnedFunctionPermissionRefs...)
		}
	}
	return canonicalizePermissionRefs(refs)
}
