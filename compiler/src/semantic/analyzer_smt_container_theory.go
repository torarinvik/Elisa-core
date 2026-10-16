package semantic

import (
	"elisacore/src/ast"
)

// Container theory: an SMT model of a darray VALUE that is fixed for the whole query.
//
// A container value is named by a BASE symbol. Its length is `base$len` (the same symbol the
// ordinary length model uses), and its elements are reached through per-base uninterpreted
// functions of the index, one per element component the element type exposes (for a view element:
// its byte array, start offset and length — see containerViewElement). Equal bases denote the same
// value, so every read through one base agrees.
//
// A base is STABLE only when the binding it names can never change during the function: an
// immutable local, or an immutable parameter passed by value or by immutable reference, read in
// the function's own frame (never a callee frame's unbound name). A callee formal bound to a stable
// base is stable through the binding. Unstable containers keep the length-only model and no element
// reads, so a container mutated between two reads is never given one value.
//
// Nothing relates an element read to the length: a read at an index past `count` is an
// unconstrained element, never a proof that the index is in bounds. The model is deliberately
// generic (an element theory over an index sort) so further element types or index-dependent
// refinements attach to the same base.

// containerBase returns the stable base symbol of a darray-valued expression.
func (tr *smtTranslator) containerBase(expr ast.Expr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil {
		return "", false
	}
	e := stripOptimizationParens(expr)
	id, ok := e.(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if bound, isBound := env[id.Name]; isBound {
		if tr.stableContainers[bound] {
			return bound, true
		}
		return "", false
	}
	if tr.calleeBodyDepth != 0 || tr.a.currentScope == nil {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	if !isDArrayTypeMaybeRef(sym.Type) {
		return "", false
	}
	if ref, isRef := StripAggregateStateType(sym.Type).(*RefType); isRef && (sym.Kind != SymbolParam || ref.Mutable || ref.Linear) {
		return "", false
	}
	base := smtVar(id.Name)
	tr.markStableContainer(base)
	return base, true
}

func (tr *smtTranslator) markStableContainer(base string) {
	if tr.stableContainers == nil {
		tr.stableContainers = map[string]bool{}
	}
	tr.stableContainers[base] = true
	tr.lenDecls[base+smtArrayLenSuffix] = true
}

// bindContainerArg binds a callee formal to a stable container argument. The implicit borrow that
// passes a container to an IMMUTABLE reference formal is seen through: the callee can only read
// the operand's current value.
func (tr *smtTranslator) bindContainerArg(env map[string]string, name string, arg ast.Expr, env0 map[string]string) bool {
	e := stripOptimizationParens(arg)
	if addr, isAddr := e.(*ast.AddrOfExpr); isAddr && addr != nil {
		if !tr.bindingFormalImmutableRef(name) {
			return false
		}
		e = addr.Operand
	}
	base, ok := tr.containerBase(e, env0)
	if !ok {
		return false
	}
	env[name] = base
	return true
}

// bindingFormalImmutableRef reports whether the formal being bound (of tr.bindingCallee) is an
// immutable reference.
func (tr *smtTranslator) bindingFormalImmutableRef(name string) bool {
	decl := tr.bindingCallee
	if decl == nil {
		return false
	}
	sym, ok := tr.a.symbolForFuncDecl(decl)
	if !ok || sym == nil {
		return false
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok || fn == nil || len(fn.Params) != len(decl.Params) {
		return false
	}
	for i, p := range decl.Params {
		if p.Name != name {
			continue
		}
		ref, isRef := fn.Params[i].(*RefType)
		return isRef && !ref.Mutable && !ref.Linear
	}
	return false
}

// containerArgType is the darray type of an argument (seeing through an implicit borrow), or nil.
func (a *Analyzer) containerArgType(arg ast.Expr) Type {
	e := stripOptimizationParens(arg)
	if t := a.exprTypes[e]; t != nil {
		if isDArrayTypeMaybeRef(t) {
			return t
		}
		return nil
	}
	if addr, isAddr := e.(*ast.AddrOfExpr); isAddr && addr != nil {
		e = stripOptimizationParens(addr.Operand)
	}
	if t := a.exprTypes[e]; t != nil {
		if isDArrayTypeMaybeRef(t) {
			return t
		}
		return nil
	}
	if id, ok := e.(*ast.Ident); ok && id != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(id.Name); found && sym != nil && isDArrayTypeMaybeRef(sym.Type) {
			return sym.Type
		}
	}
	return nil
}
