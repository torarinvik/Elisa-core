package semantic

import (
	"encoding/hex"
	"strings"

	"elisacore/src/ast"
)

const smtIndexedSViewArgPrefix = "\x00smt-indexed-sview:"

func smtIndexedSViewArgKey(name, part string) string {
	return smtIndexedSViewArgPrefix + part + ":" + name
}

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
	if field, ok := e.(*ast.FieldExpr); ok {
		return tr.recordFieldContainerBase(field, env)
	}
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

// containerElementType is the element type of a darray-typed container expression, or nil.
func (tr *smtTranslator) containerElementType(expr ast.Expr) Type {
	t := tr.a.containerArgType(expr)
	if t == nil {
		if typed := tr.placeStaticType(expr); typed != nil && isDArrayTypeMaybeRef(typed) {
			t = typed
		}
	}
	if t == nil {
		return nil
	}
	if r, ok := t.(*RefType); ok {
		t = r.Elem
	}
	if d, ok := StripAggregateStateType(t).(*DArrayType); ok && d != nil {
		return d.Elem
	}
	return nil
}

// placeStaticType types an untyped place `root.f.g`. Inside an inlined callee body the root is
// one of the callee's formals, typed from its signature; the caller's scope never names it.
func (tr *smtTranslator) placeStaticType(expr ast.Expr) Type {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if t := tr.a.exprTypes[n]; t != nil {
			return t
		}
		if tr.calleeBodyDepth != 0 {
			decl := tr.calleeBodyDecl
			if decl == nil {
				return nil
			}
			sym, ok := tr.a.symbolForFuncDecl(decl)
			if !ok || sym == nil {
				return nil
			}
			fn, ok := sym.Type.(*FuncType)
			if !ok || fn == nil || len(fn.Params) != len(decl.Params) {
				return nil
			}
			for i, param := range decl.Params {
				if param.Name == n.Name {
					return fn.Params[i]
				}
			}
			return nil
		}
		if tr.a.currentScope != nil {
			if sym, found := tr.a.currentScope.Lookup(n.Name); found && sym != nil {
				return sym.Type
			}
		}
	case *ast.IndexExpr:
		if t := tr.a.exprTypes[n]; t != nil {
			return t
		}
		if !validContainerViewIndex(n) {
			return nil
		}
		return tr.containerElementType(n.Object)
	case *ast.FieldExpr:
		if t := tr.a.exprTypes[n]; t != nil {
			return t
		}
		object := tr.placeStaticType(n.Object)
		if object == nil {
			return nil
		}
		if t, ok := tr.a.lookupResolvedFieldType(peelNamedStateRefs(object), n.Field); ok {
			return t
		}
	}
	return nil
}

// containerViewIndex recognizes `xs[k]` on a stable container of views and returns its base and
// index term.
func (tr *smtTranslator) containerViewIndex(expr ast.Expr, env map[string]string) (string, string, bool) {
	e := stripOptimizationParens(expr)
	if idx, ok := e.(*ast.IndexExpr); ok {
		if !validContainerViewIndex(idx) {
			return "", "", false
		}
		elem := tr.containerElementType(idx.Object)
		if elem == nil {
			return "", "", false
		}
		if _, isView := stripRefForBounds(elem).(*SViewType); !isView {
			return "", "", false
		}
		return tr.containerViewIndexTerm(idx, "", env)
	}
	field, ok := e.(*ast.FieldExpr)
	if !ok || field == nil || tr == nil || tr.a == nil || tr.a.currentFuncDecl == nil ||
		!tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) {
		return "", "", false
	}
	root, path := projectionChain(field)
	if id, ok := root.(*ast.Ident); ok && id != nil {
		if value, found := tr.immutableIndexedRecordLocal(id, env); found {
			return tr.containerViewIndex(rerootProjection(field, value, tr.a), env)
		}
	}
	idx, ok := root.(*ast.IndexExpr)
	if !ok || !validContainerViewIndex(idx) || path == "" {
		return "", "", false
	}
	record, ok := StripAggregateStateType(tr.containerElementType(idx.Object)).(*StructType)
	if !ok || record == nil {
		return "", "", false
	}
	var leaf Type = record
	for _, name := range strings.Split(strings.TrimPrefix(path, smtFieldSeparator), smtFieldSeparator) {
		st, ok := StripAggregateStateType(leaf).(*StructType)
		if !ok || st == nil {
			return "", "", false
		}
		field, found := st.Fields[name]
		if !found {
			return "", "", false
		}
		leaf = field.Type
	}
	if _, isView := stripRefForBounds(leaf).(*SViewType); !isView {
		return "", "", false
	}
	return tr.containerViewIndexTerm(idx, path, env)
}

func validContainerViewIndex(idx *ast.IndexExpr) bool {
	return idx != nil && idx.Index2 == nil && idx.Fallback == nil && !idx.LegacyElseFallback &&
		idx.AsSpecialize == nil && idx.AsOverlayCall == nil && idx.AsBuiltinDictGet == nil
}

func (tr *smtTranslator) containerViewIndexTerm(idx *ast.IndexExpr, path string, env map[string]string) (string, string, bool) {
	if !validContainerViewIndex(idx) {
		return "", "", false
	}
	base, ok := tr.containerBase(idx.Object, env)
	if !ok {
		return "", "", false
	}
	if path != "" {
		base += "$field_" + hex.EncodeToString([]byte(path))
	}
	k, ok := tr.termEnv(idx.Index, env)
	if !ok {
		return "", "", false
	}
	return base, k, true
}

// containerViewStringID is the classifier identity of a view stored in a stable
// container element. Keep the selected index as a function argument: turning a
// quantified index into a free constant would make distinct elements spuriously
// share identity, while the UF preserves congruence for repeated reads at the
// same index.
func (tr *smtTranslator) containerViewStringID(base, index string) string {
	name := "|@container_sview_sid_" + hex.EncodeToString([]byte(base)) + "|"
	if tr.ghostUFDecls == nil {
		tr.ghostUFDecls = map[string]bool{}
	}
	if !tr.ghostUFDecls[name] {
		tr.ghostUFDecls[name] = true
		tr.auxDecls = append(tr.auxDecls, "(declare-fun "+name+" (Int) Int)\n")
	}
	return "(" + name + " " + index + ")"
}

// containerViewElement is the view stored at index k of a container: its byte array, start offset
// and length, as per-base uninterpreted functions of the index. A non-binder-dependent length is
// constrained to be non-negative; when k mentions a quantifier binder, that optional fact is
// dropped instead of leaking the binder into a top-level assertion. Nothing relates an element to
// the container length, in particular not that k is in bounds.
func (tr *smtTranslator) containerViewElement(base, k string) (arr, lo, length string) {
	data := "|" + base + "$edata|"
	start := "|" + base + "$elo|"
	size := "|" + base + "$elen|"
	if tr.ghostUFDecls == nil {
		tr.ghostUFDecls = map[string]bool{}
	}
	if !tr.ghostUFDecls[data] {
		tr.ghostUFDecls[data] = true
		tr.auxDecls = append(tr.auxDecls,
			"(declare-fun "+data+" (Int) (Array Int Int))\n",
			"(declare-fun "+start+" (Int) Int)\n",
			"(declare-fun "+size+" (Int) Int)\n")
	}
	length = "(" + size + " " + k + ")"
	// Keep a quantified binder inside its scope. For a binder-dependent view,
	// dropping this optional well-formedness fact is a conservative loss of
	// precision; emitting it as a top-level assertion would leak the binder.
	if !smtTermMentionsQuantifierBinder(length) {
		tr.auxDecls = append(tr.auxDecls, "(assert (>= "+length+" 0))\n")
	}
	return "(" + data + " " + k + ")", "(" + start + " " + k + ")", length
}

// indexedSViewCallTerms keeps a view selected from a stable darray[sview] as
// its backing bytes, offset, and length. These are function arguments rather
// than global view symbols, so a quantifier binder remains inside the term.
func (tr *smtTranslator) indexedSViewCallTerms(expr ast.Expr, env map[string]string) (data, offset, length string, ok bool) {
	if tr == nil || tr.a == nil || tr.a.currentFuncDecl == nil || !tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) {
		return "", "", "", false
	}
	base, index, ok := tr.containerViewIndex(expr, env)
	if !ok {
		return "", "", "", false
	}
	data, offset, length = tr.containerViewElement(base, index)
	return data, offset, length, true
}

func bindSMTIndexedSViewArgTerms(env map[string]string, name, data, offset, length string) {
	if env == nil || name == "" || data == "" || offset == "" || length == "" {
		return
	}
	env[smtIndexedSViewArgKey(name, "data")] = data
	env[smtIndexedSViewArgKey(name, "offset")] = offset
	env[smtIndexedSViewArgKey(name, "length")] = length
}

func smtIndexedSViewArgTermsFromEnv(name string, env map[string]string) (data, offset, length string, ok bool) {
	if env == nil || name == "" {
		return "", "", "", false
	}
	data = env[smtIndexedSViewArgKey(name, "data")]
	offset = env[smtIndexedSViewArgKey(name, "offset")]
	length = env[smtIndexedSViewArgKey(name, "length")]
	return data, offset, length, data != "" && offset != "" && length != ""
}

// indexedSViewCallArray binds a sview read from a stable container as a
// zero-based byte array for a verified read-only call. The container theory
// supplies the element's backing bytes, start offset, and length as functions
// of the same base/index pair used by caller-side reads. Normalize that window
// through sviewWindowArrayTerm so call ensures and caller facts share contents.
func (tr *smtTranslator) indexedSViewCallArray(expr ast.Expr, env map[string]string) (array, length string, ok bool) {
	if tr == nil || tr.a == nil || tr.bindingCallee == nil || tr.a.currentFuncDecl == nil {
		return "", "", false
	}
	// A stable container preserves its view headers. Also require the caller and
	// callee bodies to avoid writes while the modeled byte window is in use; a
	// view header alone does not make its backing bytes immutable.
	if !tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) ||
		!tr.a.sviewFrameCallBodyWriteFree(tr.bindingCallee, len(tr.bindingCallee.Params)) {
		return "", "", false
	}
	base, index, ok := tr.containerViewIndex(expr, env)
	if !ok {
		return "", "", false
	}
	backing, offset, length := tr.containerViewElement(base, index)
	array, ok = tr.sviewWindowArrayTerm(backing, offset, length)
	if !ok {
		return "", "", false
	}
	return array, length, true
}
