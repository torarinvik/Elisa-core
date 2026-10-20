package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// Parameter write reachability. The callee-write taint (storeScanCallWritePlan) asks whether a
// resolved callee may write caller-visible storage through parameter i before tainting a body
// local passed there. The answer is reachability over a graph whose nodes are
// (function, parameter, mode) and whose node facts are purely local, so it never consults store
// summaries or taint: deterministic, cycle-safe, cacheable.
//
// Mode write, (f, i): f may write storage reachable from its parameter i. Local writers: a
// `Target` / `Store` place, a non-read-only method receiver, or an argument of an unresolved
// call, mentioning the parameter or a local alias of it (bound from a value that may share its
// storage, by a declaration, a store, a binding construct, or a builtin push into the local).
// Edges: (g, k, write) for an alias passed at a writable, storage-sharing parameter k of a
// resolved g; and (g, m, share) when such a call also receives a local holder L of f at m that
// f writes or passes on -- g may store the parameter's storage into L, and f then writes L.
//
// Mode share, (g, m): g may store a value sharing indirect storage into the storage of its
// parameter m (whatever that value's origin). Local writers: a place or non-read-only method on
// an alias of the parameter that receives a value that may share storage (unknown types do), any
// other `Target` / `Store` place, an unresolved call given an alias. Edges: (h, k, share) for an
// alias passed at a writable parameter k of a resolved h.

const (
	storeScanModeWrite = 0
	// Mode deep, (g, i): g may write storage reachable from the PARTS of its parameter i (what
	// its elements / fields point to), not i's own buffer. A holder argument (`entries`, holding
	// entries that contain a part) reaches the caller's storage only this way: the callee
	// pushing onto its own `entries` buffer writes nothing the holder's parts point to.
	storeScanModeDeep = 2
	// Mode share-from, (g, m, k): mode storeScanModeShareFrom+k. g may store a value sharing the
	// storage of its parameter k into the storage of its parameter m. Per (into, from) pair: a
	// callee pushing an AST handle it was given into `entries` does not thereby store some other
	// argument's darray header there.
	storeScanModeShareFrom = 16
)

// storeScanShareKey: the share-from node for (callee, into, from).
func storeScanShareKey(callee *ast.FuncDecl, into, from int) storeScanParamKey {
	return storeScanParamKey{callee, into, storeScanModeShareFrom + from}
}

type storeScanParamKey struct {
	decl  *ast.FuncDecl
	index int
	mode  int
}

type storeScanParamFacts struct {
	why    string
	at     any
	writes bool
	reach  []storeScanParamKey
}

// storeScanParamMayBeWritten: (decl, index, write) reaches a writing node.
func (a *Analyzer) storeScanParamMayBeWritten(decl *ast.FuncDecl, index int) bool {
	decl = a.readDecl(decl)
	return a.storeScanParamReaches(storeScanParamKey{decl, index, storeScanModeWrite})
}

func (a *Analyzer) storeScanParamReaches(start storeScanParamKey) bool {
	if cached, ok := a.storeScanParamWriteCache[start]; ok {
		return cached
	}
	if a.storeScanParamWriteCache == nil {
		a.storeScanParamWriteCache = map[storeScanParamKey]bool{}
	}
	visited := map[storeScanParamKey]bool{start: true}
	parent := map[storeScanParamKey]storeScanParamKey{}
	queue := []storeScanParamKey{start}
	result := false
	for len(queue) > 0 && !result {
		key := queue[0]
		queue = queue[1:]
		if cached, ok := a.storeScanParamWriteCache[key]; ok {
			if cached && storeScanTrace {
				storeScanParamWriterOf[start] = storeScanParamWriterOf[key]
			}
			result = result || cached
			continue // a cached false already covers everything it reaches
		}
		facts := a.storeScanParamLocalFacts(key)
		if facts.writes {
			if storeScanTrace {
				pos := ""
				if n, ok := facts.at.(ast.Node); ok && n != nil {
					pos = n.Pos().String()
				}
				storeScanParamWriterOf[start] = fmt.Sprintf("%s/%d/%d %s %s", key.decl.Name, key.index, key.mode, facts.why, pos)
				path := ""
				for k := key; k != start; k = parent[k] {
					path = fmt.Sprintf(" <- %s/%d/%d", k.decl.Name, k.index, k.mode) + path
				}
				fmt.Fprintf(os.Stderr, "PARAMW start=%s/%d/%d writer=%s/%d/%d why=%s at %s path=%s\n", start.decl.Name, start.index, start.mode, key.decl.Name, key.index, key.mode, facts.why, pos, path)
			}
			result = true
			break
		}
		for _, next := range facts.reach {
			if !visited[next] {
				visited[next] = true
				parent[next] = key
				queue = append(queue, next)
			}
		}
	}
	if result {
		a.storeScanParamWriteCache[start] = true
	} else {
		// Nothing reachable writes: the same holds for every node visited.
		for key := range visited {
			a.storeScanParamWriteCache[key] = false
		}
	}
	return result
}

func (a *Analyzer) storeScanParamLocalFacts(key storeScanParamKey) storeScanParamFacts {
	if facts, ok := a.storeScanParamFactsCache[key]; ok {
		return facts
	}
	facts := a.computeStoreScanParamFacts(key)
	if a.storeScanParamFactsCache == nil {
		a.storeScanParamFactsCache = map[storeScanParamKey]storeScanParamFacts{}
	}
	a.storeScanParamFactsCache[key] = facts
	return facts
}

// storeScanMayShare: value's type may share indirect storage (unknown types may).
func (a *Analyzer) storeScanMayShare(decl *ast.FuncDecl, value ast.Expr) bool {
	decl = a.readDecl(decl)
	if value == nil {
		return false
	}
	inner := storeScanUnwrap(value)
	if t, ok := a.storeScanStaticType(decl, inner); ok && t != nil && !containsTypeParam(t) && !a.storeScanSharesCached(t) {
		return false
	}
	switch e := inner.(type) {
	case *ast.Ident:
		// A local or parameter declared with a scalar / opaque type (`line: u32`).
		if e == nil {
			return false
		}
		for _, p := range decl.Params {
			if p.Name == e.Name {
				return !(storeScanTypeExprIsPlainScalar(p.Type) || a.storeScanTypeExprIsOpaque(p.Type))
			}
		}
		if a.storeScanScalarLocals(decl)[e.Name] {
			return false
		}
	case *ast.CallExpr:
		// An un-typed call result: the callee's declared return type.
		if t := a.storeScanCallReturnType(e); t != nil && !containsTypeParam(t) && !a.storeScanSharesCached(t) {
			return false
		}
	case *ast.TernaryExpr:
		// Only the selected branch is returned. Effects of evaluating the
		// condition are scanned separately, not retained in the result.
		return a.storeScanMayShare(decl, e.Value) || a.storeScanMayShare(decl, e.Alt)
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit:
		return false
	}
	return true
}

// storeScanCallReturnType: the declared return type of a call's resolved callee or global
// function / extern symbol, or nil.
func (a *Analyzer) storeScanCallReturnType(call *ast.CallExpr) Type {
	if callee, ok := a.resolveReadOnlyScanCallee(call); ok && callee != nil {
		if sym := a.funcDeclSymbols[callee]; sym != nil {
			if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil {
				return ft.Return
			}
		}
		return nil
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil || a.globalScope == nil {
		return nil
	}
	sym, ok := a.globalScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil
	}
	if ft, isFn := sym.Type.(*FuncType); isFn && ft != nil {
		return ft.Return
	}
	return nil
}

// storeScanParamAliases: the names that ARE the parameter's storage (alias) and the locals that
// CONTAIN parts of it (holds).
func (a *Analyzer) storeScanParamAliases(decl *ast.FuncDecl, idx *storeScanDeclIndex, index, mode int) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	if mode == storeScanModeDeep {
		// The parameter itself is only a holder: its parts are the storage.
		return a.storeScanAliasClosureFrom(decl, idx, map[string]bool{}, map[string]bool{decl.Params[index].Name: true})
	}
	return a.storeScanAliasClosure(decl, idx, map[string]bool{decl.Params[index].Name: true})
}

// storeScanAliasClosure grows the starting names into:
//   - alias: names bound to the storage itself or a part of it -- a place rooted at an alias
//     (`x = p.items`, `x = holder[0]`), a call that may return one, a binder over an alias or a
//     holder -- when the value's type can share storage at all;
//   - holds: locals whose own buffer is fresh but CONTAINS parts (`h = [p]`, `h.push(p.items)`):
//     writing the holder itself (`h.push(v)`, `h[0] <- v`) writes nothing of the storage; writing
//     through a part (`h[0][0] <- v`, `h[0].push(v)`) does.
func (a *Analyzer) storeScanAliasClosure(decl *ast.FuncDecl, idx *storeScanDeclIndex, alias map[string]bool) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	return a.storeScanAliasClosureFrom(decl, idx, alias, nil)
}

// storeScanAliasClosureFrom: storeScanAliasClosure starting from alias names and holder names
// (holding parts under any projection).
func (a *Analyzer) storeScanAliasClosureFrom(decl *ast.FuncDecl, idx *storeScanDeclIndex, alias, startHolds map[string]bool) (map[string]bool, storeScanHolds) {
	decl = a.readDecl(decl)
	holds := map[string]bool{}
	// fields: per holder, the first projections that lead to a part (`entry = E{target: p}`
	// holds a part under `.target` only; "*" = any projection).
	fields := map[string]map[string]bool{}
	for name := range startHolds {
		holds[name] = true
		fields[name] = map[string]bool{"*": true}
	}
	// Locals declared with a scalar / opaque type, and integer range binders, hold no storage
	// whatever their value mentions (`slot: usize = p.bits.usize()`).
	scalars := a.storeScanScalarLocals(decl)
	carries := func(value ast.Expr) bool {
		return a.storeScanFlowsFrom(decl, idx, value, alias) || (len(holds) > 0 && a.storeScanFlowsFrom(decl, idx, value, holds))
	}
	for changed := true; changed; {
		changed = false
		add := func(set map[string]bool, name string) {
			if name != "" && !alias[name] && !set[name] && !scalars[name] {
				set[name] = true
				changed = true
			}
		}
		// hold: name contains a part under the first projection proj ("*" = any).
		hold := func(name, proj string) {
			if name == "" || alias[name] || scalars[name] {
				return
			}
			add(holds, name)
			if fields[name] == nil {
				fields[name] = map[string]bool{}
			}
			if !fields[name][proj] {
				fields[name][proj] = true
				changed = true
			}
		}
		h := storeScanHolds{names: holds, fields: fields}
		bindValue := func(name string, value ast.Expr) {
			if value == nil || !a.storeScanMayShare(decl, value) {
				return
			}
			switch {
			case storeScanMayAliasPlace(idx, value, alias):
				add(alias, name)
			case len(holds) > 0 && storeScanMayAliasPlace(idx, value, holds):
				if storeScanPlaceDepth(value) >= 1 {
					if h.through(value, 1) {
						add(alias, name) // `x = holder[0]`: a part
					} else if carries(value) {
						hold(name, "*") // `x = entry.other`: may still contain parts
					}
				} else {
					hold(name, "*") // `x = holder`: another holder
				}
			case carries(value):
				if lit, isLit := stripParenExpr(value).(*ast.StructLitExpr); isLit && lit != nil && len(lit.Spreads) == 0 && lit.CopyBase == nil && len(lit.ArgNames) == len(lit.Args) {
					for k, arg := range lit.Args {
						if carries(arg) {
							hold(name, lit.ArgNames[k]) // `E{target: p}`: a part under .target
						}
					}
				} else {
					hold(name, "*") // a fresh literal / result containing parts
				}
			}
		}
		for _, node := range idx.flat {
			switch n := node.(type) {
			case *ast.VarDeclStmt:
				if n != nil {
					bindValue(n.Name, n.Value)
				}
			case *ast.AssignStmt:
				if n == nil {
					continue
				}
				if id, bare := stripParenExpr(n.Target).(*ast.Ident); bare && id != nil {
					bindValue(id.Name, n.Value)
				} else if carries(n.Value) {
					// `local.f <- p` / `local[i] <- p`: the local now contains a part.
					if root := rootIdentExpr(stripAddrAndParens(n.Target)); root != nil && !alias[root.Name] {
						hold(root.Name, storeScanFirstProjection(n.Target))
					}
				}
			case *ast.CallExpr:
				// `xs.push(p)` / `xs.extend(..p..)`: the receiver keeps a part.
				if n == nil {
					continue
				}
				if field, isMethod := n.Func.(*ast.FieldExpr); isMethod && field != nil {
					if containerGrowthMethods[field.Field] && a.storeScanReceiverIsBuiltinDArray(decl, field.Object) && !a.storeScanContainerElemsMayShare(decl, field.Object) {
						// `path.names.extend(xs)` into a `darray[sview]`: element copies that share
						// no storage -- the receiver holds nothing of xs.
						continue
					}
					for _, arg := range n.Args {
						if carries(arg) {
							if root := rootIdentExpr(stripAddrAndParens(field.Object)); root != nil && !alias[root.Name] {
								hold(root.Name, storeScanFirstProjection(field.Object))
							}
							break
						}
					}
				}
			case *ast.ForStmt:
				// `for i in a..<b`: an integer binder.
			case ast.Stmt:
				if n != nil && (idx.headerMentions(n, alias) || (len(holds) > 0 && idx.headerMentions(n, holds))) {
					for _, name := range idx.boundNames(n) {
						add(alias, name)
					}
				}
			}
		}
	}
	return alias, storeScanHolds{names: holds, fields: fields}
}

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
		return idx.mentions(expr, alias) || (len(holds) > 0 && idx.mentions(expr, holds))
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
	// Locals of decl that are written or handed to a call (holders, write mode only).
	var touched map[string]bool
	if !share {
		touched = storeScanTouchedLocals(idx)
	}
	// Share mode asks whether parameter from's storage may be stored into this one: a stored
	// value must flow from an alias of that parameter (a fresh literal, a foreign handle, a
	// scalar result or another parameter's storage stores none of it). An unknown source index
	// conservatively stands for every other parameter.
	var outside map[string]bool
	fromOutside := func(value ast.Expr) bool { return true }
	if share {
		start := map[string]bool{}
		for k, p := range decl.Params {
			if k != index && (from < 0 || from >= len(decl.Params) || k == from) {
				start[p.Name] = true
			}
		}
		var outsideHS storeScanHolds
		outside, outsideHS = a.storeScanAliasClosure(decl, idx, start)
		outsideHolds := outsideHS.names
		fromOutside = func(value ast.Expr) bool {
			return a.storeScanFlowsFrom(decl, idx, value, outside) || (len(outsideHolds) > 0 && a.storeScanFlowsFrom(decl, idx, value, outsideHolds))
		}
	}
	for _, node := range idx.flat {
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
		} else if storeScanNodePlaceMentions(idx, node, alias) || (len(holds) > 0 && storeScanNodePlaceMentions(idx, node, holds)) {
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
		mentioned := []int{}
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
		sharing := false
		for _, k := range mentioned {
			sharing = sharing || a.storeScanMayShare(decl, args[k])
		}
		if !sharing {
			continue
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
	for _, node := range idx.flat {
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
			for _, field := range []string{"Target", "Store"} {
				f := reflectFieldByName(v.Elem(), field)
				if f.IsValid() && f.CanInterface() {
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
	v := reflect.ValueOf(node)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return false
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return false
	}
	for _, field := range []string{"Target", "Store"} {
		f := reflectFieldByName(v, field)
		if !f.IsValid() || !f.CanInterface() {
			continue
		}
		if expr, ok := f.Interface().(ast.Expr); ok && expr != nil && idx.mentions(expr, names) {
			return true
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
	if !ok || t == nil || containsTypeParam(t) {
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
		if resolved := a.storeFlowNamedType(named.Name); resolved != nil && !containsTypeParam(resolved) && !a.storeScanSharesCached(resolved) {
			return true
		}
	}
	return false
}

// storeScanFlowsFrom: value may carry storage shared with one of alias. Structural: a call result, literal or
// comprehension carries it only through a part that does (`remove_name(name, xs)` with
// `name: sview` aliasing nothing new; `[item for item in xs]` copies sview elements).
func (a *Analyzer) storeScanFlowsFrom(decl *ast.FuncDecl, idx *storeScanDeclIndex, value ast.Expr, alias map[string]bool) bool {
	decl = a.readDecl(decl)
	flows := func(v ast.Expr) bool { return a.storeScanFlowsFrom(decl, idx, v, alias) }
	{
		if value == nil || !idx.mentions(value, alias) || !a.storeScanMayShare(decl, value) {
			return false
		}
		switch e := stripParenExpr(value).(type) {
		case *ast.TernaryExpr:
			return flows(e.Value) || flows(e.Alt)
		case *ast.CallExpr:
			if field, isMethod := e.Func.(*ast.FieldExpr); isMethod && field != nil && flows(field.Object) {
				return true
			}
			if callee, ok := a.resolveReadOnlyScanCallee(e); ok && callee != nil && storeFlowCallFitsParams(callee, e) {
				// A resolved callee's result carries only the parameters its (completed) store-flow
				// summary connects to the result.
				if sum := a.storeFlowKindsFor(callee); sum != nil && a.storeFlowInProgress[callee] == nil {
					args := returnBorrowCallArgs(e)
					if len(sum.retConn) == len(callee.Params) {
						for i, arg := range args {
							if i >= len(sum.retConn) || a.storeFlowRetKind(sum, i) == storeFlowEdgeHeader {
								if flows(arg) {
									return true
								}
							}
						}
						return false
					}
				}
			}
			for _, arg := range e.Args {
				if flows(arg) {
					return true
				}
			}
			return false
		case *ast.StructLitExpr:
			for _, arg := range e.Args {
				if flows(arg) {
					return true
				}
			}
			for _, spread := range e.Spreads {
				if idx.mentions(spread, alias) {
					return true
				}
			}
			return false
		case *ast.TupleExpr:
			for _, elem := range e.Elems {
				if flows(elem) {
					return true
				}
			}
			return false
		case *ast.ListLitExpr:
			for i, elem := range e.Elems {
				if (i < len(e.Spreads) && e.Spreads[i] && idx.mentions(elem, alias)) || flows(elem) {
					return true
				}
			}
			for _, key := range e.Keys {
				if flows(key) {
					return true
				}
			}
			return false
		case *ast.ListComprehensionExpr:
			// Elements read out of an alias share its storage only if their type can.
			return a.storeScanMayShare(decl, e.Value)
		}
		return true
	}
}

// storeScanParamWriterOf: trace only -- the writing node behind a true answer.
var storeScanParamWriterOf = map[storeScanParamKey]string{}

// storeScanIsEnumConstructor: storeFlowEnumVariantConstructor judged inside decl (it consults the
// current declaration's binders and type parameters).
func (a *Analyzer) storeScanIsEnumConstructor(decl *ast.FuncDecl, call *ast.CallExpr) bool {
	decl = a.readDecl(decl)
	saved := a.storeFlowCurrentDecl
	a.storeFlowCurrentDecl = decl
	defer func() { a.storeFlowCurrentDecl = saved }()
	// Resolve the enum name where decl is declared: scanned as a callee, the analyzer's current
	// namespace/usings are the caller's, and `Expr.Ident(..)` in another module looked unresolved.
	result := false
	a.withResolutionContext(a.declNamespace(decl), a.funcDeclUsings[decl], func() {
		result = a.storeFlowEnumVariantConstructor(call)
	})
	return result
}

// declNamespace: the namespace decl is declared in ("" at top level or when unknown).
func (a *Analyzer) declNamespace(decl *ast.FuncDecl) string {
	decl = a.readDecl(decl)
	sym := a.funcDeclSymbols[decl]
	if sym == nil {
		return a.currentNamespace
	}
	if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
		if idx := strings.LastIndex(fnType.Name, "."); idx >= 0 {
			return fnType.Name[:idx]
		}
		return ""
	}
	return a.currentNamespace
}

// storeScanHolds: locals that contain parts of some storage, and under which first projection.
type storeScanHolds struct {
	names  map[string]bool
	fields map[string]map[string]bool
}

// through: expr is a place at least depth projections under a holder, entered through a
// projection that leads to a part.
func (h storeScanHolds) through(expr ast.Expr, depth int) bool {
	root := rootIdentExpr(stripAddrAndParens(expr))
	if root == nil || !h.names[root.Name] || storeScanPlaceDepth(expr) < depth {
		return false
	}
	f := h.fields[root.Name]
	return f == nil || f["*"] || f[storeScanFirstProjection(expr)]
}

// storeScanFirstProjection: the field name of the projection applied directly to the root of a
// place (`e.target.x` -> "target"); "*" for an index or a bare root.
func storeScanFirstProjection(expr ast.Expr) string {
	proj := "*"
	for {
		switch e := stripAddrAndParens(expr).(type) {
		case *ast.FieldExpr:
			if e == nil {
				return proj
			}
			proj = e.Field
			expr = e.Object
		case *ast.IndexExpr:
			if e == nil {
				return proj
			}
			proj = "*"
			expr = e.Object
		default:
			return proj
		}
	}
}
