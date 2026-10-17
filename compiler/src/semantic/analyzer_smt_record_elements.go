package semantic

import (
	"encoding/hex"
	"strings"

	"elisacore/src/ast"
)

// A record's scalar fields are functions of the container and index, rather
// than of the source location of a read. Keep the index inside the application
// so quantified reads cannot become one binder-independent scalar.
func (tr *smtTranslator) recordElementFieldTerm(n *ast.FieldExpr, env map[string]string) (string, bool, bool) {
	root, path := projectionChain(n)
	if id, ok := root.(*ast.Ident); ok {
		if value, found := tr.immutableIndexedRecordLocal(id, env); found {
			return tr.recordElementFieldTerm(rerootProjection(n, value, tr.a), env)
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
		!tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) {
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
	signed, bits, numeric := smtIntWidthSign(leaf)
	if !numeric || bits <= 0 || bits > 64 {
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
	name := "|@record_field_" + hex.EncodeToString([]byte(base+"/"+path)) + "|"
	if tr.ghostUFDecls == nil {
		tr.ghostUFDecls = map[string]bool{}
	}
	if !tr.ghostUFDecls[name] {
		tr.ghostUFDecls[name] = true
		tr.auxDecls = append(tr.auxDecls, "(declare-fun "+name+" (Int) Int)\n")
	}
	return smtSameWidthReinterpret("("+name+" "+k+")", signed, bits), true, true
}

// Fields of a read-only aggregate parameter can contain mutable arrays. They
// are a fixed snapshot only in a frame that cannot write memory through any
// alias or call. Do not use a callee's unbound field spelling as a caller root.
func (tr *smtTranslator) recordFieldContainerBase(field *ast.FieldExpr, env map[string]string) (string, bool) {
	if tr.a.currentFuncDecl == nil || tr.calleeBodyDepth != 0 || tr.a.currentScope == nil ||
		!tr.a.sviewFrameWriteFree(tr.a.currentFuncDecl) {
		return "", false
	}
	root, _ := projectionChain(field)
	id, ok := root.(*ast.Ident)
	if !ok || tr.envBindsRoot(id, env) {
		return "", false
	}
	sym, found := tr.a.currentScope.Lookup(id.Name)
	if !found || sym == nil || (sym.Kind != SymbolParam && sym.Kind != SymbolLocal) || sym.Mutable || symbolRootMutable(sym) {
		return "", false
	}
	if ref, ok := StripAggregateStateType(sym.Type).(*RefType); ok && (ref.Mutable || ref.Linear) {
		return "", false
	}
	element := tr.containerElementType(field)
	if _, ok := StripAggregateStateType(element).(*StructType); !ok {
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
