package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// storeScanHoldsNoRegionStorage: t carries no region storage and can contain no region reference
// (memoized per type: the scan asks it for every node of every summary query).
// storeScanExtendArgMode: a builtin darray's `extend(xs)` copies xs's ELEMENTS into the
// receiver and never retains xs's own header; elements whose type holds no region storage
// (`darray[u32]`) carry nothing from xs at all.
func (a *Analyzer) storeScanExtendArgMode(decl *ast.FuncDecl, arg ast.Expr, mode storeMode) storeMode {
	decl = a.readDecl(decl)
	if mode == storeNone {
		return mode
	}
	t, known := a.lookupExprType(storeScanUnwrap(arg))
	if !known {
		// The callee may not be typed yet: a field chain of a parameter or of an explicitly
		// typed local still has its declared type.
		t, known = a.storeScanDeclTypeOf(decl, storeScanUnwrap(arg))
	}
	if !known || t == nil {
		// Interleaved projections such as state.rows[i].names have declared
		// element types too, even before their expression types are populated.
		t, known = a.storeScanParamProjectionType(decl, storeScanUnwrap(arg))
	}
	if known && t != nil && !a.containsTypeParamCached(t) {
		if da, ok := stripRefForBounds(t).(*DArrayType); ok && da.Elem != nil && !a.containsTypeParamCached(da.Elem) && !a.storeScanTypeMayEncodeAddress(da.Elem, map[Type]bool{}) && (a.storeScanHoldsNoRegionStorage(da.Elem) || a.storeScanTypeIsProgramLivedForStoreProof(da.Elem)) {
			return storeNone
		}
	}
	if mode == storeHeader {
		return storeElements
	}
	return mode
}

func (a *Analyzer) storeScanHoldsNoRegionStorage(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.lookupStoreScanNoStorage(t); ok {
			return cached
		}
	}
	result := !typeCarriesRegionStorage(t) && !a.typeCanContainRegionRefs(stripRefForBounds(t), map[string]bool{})
	if cacheable {
		a.setStoreScanNoStorage(t, result)
	}
	return result
}

// storeScanViewOnlyCached: storeScanTypeIsViewOnly, memoized per type.
func (a *Analyzer) storeScanViewOnlyCached(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.lookupStoreScanViewOnly(t); ok {
			return cached
		}
	}
	result := storeScanTypeIsViewOnly(t)
	if cacheable {
		a.setStoreScanViewOnly(t, result)
	}
	return result
}

// storeScanSharesCached: typeSharesIndirectStorage, memoized per type.
func (a *Analyzer) storeScanSharesCached(t Type) bool {
	cacheable := t != nil && reflect.ValueOf(t).Kind() == reflect.Pointer
	if cacheable {
		if cached, ok := a.lookupStoreScanShares(t); ok {
			return cached
		}
	}
	result := typeSharesIndirectStorage(t, map[Type]bool{})
	if cacheable {
		a.setStoreScanShares(t, result)
	}
	return result
}

type storeScanTaintKey struct {
	decl   *ast.FuncDecl
	other  int
	target string
}

type storeScanTaintResult struct {
	params, header, content, targets, views, holders map[string]bool
}

// storeScanLocalIsProgramView: name is a body local (not a parameter, not bound by a pattern or
// loop) whose every declaration spells a view type annotated with a program region
// (`text: sview @source = ...`). Such a value can only view program-lived bytes.
func (a *Analyzer) storeScanLocalIsProgramView(decl *ast.FuncDecl, name string) bool {
	decl = a.readDecl(decl)
	if decl == nil || len(a.programRegions) == 0 {
		return false
	}
	for _, p := range decl.Params {
		if p.Name == name {
			return false
		}
	}
	idx := a.storeScanIndex(decl)
	if idx.otherBound[name] || len(idx.decls[name]) == 0 {
		return false
	}
	for _, v := range idx.decls[name] {
		nt, ok := v.Type.(*ast.NamedType)
		if !ok || nt == nil || (nt.Name != "sview" && nt.Name != "cstr") || !a.programRegions[nt.Region] {
			return false
		}
	}
	return true
}

// declParamIsProgramLived: decl's parameter index has a resolved type whose every region-carrying
// part is program-lived.
func (a *Analyzer) declParamIsProgramLived(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	if len(a.programRegions) == 0 {
		return false
	}
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || index < 0 || index >= len(fnType.Params) {
		return false
	}
	t := fnType.Params[index]
	if t == nil || a.containsTypeParamCached(t) {
		return false
	}
	elem := stripRefForBounds(t)
	return a.storeScanTypeIsProgramLivedForStoreProof(elem)
}
