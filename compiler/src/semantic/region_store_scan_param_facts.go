package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
)

func (a *Analyzer) computeStoreScanParamFacts(key storeScanParamKey) storeScanParamFacts {
	decl, index, mode := a.readDecl(key.decl), key.index, key.mode
	share := mode >= storeScanModeShareFrom
	from := mode - storeScanModeShareFrom
	if decl == nil || index < 0 || index >= len(decl.Params) || len(decl.Body) == 0 {
		return storeScanParamFacts{writes: true} // no body to inspect (extern, unknown)
	}
	idx := a.storeScanIndex(decl)
	alias, hs := a.storeScanParamAliases(decl, idx, index, mode)
	holds := hs.names
	// reaches: an expression that mentions the storage or a holder of its parts.
	reachesStorage := func(expr ast.Expr) bool {
		if len(holds) == 0 {
			return idx.mentions(expr, alias)
		}
		aliased, held := idx.mentionsTwo(expr, alias, holds)
		return aliased || held
	}
	// throughHolder: a place / receiver at least depth deep under a holder (`h[0][0]`, `h[0]`).
	throughHolder := hs.through
	facts := storeScanParamFacts{}
	seenReach := map[storeScanParamKey]bool{}
	reach := func(next storeScanParamKey) {
		if !seenReach[next] {
			seenReach[next] = true
			facts.reach = append(facts.reach, next)
		}
	}
	// Locals of decl that are written or handed to a call. Most scans never
	// need this set: defer the body walk until a sharing argument reaches a call.
	var touched map[string]bool
	// Share mode asks whether parameter from's storage may be stored into this one: a stored
	// value must flow from an alias of that parameter (a fresh literal, a foreign handle, a
	// scalar result or another parameter's storage stores none of it). An unknown source index
	// conservatively stands for every other parameter.
	var outside map[string]bool
	fromOutside := func(value ast.Expr) bool { return true }
	if share {
		var outsideHS storeScanHolds
		if from >= 0 && from < len(decl.Params) && from != index {
			// This is the same single-parameter closure used by other write and
			// share queries, so reuse it across target/source pairs.
			outside, outsideHS = a.storeScanParamAliases(decl, idx, from, storeScanModeWrite)
		} else {
			start := map[string]bool{}
			for k, p := range decl.Params {
				if k != index && (from < 0 || from >= len(decl.Params) || k == from) {
					start[p.Name] = true
				}
			}
			outside, outsideHS = a.storeScanAliasClosure(decl, idx, start)
		}
		outsideHolds := outsideHS.names
		fromOutside = func(value ast.Expr) bool {
			return a.storeScanFlowsFrom(decl, idx, value, outside) || (len(outsideHolds) > 0 && a.storeScanFlowsFrom(decl, idx, value, outsideHolds))
		}
	}
	for _, node := range idx.paramWriteNodes() {
		if assign, isAssign := node.(*ast.AssignStmt); isAssign && assign != nil {
			if id, bare := stripParenExpr(assign.Target).(*ast.Ident); bare && id != nil && !storeScanNameIsRefParam(decl, id.Name, idx) {
				continue // rebinding a local / by-value copy writes no shared storage
			}
			if (idx.mentions(assign.Target, alias) || throughHolder(assign.Target, 2)) && (!share || (a.storeScanAssignMayStoreShared(decl, assign) && fromOutside(assign.Value))) {
				facts.writes = true
				facts.why = "assign"
				if storeScanTrace && share {
					facts.why = fmt.Sprintf("assign outside=%v", outside)
				}
				facts.at = node
				return facts
			}
		} else if storeScanNodePlaceMentionsEither(idx, node, alias, holds) {
			facts.writes = true
			facts.why = "place"
			facts.at = node
			return facts
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || call == nil {
			continue
		}
		if a.storeScanIsEnumConstructor(decl, call) {
			continue // `Enum.Variant(..)` builds a fresh value; it writes none of its payloads
		}
		args := returnBorrowCallArgs(call)
		if field, isMethod := call.Func.(*ast.FieldExpr); isMethod && field != nil && len(args) == len(call.Args) {
			if (idx.mentions(field.Object, alias) || throughHolder(field.Object, 1)) && !storeScanReadOnlyMethods[field.Field] {
				if !share {
					facts.writes = true
					facts.why = "method-write"
					facts.at = node
					return facts
				}
				for _, arg := range call.Args {
					if fromOutside(arg) && a.storeScanContainerElemsMayShare(decl, field.Object) {
						facts.writes = true
						facts.why = "method-share"
						facts.at = node
						return facts
					}
				}
			}
			if a.storeScanReceiverIsBuiltinDArray(decl, field.Object) {
				continue
			}
		}
		// Most calls have only a few operands; retain their indexes on the
		// stack while preserving append growth for larger signatures.
		var mentionedStorage [8]int
		mentioned := mentionedStorage[:0]
		for k, arg := range args {
			if reachesStorage(arg) {
				mentioned = append(mentioned, k)
			}
		}
		if len(mentioned) == 0 {
			continue
		}
		callee, resolved := a.resolveReadOnlyScanCallee(call)
		if !resolved || callee == nil || !storeFlowCallFitsParams(callee, call) {
			if share {
				given := false
				for _, arg := range args {
					given = given || fromOutside(arg)
				}
				if !given {
					continue // nothing of another parameter is handed to it
				}
			}
			facts.writes = true
			facts.why = "unresolved"
			facts.at = node
			return facts
		}
		for _, k := range mentioned {
			if !a.declParamIsWritableRef(callee, k) || a.declParamIsViewOrScalar(callee, k) {
				continue
			}
			if share {
				// The callee may store into k what any argument carrying from's storage gives it.
				for j, arg := range args {
					if j != k && fromOutside(arg) {
						reach(storeScanShareKey(callee, k, j))
					}
				}
				continue
			}
			next := mode
			if !idx.mentions(args[k], alias) {
				// Only a holder of parts is handed over: the callee must write through its parts.
				next = storeScanModeDeep
			}
			reach(storeScanParamKey{callee, k, next})
		}
		if share {
			continue
		}
		// The callee may store the parameter's storage into a holder argument f then writes.
		// Without local bindings there cannot be such a holder, so neither the
		// sharing type queries nor the touched-local scan can add an edge.
		if len(idx.locals) == 0 {
			continue
		}
		sharing := false
		for _, k := range mentioned {
			if a.storeScanMayShare(decl, args[k]) {
				sharing = true
				break
			}
		}
		if !sharing {
			continue
		}
		if touched == nil {
			touched = storeScanTouchedLocals(idx)
		}
		for m, arg := range args {
			root := rootIdentExpr(stripAddrAndParens(arg))
			if root == nil || alias[root.Name] || !touched[root.Name] || !idx.locals[root.Name] {
				continue
			}
			if !a.declParamIsWritableRef(callee, m) || a.declParamIsViewOrScalar(callee, m) {
				continue
			}
			// The callee may store the storage it receives at k into m.
			for _, k := range mentioned {
				if k != m && a.storeScanMayShare(decl, args[k]) {
					reach(storeScanShareKey(callee, m, k))
				}
			}
		}
	}
	return facts
}

// storeScanTouchedLocals: names that are a store place's root, a method receiver's root, or an
// argument's root anywhere in the body.
func storeScanTouchedLocals(idx *storeScanDeclIndex) map[string]bool {
	touched := map[string]bool{}
	mark := func(expr ast.Expr) {
		if root := rootIdentExpr(stripAddrAndParens(expr)); root != nil {
			touched[root.Name] = true
		}
	}
	for _, node := range idx.paramWriteNodes() {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n != nil {
				mark(n.Target)
			}
		case *ast.CallExpr:
			if n == nil {
				continue
			}
			if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil {
				mark(field.Object)
			}
			for _, arg := range n.Args {
				mark(arg)
			}
		default:
			v := reflect.ValueOf(node)
			if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
				continue
			}
			v = v.Elem()
			for _, index := range reflectMetaOf(v.Type()).storeScanPlaceFields {
				var f reflect.Value
				if len(index) == 1 {
					f = v.Field(index[0])
				} else {
					f = v.FieldByIndex(index)
				}
				if f.CanInterface() {
					if expr, ok := f.Interface().(ast.Expr); ok && expr != nil {
						mark(expr)
					}
				}
			}
		}
	}
	return touched
}

// storeScanNodePlaceMentions: node has a `Target` or `Store` place mentioning one of names.
func storeScanNodePlaceMentions(idx *storeScanDeclIndex, node any, names map[string]bool) bool {
	return storeScanNodePlaceMentionsEither(idx, node, names, nil)
}

func storeScanNodePlaceMentionsEither(idx *storeScanDeclIndex, node any, left, right map[string]bool) bool {
	v := reflect.ValueOf(node)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return false
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, index := range reflectMetaOf(v.Type()).storeScanPlaceFields {
		var f reflect.Value
		if len(index) == 1 {
			f = v.Field(index[0])
		} else {
			f = v.FieldByIndex(index)
		}
		if !f.CanInterface() {
			continue
		}
		if expr, ok := f.Interface().(ast.Expr); ok && expr != nil {
			if len(right) == 0 {
				if idx.mentions(expr, left) {
					return true
				}
			} else if l, r := idx.mentionsTwo(expr, left, right); l || r {
				return true
			}
		}
	}
	return false
}

// storeScanNameIsRefParam: name is a reference-typed parameter of decl that no local shadows
// (a bare `name <- v` then writes through the reference). Unknown parameter types count.
func storeScanNameIsRefParam(decl *ast.FuncDecl, name string, idx *storeScanDeclIndex) bool {
	if idx.locals[name] {
		return false
	}
	for _, p := range decl.Params {
		if p.Name != name {
			continue
		}
		t := p.Type
		for {
			switch tt := t.(type) {
			case *ast.MutableType:
				if tt == nil {
					return false
				}
				t = tt.Elem
			case *ast.RefType:
				return true
			default:
				return p.Type == nil || p.Mutable
			}
		}
	}
	return false
}

// storeScanContainerElemsMayShare: what a builtin method stores into receiver (an element, key or
// value, converted to the container's element type) may share indirect storage. A `darray[u8]`
// receiver stores bytes whatever expression produced them.
func (a *Analyzer) storeScanContainerElemsMayShare(decl *ast.FuncDecl, receiver ast.Expr) bool {
	decl = a.readDecl(decl)
	t, ok := a.storeScanStaticType(decl, storeScanUnwrap(receiver))
	if !ok || t == nil || a.containsTypeParamCached(t) {
		// Declared annotations: every declaration of the name (parameter or local, no other
		// binder) a builtin container of non-sharing elements.
		id, isIdent := storeScanUnwrap(receiver).(*ast.Ident)
		if !isIdent || id == nil {
			return true
		}
		idx := a.storeScanIndex(decl)
		if idx.otherBound[id.Name] {
			return true
		}
		found := false
		for _, p := range decl.Params {
			if p.Name == id.Name {
				if !a.storeScanTypeExprElemsShareNot(p.Type) {
					return true
				}
				found = true
			}
		}
		for _, v := range idx.decls[id.Name] {
			if !a.storeScanTypeExprElemsShareNot(v.Type) {
				return true
			}
			found = true
		}
		return !found
	}
	switch c := StripAggregateStateType(stripRefForBounds(t)).(type) {
	case *DArrayType:
		return c == nil || a.storeScanSharesCached(c.Elem)
	case *SetType:
		return c == nil || a.storeScanSharesCached(c.Elem)
	case *DictType:
		return c == nil || a.storeScanSharesCached(c.Key) || a.storeScanSharesCached(c.Value)
	}
	return true
}

// storeScanAssignMayStoreShared: the stored value may share indirect storage. An element store
// (`xs[i] <- v`) stores v as the container's element type, which may share none.
func (a *Analyzer) storeScanAssignMayStoreShared(decl *ast.FuncDecl, assign *ast.AssignStmt) bool {
	decl = a.readDecl(decl)
	if index, isIndex := stripParenExpr(assign.Target).(*ast.IndexExpr); isIndex && index != nil && !a.storeScanContainerElemsMayShare(decl, index.Object) {
		return false
	}
	return a.storeScanMayShare(decl, assign.Value)
}

// storeScanTypeExprElemsShareNot: a declared `darray[E]` / `set[E]` (through `mutable` and a
// reference) whose element E is a plain scalar, a read-only view (sview, cstr) or an opaque
// foreign handle -- none of which shares indirect storage.
func (a *Analyzer) storeScanTypeExprElemsShareNot(t ast.TypeExpr) bool {
	if mt, ok := t.(*ast.MutableType); ok && mt != nil {
		t = mt.Elem
	}
	if ref, ok := t.(*ast.RefType); ok && ref != nil {
		t = ref.Elem
	}
	bt, ok := t.(*ast.BuiltinTypeExpr)
	if !ok || bt == nil || (bt.Name != "darray" && bt.Name != "set") || len(bt.TypeArgs) != 1 {
		return false
	}
	elem := bt.TypeArgs[0]
	if storeScanTypeExprIsPlainScalar(elem) || a.storeScanTypeExprIsOpaque(elem) {
		return true
	}
	if named, isNamed := elem.(*ast.NamedType); isNamed && named != nil {
		if named.Name == "sview" || named.Name == "cstr" {
			return true
		}
		// A named struct / enum of such values (`ValueType`).
		if resolved := a.storeFlowNamedType(named.Name); resolved != nil && !a.containsTypeParamCached(resolved) && !a.storeScanSharesCached(resolved) {
			return true
		}
	}
	return false
}
