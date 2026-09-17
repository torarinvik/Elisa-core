package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// nameIsRuntimeBinding reports whether a bare name, read by the constant evaluator, resolves to a
// runtime binding (parameter, local, loop or pattern variable) rather than to a compile-time value.
//
// The const tables are global: they know nothing of function scopes. Without this test a binding
// that SHADOWS a const was read as the const, silently:
//
//	const N = 3
//	def f(N: i64) -> i64:
//	    return xs[N]        # bounds check elided -- the index "is" 3
//
// and likewise a refinement proven from the const's value, a `static if` on a parameter taking the
// const's branch, and `(A if true else B).len` folding the global A's length.
//
// A const-eval scope value or a const generic parameter is compile-time, and a local scope never
// holds a SymbolConst. Inside a static callee's body the caller's locals are not visible at all:
// the callee's own names live in constEvalScopes.
func (a *Analyzer) nameIsRuntimeBinding(name string) bool {
	if a == nil || a.staticCallDepth > 0 {
		return false
	}
	if _, ok := a.lookupConstEvalValue(name); ok {
		return false
	}
	if _, ok := a.lookupConstParam(name); ok {
		return false
	}
	sym, ok := a.lookupLocalSymbol(name)
	return ok && sym != nil && sym.Kind != SymbolConst
}

// ConstStringLen folds `.len` on a compile-time string whose receiver has static type
// receiverType (nil when unknown).
//
// A `u8&` string's `.len` is its byte count; a cstr's `.len` lowers to strlen and stops at the
// first NUL. The two agree unless the bytes hold an embedded NUL -- `const G: cstr = "ab\0cd"` is
// 2 at runtime -- and a fold that ignored the type made `const N = G.len` 5, proved refinements
// the running program broke, and took the wrong `static if` branch. With a NUL byte and no known
// type the fold is refused; a typed pass folds it.
func ConstStringLen(value string, receiverType Type) (int64, bool) {
	nul := strings.IndexByte(value, 0)
	if nul < 0 {
		return int64(len(value)), true
	}
	if receiverType == nil || IsInvalidType(receiverType) {
		return 0, false
	}
	if isCStrLenReceiver(receiverType) {
		return int64(nul), true
	}
	return int64(len(value)), true
}

func isCStrLenReceiver(t Type) bool {
	if _, ok := t.(*CStrType); ok {
		return true
	}
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return false
	}
	_, ok = ref.Elem.(*CStrType)
	return ok
}

// constLenReceiverType is the static type of a `.len` receiver being const-folded: its analyzed
// type when it has one, else what the receiver's shape settles on its own (a literal is a `u8&`
// string; a const has its declared type). nil means unknown.
func (a *Analyzer) constLenReceiverType(expr ast.Expr) Type {
	if t, ok := a.exprTypes[expr]; ok && t != nil && !IsInvalidType(t) {
		return t
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		if n != nil {
			return a.constLenReceiverType(n.Inner)
		}
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	case *ast.Ident:
		if n == nil || a.nameIsRuntimeBinding(n.Name) {
			return nil
		}
		if _, ok := a.lookupConstEvalValue(n.Name); ok {
			return nil
		}
		if _, ok := a.lookupConstParam(n.Name); ok {
			return nil
		}
		if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym != nil && sym.Kind == SymbolConst && sym.Type != nil && !IsInvalidType(sym.Type) {
			return sym.Type
		}
	}
	return nil
}
