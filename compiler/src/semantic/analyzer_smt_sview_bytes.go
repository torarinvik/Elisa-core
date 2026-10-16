package semantic

import "elisacore/src/ast"

// stableSviewByteIndex models byte values read from an immutable, write-free view
// parameter. The runtime's data-field spelling is a raw u8 load; the ordinary
// spelling also returns a byte (or zero for an invalid request).
func (tr *smtTranslator) stableSviewByteIndex(read *ast.IndexExpr, env map[string]string) (string, bool) {
	if read == nil || read.Index2 != nil || read.Fallback != nil || read.LegacyElseFallback || read.AsBuiltinDictGet != nil || read.AsSpecialize != nil || read.AsOverlayCall != nil {
		return "", false
	}
	object := stripOptimizationParens(read.Object)
	raw := false
	if data, ok := object.(*ast.FieldExpr); ok && data != nil && data.Field == "data" {
		object = stripOptimizationParens(data.Object)
		raw = true
	}
	view, ok := object.(*ast.Ident)
	if !ok || view == nil || !tr.immutableSviewArrayParameter(view, env) {
		return "", false
	}
	index, ok := tr.termEnv(read.Index, env)
	if !ok {
		return "", false
	}
	var array string
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
