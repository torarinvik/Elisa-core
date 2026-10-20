package semantic

import (
	"encoding/hex"
	"strconv"
	"strings"

	"elisacore/src/ast"
)

type smtStructArgLeaf struct {
	path string
	term string
}

// Use the same scalar leaves for callee bindings and call congruence keys.
// A nested value such as BindingId.owner.value must not revert to an opaque
// token merely because its scalar component is one record deeper.
func (tr *smtTranslator) scalarStructArgLeaves(arg ast.Expr, st *StructType, env map[string]string) ([]smtStructArgLeaf, bool) {
	var leaves []smtStructArgLeaf
	var walk func(ast.Expr, *StructType, string) bool
	walk = func(object ast.Expr, record *StructType, prefix string) bool {
		for _, name := range sortedStructFieldNames(record) {
			field := record.Fields[name]
			expr := &ast.FieldExpr{Object: object, Field: name}
			path := prefix + name
			typ := stripRefForBounds(field.Type)
			if nested, ok := StripAggregateStateType(field.Type).(*StructType); ok {
				if !plainFrameRecordType(nested, map[*StructType]bool{}) || !walk(expr, nested, path+smtFieldSeparator) {
					return false
				}
				continue
			}
			var term string
			var ok bool
			if IsBoolType(typ) {
				term, ok = tr.boolTerm(expr, env)
			} else if IsNumericType(typ) && !IsFloatType(typ) {
				term, ok = tr.termEnv(expr, env)
			}
			if !ok {
				return false
			}
			leaves = append(leaves, smtStructArgLeaf{path: path, term: term})
		}
		return true
	}
	ok := walk(arg, st, "")
	return leaves, ok
}

// A record's scalar fields are functions of the container and index, rather
// than of the source location of a read. Keep the index inside the application
// so quantified reads cannot become one binder-independent scalar.
func (tr *smtTranslator) recordElementFieldTerm(n *ast.FieldExpr, env map[string]string, sort string) (string, bool, bool) {
	root, path := projectionChain(n)
	if id, ok := root.(*ast.Ident); ok {
		if value, found := tr.immutableIndexedRecordLocal(id, env); found {
			return tr.recordElementFieldTerm(rerootProjection(n, value, tr.a), env, sort)
		}
	}
	index, ok := root.(*ast.IndexExpr)
	if !ok || index == nil {
		return "", false, false
	}
	element := tr.containerElementType(index.Object)
	if element == nil {
		return "", true, false
	}
	typ, record := StripAggregateStateType(element).(*StructType)
	if !record {
		return "", false, false
	}
	if index.Index2 != nil || index.Fallback != nil || index.LegacyElseFallback ||
		index.AsSpecialize != nil || index.AsOverlayCall != nil || index.AsBuiltinDictGet != nil ||
		(!tr.loopFrameContainerRead(index.Object, env) && !tr.a.sviewFrameWriteFreeGhostReads(tr.a.currentFuncDecl)) {
		return "", true, false
	}
	var leaf Type = typ
	for _, name := range strings.Split(strings.TrimPrefix(path, smtFieldSeparator), smtFieldSeparator) {
		st, ok := StripAggregateStateType(leaf).(*StructType)
		if !ok {
			return "", true, false
		}
		field, found := st.Fields[name]
		if !found {
			return "", true, false
		}
		leaf = field.Type
	}
	// A const-enum field is its integer storage value, as everywhere else in the SMT model.
	if storage, ok := stripRefForBounds(leaf).(*ConstEnumType); ok && storage != nil && storage.Storage != nil {
		leaf = storage.Storage
	}
	signed, bits, numeric := smtIntWidthSign(leaf)
	// A payload-free enum field is its variant index (enumValue's tag term). It needs no width
	// reinterpretation, and a stored overlay value is not an integer term, so it declines there.
	enumTag, variants := false, 0
	if et, ok := stripRefForBounds(leaf).(*EnumType); ok && et != nil && enumHasNoPayload(et) && sort == "Int" {
		variants = len(et.Variants)
		if _, overlay := tr.recordOverlayFor(index.Object, env); overlay {
			return "", true, false
		}
		enumTag, numeric, bits = true, true, 64
	}
	if (sort == "Int" && (!numeric || bits <= 0 || bits > 64)) || (sort == "Bool" && !IsBoolType(leaf)) {
		return "", true, false
	}
	base, stable := tr.containerBase(index.Object, env)
	if !stable {
		return "", true, false
	}
	k, ok := tr.termEnv(index.Index, env)
	if !ok {
		return "", true, false
	}
	name := "|@record_field_" + hex.EncodeToString([]byte(base+"/"+path+"/"+sort)) + "|"
	if tr.ghostUFDecls == nil {
		tr.ghostUFDecls = map[string]bool{}
	}
	if !tr.ghostUFDecls[name] {
		tr.ghostUFDecls[name] = true
		tr.auxDecls = append(tr.auxDecls, "(declare-fun "+name+" (Int) "+sort+")\n")
		if enumTag {
			// Every element's tag names one of the enum's variants.
			app := "(" + name + " k)"
			tr.auxDecls = append(tr.auxDecls, "(assert (forall ((k Int)) (! (and (<= 0 "+app+") (< "+app+" "+strconv.Itoa(variants)+")) :pattern ("+app+"))))\n")
		}
	}
	term := "(" + name + " " + k + ")"
	if enumTag {
		return term, true, true
	}
	if sort == "Int" {
		term = smtSameWidthReinterpret(term, signed, bits)
	}
	if overlay, ok := tr.recordOverlayFor(index.Object, env); ok {
		// The post-body element at the stored index is the stored value; every other element is
		// the pre-body one. The value and index read pre-body state.
		tr.recordOverlayActive = false
		var stored, at string
		var okValue, okIndex bool
		var projection ast.Expr = rerootProjection(n, overlay.value, tr.a)
		if field, ok := structLiteralFieldPath(overlay.value, path); ok {
			projection = field
		}
		if sort == "Bool" {
			stored, okValue = tr.boolTerm(projection, nil)
		} else {
			stored, okValue = tr.termEnv(projection, nil)
			if okValue {
				stored = smtSameWidthReinterpret(stored, signed, bits)
			}
		}
		at, okIndex = tr.termEnv(overlay.index, nil)
		tr.recordOverlayActive = true
		if !okValue || !okIndex {
			return "", true, false
		}
		term = "(ite (= " + k + " " + at + ") " + stored + " " + term + ")"
	}
	return term, true, true
}

// loopFrameContainerRead reports a read of an array a loop-preservation query fixes at the loop
// head (an unbound root name in loopFrameContainers).
func (tr *smtTranslator) loopFrameContainerRead(object ast.Expr, env map[string]string) bool {
	id, ok := stripOptimizationParens(object).(*ast.Ident)
	if !ok || id == nil || tr.calleeBodyDepth != 0 || !tr.loopFrameContainers[id.Name] {
		return false
	}
	_, bound := env[id.Name]
	return !bound
}

func (tr *smtTranslator) recordOverlayFor(object ast.Expr, env map[string]string) (loopArrayStore, bool) {
	if !tr.recordOverlayActive || !tr.loopFrameContainerRead(object, env) {
		return loopArrayStore{}, false
	}
	overlay, ok := tr.recordOverlays[stripOptimizationParens(object).(*ast.Ident).Name]
	return overlay, ok
}

// Fields of a read-only aggregate parameter can contain mutable arrays. They
// are a fixed snapshot only in a frame that cannot write memory through any
// alias or call. Do not use a callee's unbound field spelling as a caller root.
func (tr *smtTranslator) recordFieldContainerBase(field *ast.FieldExpr, env map[string]string) (string, bool) {
	if tr.a.currentFuncDecl == nil || tr.a.currentScope == nil || !tr.a.sviewFrameWriteFreeGhostReads(tr.a.currentFuncDecl) {
		return "", false
	}
	root, _ := projectionChain(field)
	id, ok := root.(*ast.Ident)
	if !ok {
		return "", false
	}
	if bound, isBound := env[id.Name]; isBound {
		// A formal bound to a stable caller record root reads the caller's container fields.
		if !tr.stableRecordRoots[bound] {
			return "", false
		}
		if !recordFieldContainerElementModeled(tr.containerElementType(field)) {
			return "", false
		}
		key, ok := tr.fieldProjectionKey(field, env)
		if !ok {
			return "", false
		}
		base := smtVar(key)
		tr.markStableContainer(base)
		return base, true
	}
	if tr.calleeBodyDepth != 0 || tr.envBindsRoot(id, env) {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	if ref, ok := StripAggregateStateType(sym.Type).(*RefType); ok && (ref.Mutable || ref.Linear) {
		return "", false
	}
	if !recordFieldContainerElementModeled(tr.containerElementType(field)) {
		return "", false
	}
	key, ok := tr.fieldProjectionKey(field, env)
	if !ok {
		return "", false
	}
	base := smtVar(key)
	tr.markStableContainer(base)
	return base, true
}

func (tr *smtTranslator) immutableIndexedRecordLocal(id *ast.Ident, env map[string]string) (ast.Expr, bool) {
	if tr.a.currentScope == nil || tr.calleeBodyDepth != 0 || tr.envBindsRoot(id, env) {
		return nil, false
	}
	for scope := tr.a.currentScope; scope != nil; scope = scope.Parent {
		sym, found := scope.Symbols[id.Name]
		if !found {
			continue
		}
		if sym == nil || sym.Kind != SymbolLocal || sym.Mutable {
			return nil, false
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl == nil || decl.Value == nil {
			return nil, false
		}
		index, ok := stripOptimizationParens(decl.Value).(*ast.IndexExpr)
		if !ok || !tr.a.immutableLocalDefinitionCurrent(scope, sym, decl.Value) {
			return nil, false
		}
		return index, true
	}
	return nil, false
}

// stableRecordRootToken binds a record argument by the caller's root name when that root is a
// fixed snapshot for the whole query: an immutable parameter or local, not held through a
// mutable reference, in a frame that cannot write memory. The callee then reads the caller's
// container fields through fieldProjectionKey's token rerooting.
func (tr *smtTranslator) stableRecordRootToken(argExpr ast.Expr, argEnv map[string]string) (string, bool) {
	if tr == nil || tr.a == nil {
		return "", false
	}
	return tr.stableRecordRootTokenGate(argExpr, argEnv, tr.a.sviewFrameWriteFree)
}

// stableRecordRootTokenGate is stableRecordRootToken with the frame gate that
// proves the current function never writes the record.
func (tr *smtTranslator) stableRecordRootTokenGate(argExpr ast.Expr, argEnv map[string]string, writeFree func(*ast.FuncDecl) bool) (string, bool) {
	if tr == nil || tr.a == nil {
		return "", false
	}
	id, ok := stripOptimizationParens(argExpr).(*ast.Ident)
	if !ok || id == nil {
		return "", false
	}
	if bound, isBound := argEnv[id.Name]; isBound {
		return bound, tr.stableRecordRoots[bound]
	}
	if tr.calleeBodyDepth != 0 || tr.a.currentFuncDecl == nil || tr.a.currentScope == nil || !writeFree(tr.a.currentFuncDecl) {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	if ref, ok := StripAggregateStateType(sym.Type).(*RefType); ok && (ref.Mutable || ref.Linear) {
		return "", false
	}
	if !tr.a.smtEquationContainerRecordType(stripRefForBounds(sym.Type)) {
		return "", false
	}
	token := dtToken(id.Name)
	if tr.stableRecordRoots == nil {
		tr.stableRecordRoots = map[string]bool{}
	}
	tr.stableRecordRoots[token] = true
	return token, true
}

// recordFieldContainerElementModeled admits the element types the container theory reads through
// per-base functions of the index: records (recordElementFieldTerm) and views (containerViewElement).
func recordFieldContainerElementModeled(element Type) bool {
	switch StripAggregateStateType(stripRefForBounds(element)).(type) {
	case *StructType, *SViewType:
		return true
	}
	return false
}

// structLiteralFieldPath reads the field path `.a.b` of a named-field struct literal through its
// field initializers, when every hop is such a literal.
func structLiteralFieldPath(value ast.Expr, path string) (ast.Expr, bool) {
	current := value
	for _, name := range strings.Split(strings.TrimPrefix(path, smtFieldSeparator), smtFieldSeparator) {
		lit, ok := stripOptimizationParens(current).(*ast.StructLitExpr)
		if !ok || lit == nil || len(lit.Spreads) != 0 || lit.CopyBase != nil || len(lit.ArgNames) != len(lit.Args) {
			return nil, false
		}
		found := false
		for i, argName := range lit.ArgNames {
			if argName == name {
				current, found = lit.Args[i], true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return current, true
}
