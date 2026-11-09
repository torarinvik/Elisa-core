package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// storeFlowStructNamed returns the struct type spelled name when every named type whose last
// segment matches it is that one struct type; otherwise nil.
func (a *Analyzer) storeFlowStructNamed(name string) Type {
	short := fillMayAdoptLastSegment(name)
	if a.parallel != nil && a.typeShapesFrozen {
		if a.storeFlowFrozenStructLookup == nil {
			index := make(map[string]Type, len(a.namedTypes))
			for qualified, named := range a.namedTypes {
				key := fillMayAdoptLastSegment(qualified)
				st, valid := named.(*StructType)
				prior, seen := index[key]
				if !valid || st == nil || seen && prior != st {
					index[key] = nil
				} else if !seen {
					index[key] = st
				}
			}
			a.storeFlowFrozenStructLookup = index
		}
		return a.storeFlowFrozenStructLookup[short]
	}
	if a.storeFlowStructLookup != nil {
		if cached, ok := a.storeFlowStructLookup[short]; ok {
			return cached
		}
		// Large named-type tables make even a few full scans costly; smaller
		// tables retain the old threshold to avoid an unnecessary index.
		threshold := 8
		if len(a.namedTypes) >= 128 {
			threshold = 2
		}
		if !a.storeFlowStructLookupComplete && len(a.storeFlowStructLookup) >= threshold {
			// Index only names used by this summary. Unrecognized names still
			// take the ordinary scan, so the cache stays bounded and exact.
			index := make(map[string]Type, len(a.storeFlowNamedTypes)+len(a.storeFlowStructLookup)+1)
			for key := range a.storeFlowStructLookup {
				index[key] = nil
			}
			for _, typeName := range a.storeFlowNamedTypes {
				index[fillMayAdoptLastSegment(typeName)] = nil
			}
			index[short] = nil
			seen := make(map[string]bool, len(index))
			for typeName, named := range a.namedTypes {
				key := fillMayAdoptLastSegment(typeName)
				if _, wanted := index[key]; !wanted {
					continue
				}
				st, valid := named.(*StructType)
				if !valid || st == nil {
					index[key] = nil
					seen[key] = true
					continue
				}
				if seen[key] {
					if index[key] != st {
						index[key] = nil
					}
				} else {
					index[key] = st
					seen[key] = true
				}
			}
			a.storeFlowStructLookup = index
			a.storeFlowStructLookupComplete = true
			return index[short]
		}
	}
	var result Type
	defer func() {
		if a.storeFlowStructLookup != nil {
			a.storeFlowStructLookup[short] = result
		}
	}()
	var found *StructType
	for typeName, named := range a.namedTypes {
		if fillMayAdoptLastSegment(typeName) != short {
			continue
		}
		st, ok := named.(*StructType)
		if !ok || st == nil || (found != nil && found != st) {
			return nil
		}
		found = st
	}
	if found == nil {
		return nil
	}
	result = found
	return result
}

// storeFlowCalleeForArgCheck returns the declared callee only when its summary
// can be used with the call's actual argument positions.
func (a *Analyzer) storeFlowCalleeForArgCheck(call *ast.CallExpr) *ast.FuncDecl {
	decl, ok := a.resolveReadOnlyScanCallee(call)
	if !ok || decl == nil || !storeFlowCallFitsParams(decl, call) || funcHasArenaParam(decl) {
		return nil
	}
	return decl
}

// callArgNeverReaches reports that the callee is summarized and the contents of argument other
// never reach parameter index (directed: other is never stored into index).
func (a *Analyzer) callArgNeverReaches(call *ast.CallExpr, other, index int) bool {
	decl := a.storeFlowCalleeForArgCheck(call)
	if decl == nil {
		return false
	}
	summary := a.storeFlowSummaryFor(decl)
	return summary != nil && !summary.reaches(other, index)
}

// storeFlowOpaqueExternCall reports a direct call of a native, non-generic `extern` whose every
// parameter is a scalar or an opaque foreign handle: no parameter can carry an Elisa reference.
func (a *Analyzer) storeFlowOpaqueExternCall(call *ast.CallExpr) bool {
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.storeFlowParams[ident.Name] || a.storeFlowLocals[ident.Name] || a.globalScope == nil {
		return false
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || !fnType.IsNativeExtern || len(fnType.Params) != len(returnBorrowCallArgs(call)) {
		return false
	}
	for _, param := range fnType.Params {
		switch pt := param.(type) {
		case *OpaqueType:
		case *BitIntType:
		case *BuiltinType:
			if pt == nil || !storeFlowScalarTypeNames[pt.Name] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// storeFlowPushedElemMayShare: an element pushed into receiver may share indirect storage. The
// element is stored as the receiver's element type: a receiver whose element type shares none
// (`darray[u8]`) stores no alias, whatever (untyped literal) expression produced it.
func (a *Analyzer) storeFlowPushedElemMayShare(receiver, elem ast.Expr) bool {
	if a.storeFlowDeclaredViewOrScalarElements(receiver) {
		return false
	}
	if rt := a.exprType(receiver); rt != nil && !a.containsTypeParamCached(rt) {
		if d, ok := StripAggregateStateType(stripRefForBounds(rt)).(*DArrayType); ok && d != nil && d.Elem != nil && !a.containsTypeParamCached(d.Elem) && !a.storeScanSharesCached(d.Elem) {
			return false
		}
	}
	t := a.exprType(elem)
	if t == nil {
		t = a.storeFlowStructLitType(elem)
	}
	return t == nil || a.containsTypeParamCached(t) || a.storeScanSharesCached(t)
}

// storeFlowStructLitType is the declared struct type of a plain, non-generic struct literal
// (`Diagnostic{...}`), found by name in the current module or at top level; nil when it is
// anything else or the name does not resolve to exactly that. It lets summaries solved before
// the body is type-checked still see the literal's field types.
func (a *Analyzer) storeFlowStructLitType(elem ast.Expr) Type {
	lit, ok := returnBorrowStripParens(elem).(*ast.StructLitExpr)
	if !ok || lit == nil || len(lit.TypeArgs) != 0 || len(lit.Spreads) != 0 || lit.CopyBase != nil {
		return nil
	}
	for _, name := range []string{joinQualifiedName(a.currentNamespace, lit.Name), lit.Name} {
		if st, isStruct := a.namedTypes[name].(*StructType); isStruct && st != nil && len(st.TypeParams) == 0 && len(st.GenericParams) == 0 {
			return st
		}
	}
	return nil
}

func storeFlowCallName(call *ast.CallExpr) string {
	switch f := call.Func.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.FieldExpr:
		return "." + f.Field
	}
	return fmt.Sprintf("%T", call.Func)
}

// storeFlowDeclaredViewOrScalarElements reports a receiver name every declaration of which in
// the current function is a darray of views or scalars: its elements share no writable storage.
// It reads declared types, which exist even before the function's body has been analyzed.
func (a *Analyzer) storeFlowDeclaredViewOrScalarElements(receiver ast.Expr) bool {
	id, ok := receiver.(*ast.Ident)
	if !ok || id == nil || a.storeFlowCurrentDecl == nil {
		return false
	}
	idx := a.storeFlowBinderIndexFor(a.storeFlowCurrentDecl)
	if idx.untyped[id.Name] {
		return false
	}
	found := false
	for _, binding := range idx.typedBindings(id.Name) {
		found = true
		elem, isDarray := storeFlowDarrayElementTypeExpr(binding.t)
		if !isDarray || !(storeFlowViewTypeExpr(elem) || storeFlowScalarTypeExpr(elem)) {
			return false
		}
	}
	return found
}

// storeFlowScalarQuery reports a query whose value is a bool or a count (`any`, `all`, `count`)
// over a plain name or field chain: its value holds nothing, and its binder stands only for the
// source's elements (storeFlowQueryBinderPairs links exactly that pair).
func storeFlowScalarQuery(q *ast.QueryExpr) bool {
	switch q.Kind {
	case ast.QueryExprAny, ast.QueryExprAll, ast.QueryExprCount:
	default:
		return false
	}
	if q.Owner != nil || q.Name == "" || q.PatternFilter != nil {
		return false
	}
	if q.Pattern != nil {
		if _, plain := q.Pattern.(*ast.MoveBindNamePattern); !plain {
			return false
		}
	}
	_, isChain := storeFlowChainRoots(q.Source)
	return isChain
}

// storeFlowQueryBinderPairs lists, for each scalar query in stmt's own expressions (not nested
// statements), its binder followed by its source's roots: a write through the binder (a call in
// the filter storing into it) is a write into the source's elements.
func (a *Analyzer) storeFlowQueryBinderPairs(stmt reflect.Value) [][]string {
	var pairs [][]string
	self := stmt.Pointer()
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || !v.CanInterface() {
				return
			}
			// The schema-checked identifier contains only a position and a name,
			// neither of which can contain a query expression.
			if storeFlowIdentFast {
				if _, ok := v.Interface().(*ast.Ident); ok {
					return
				}
			}
			// The schema-checked integer literal contains no nested expressions,
			// so it cannot contain a query either.
			if storeFlowIntLiteralFast && storeFlowIdentFast && storeFlowPositionFast {
				if _, ok := v.Interface().(*ast.IntLit); ok {
					return
				}
			}
			info := storeFlowTypeInfoOf(v.Type())
			if (info.isStmt && v.Pointer() != self) || info.isTypeExpr {
				return
			}
			if q, ok := v.Interface().(*ast.QueryExpr); ok && q != nil && storeFlowScalarQuery(q) {
				roots, _ := storeFlowChainRoots(q.Source)
				pairs = append(pairs, append([]string{q.Name}, a.storeFlowRenamedAll(roots)...))
			}
			walk(v.Elem())
		case reflect.Struct:
			if storeFlowIdentFast && storeFlowPositionFast && v.Type() == lexerPosType {
				return // a verified lexer position has no query expressions
			}
			for _, i := range storeFlowTypeInfoOf(v.Type()).fields {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(stmt)
	return pairs
}
