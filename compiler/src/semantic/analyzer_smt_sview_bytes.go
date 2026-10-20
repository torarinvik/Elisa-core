package semantic

import "elisacore/src/ast"

// stableSviewByteIndex models byte values read from an immutable, write-free view
// parameter. The runtime's data-field spelling is a raw u8 load; the ordinary
// spelling also returns a byte (or zero for an invalid request).
func (tr *smtTranslator) stableSviewByteIndex(read *ast.IndexExpr, env map[string]string) (string, bool) {
	if term, ok := tr.stableContainerSViewByteIndex(read, env); ok {
		return term, true
	}
	view, raw, ok := tr.stableSviewByteIndexRoot(read, env)
	if !ok {
		return "", false
	}
	index, ok := tr.termEnv(read.Index, env)
	if !ok {
		return "", false
	}
	return tr.stableSviewByteIndexValue(read, view, raw, index, env)
}

// stableContainerSViewByteIndex reads bytes from `views[k][i]` when the selected
// view belongs to a stable darray and the current function cannot write memory.
// It uses the same (base, index) element theory as indexed SView call binding,
// so the caller's byte read agrees with the callee formal's normalized window.
func (tr *smtTranslator) stableContainerSViewByteIndex(read *ast.IndexExpr, env map[string]string) (string, bool) {
	if tr == nil || tr.a == nil || read == nil || read.Index2 != nil || read.Fallback != nil ||
		read.LegacyElseFallback || read.AsBuiltinDictGet != nil || read.AsSpecialize != nil ||
		read.AsOverlayCall != nil || tr.calleeBodyDepth != 0 ||
		tr.a.currentFuncDecl == nil || !tr.a.sviewFrameWriteFreeGhostReads(tr.a.currentFuncDecl) {
		return "", false
	}
	view := stripOptimizationParens(read.Object)
	raw := false
	if data, ok := view.(*ast.FieldExpr); ok && data != nil && data.Field == "data" {
		view = stripOptimizationParens(data.Object)
		raw = true
	}
	base, containerIndex, ok := tr.containerViewIndex(view, env)
	if !ok {
		return "", false
	}
	backing, offset, length := tr.containerViewElement(base, containerIndex)
	index, ok := tr.termEnv(read.Index, env)
	if !ok {
		return "", false
	}
	array := backing
	if !raw {
		array, ok = tr.sviewWindowArrayTerm(backing, offset, length)
		if !ok {
			return "", false
		}
	} else {
		index = "(+ " + offset + " " + index + ")"
	}
	value := "(select " + array + " " + index + ")"
	// A byte load is in [0, 256), independent of whether it came through the
	// checked view index or its raw backing-data projection.
	if !smtTermMentionsQuantifierBinder(value) && !tr.byteReadBounds[value] {
		if tr.byteReadBounds == nil {
			tr.byteReadBounds = make(map[string]bool)
		}
		tr.byteReadBounds[value] = true
		tr.auxDecls = append(tr.auxDecls, "(assert (and (>= "+value+" 0) (< "+value+" 256)))\n")
	}
	return value, true
}

// Keep frame authorization separate from term emission so saved Boolean initializers can
// validate a byte read without translating its quantifier binder outside the binder's scope.
func (tr *smtTranslator) stableSviewByteIndexRoot(read *ast.IndexExpr, env map[string]string) (*ast.Ident, bool, bool) {
	if read == nil || read.Index2 != nil || read.Fallback != nil || read.LegacyElseFallback || read.AsBuiltinDictGet != nil || read.AsSpecialize != nil || read.AsOverlayCall != nil {
		return nil, false, false
	}
	object := stripOptimizationParens(read.Object)
	raw := false
	if data, ok := object.(*ast.FieldExpr); ok && data != nil && data.Field == "data" {
		object = stripOptimizationParens(data.Object)
		raw = true
	}
	view, ok := object.(*ast.Ident)
	if !ok || view == nil || !tr.immutableSviewArrayParameter(view, env) {
		return nil, false, false
	}
	return view, raw, true
}

func (tr *smtTranslator) stableSviewByteIndexValue(read *ast.IndexExpr, view *ast.Ident, raw bool, index string, env map[string]string) (string, bool) {
	var array string
	var ok bool
	if raw {
		// Keep raw loads separate from checked indexing: outside the view's range,
		// checked indexing returns zero while a raw load has no such guarantee.
		key, ok := tr.fieldProjectionKey(read.Object, env)
		if !ok {
			return "", false
		}
		tr.arrayDecls[key] = true
		array = smtVar(key)
	} else {
		array, ok = tr.arrayTermEnv(view, env)
		if !ok {
			return "", false
		}
	}
	value := "(select " + array + " " + index + ")"
	// A load of u8 followed by widening is zero-extended. This is a type fact,
	// independent of index bounds. Do not hoist a quantified binder out of scope.
	if !smtTermMentionsQuantifierBinder(value) && !tr.byteReadBounds[value] {
		if tr.byteReadBounds == nil {
			tr.byteReadBounds = make(map[string]bool)
		}
		tr.byteReadBounds[value] = true
		tr.auxDecls = append(tr.auxDecls, "(assert (and (>= "+value+" 0) (< "+value+" 256)))\n")
	}
	return value, true
}
